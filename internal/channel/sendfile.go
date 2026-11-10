package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/wnexous/partyline/internal/api/dto"
)

// The sizes and time bounds of send_file (LF-02).
const (
	// multipartMaxBytes is the largest file send_file posts to a room in one multipart request; a
	// larger one goes up through an upload session, in chunks. The client cannot know the server's
	// single-request cap (PARTYLINE_MAX_UPLOAD_MB, and the proxy in front of it), and past a few MB a
	// session costs only two small requests more while a failed chunk is resent alone instead of the
	// whole file. A multipart post the server refuses as too large goes up in chunks as well.
	multipartMaxBytes = 8 << 20
	// legacyRequestBytes is the largest file a server without upload sessions, or one whose
	// connections take no uploads yet, gets in one request: the default PARTYLINE_MAX_UPLOAD_MB.
	legacyRequestBytes = 95 << 20
	// chunkTries is how many times one chunk is sent before the upload gives up: once and three
	// retries. A chunk that brings the upload further starts the count again.
	chunkTries = 4
	// chunkTimeout bounds one chunk (at most 50 MB): a stalled request fails and is retried.
	chunkTimeout = 5 * time.Minute
	// maxRetryWait is the longest Retry-After a chunk waits for; a longer one fails the upload.
	maxRetryWait = time.Minute
	// fileToolTimeout bounds a whole send_file or download_attachment, a file of gigabytes included.
	// Every request has its own bound, a download its stall, and the retries are counted, so it is a
	// safety net.
	fileToolTimeout = 6 * time.Hour
)

// chunkRetryWait is the wait before the first retry of a chunk; each further one waits once more.
const chunkRetryWait = 250 * time.Millisecond

// errNoUploads is a server that takes no upload sessions (one before upload sessions existed).
var errNoUploads = errors.New("this Partyline server takes no upload sessions")

// localFile is the file send_file sends, open for reading: its body is streamed from disk and never
// held in memory.
type localFile struct {
	f    *os.File
	name string
	mime string
	size int64
}

// openFile opens a regular local file for send_file.
func openFile(path string) (localFile, error) {
	f, err := os.Open(path) // #nosec G304 -- the session asks to share this local file
	if err != nil {
		return localFile{}, fmt.Errorf("read %s: %w", path, errors.Unwrap(err))
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return localFile{}, fmt.Errorf("read %s: %w", path, errors.Unwrap(err))
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		if info.IsDir() {
			return localFile{}, fmt.Errorf("read %s: is a directory", path)
		}
		return localFile{}, fmt.Errorf("read %s: not a regular file", path)
	}
	name := filepath.Base(path)
	ct := mime.TypeByExtension(filepath.Ext(name))
	if ct == "" {
		ct = "application/octet-stream"
	}
	return localFile{f: f, name: name, mime: ct, size: info.Size()}, nil
}

// The refusals of send_file about where a file goes.
const (
	msgFileSeveralTargets = "give either room, or connection with chat, not both: room uploads the file to a room or DM you belong to, connection with chat sends it to a chat of a connection you operate"
	msgFileNoTarget       = "room, or connection with chat, is required: name the room to upload the file to, or the connection and the chat to send it to"
	msgFileQuote          = "reply_to applies to rooms: a file sent through a connection is not a quote; to answer a message from a connection with a file, give its connection and chat"
)

// sendFile uploads a local file to a room, or sends it to a chat of a connection the AI operates
// (connection and chat, CID-32). It opens the file only once the target is known to be one, and
// streams it: a large file goes to a room in chunks through an upload session (LF-02).
func (t *tools) sendFile(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Room       string `json:"room"`
		Connection string `json:"connection"`
		Chat       string `json:"chat"`
		Path       string `json:"path"`
		Caption    string `json:"caption"`
		ReplyTo    string `json:"reply_to"`
	}
	if err := decode(args, &a); err != nil {
		return "", err
	}
	// An empty value is not given, as models that send every parameter do (DM-04).
	hasRoom, hasConnection, hasChat := strings.TrimSpace(a.Room) != "", strings.TrimSpace(a.Connection) != "", strings.TrimSpace(a.Chat) != ""
	switch {
	case hasConnection != hasChat:
		return "", errors.New(msgHalfTarget)
	case hasRoom && hasConnection:
		return "", errors.New(msgFileSeveralTargets)
	case !hasRoom && !hasConnection:
		return "", errors.New(msgFileNoTarget)
	}
	if err := required("path", a.Path); err != nil {
		return "", err
	}
	if hasConnection && strings.TrimSpace(a.ReplyTo) != "" {
		return "", errors.New(msgFileQuote)
	}
	f, err := openFile(a.Path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.f.Close() }()
	if hasConnection {
		return t.sendFileToChat(ctx, f, strings.TrimSpace(a.Connection), strings.TrimSpace(a.Chat), a.Caption)
	}
	return t.sendFileToRoom(ctx, f, a.Room, dto.PostMessageRequest{Text: a.Caption, ReplyTo: a.ReplyTo})
}

// sendFileToRoom posts f to room with the caption and reply_to of msg: in one multipart request when
// it is small, else through an upload session whose id the post then attaches.
func (t *tools) sendFileToRoom(ctx context.Context, f localFile, room string, msg dto.PostMessageRequest) (string, error) {
	if f.size > multipartMaxBytes {
		id, err := t.uploadFile(ctx, f, "")
		switch {
		case errors.Is(err, errNoUploads) && f.size <= legacyRequestBytes:
			// A server before upload sessions still takes a file of one request.
		case err != nil:
			return "", err
		default:
			return t.postUploads(ctx, room, msg, id)
		}
	}
	resp, err := t.postMultipart(ctx, roomPath(room, "/messages"), msg, f)
	if isTooLarge(err) {
		// The server's single-request cap is below this file: send it in chunks (LF-02).
		id, err := t.uploadFile(ctx, f, "")
		if err != nil {
			return "", err
		}
		return t.postUploads(ctx, room, msg, id)
	}
	if err != nil {
		return "", fileError(err, f)
	}
	defer func() { _ = resp.Body.Close() }()
	var m dto.Message
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return "", fmt.Errorf("partyline: unexpected answer: %w", err)
	}
	return sent(m), nil
}

// postUploads posts msg to room with the finished upload id attached. When the post fails the upload
// is deleted: another send_file uploads the file again.
func (t *tools) postUploads(ctx context.Context, room string, msg dto.PostMessageRequest, id string) (string, error) {
	postCtx, cancel := context.WithTimeout(ctx, toolTimeout)
	defer cancel()
	msg.UploadIDs = []string{id}
	var m dto.Message
	if err := t.api.json(postCtx, http.MethodPost, roomPath(room, "/messages"), nil, msg, &m); err != nil {
		t.dropUpload(ctx, id)
		return "", fileError(err, localFile{})
	}
	return sent(m), nil
}

// dropTimeout bounds the deletion of an upload that failed.
const dropTimeout = 10 * time.Second

// dropUpload deletes an upload that failed for good, best-effort, so that it does not hold one of the
// owner's places until it expires. It runs also when the tool call was cancelled; an upload a message
// used already, or that the server removed, is 404 and nothing to do. The server may still be writing
// the chunk that failed (409 upload_busy): the delete waits for it, within dropTimeout.
func (t *tools) dropUpload(ctx context.Context, id string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), dropTimeout)
	defer cancel()
	for {
		err := t.api.json(ctx, http.MethodDelete, "agent/uploads/"+url.PathEscape(id), nil, nil, nil)
		var se *statusError
		if !errors.As(err, &se) || se.code != dto.ErrorCodeUploadBusy {
			return
		}
		if sleep(ctx, chunkRetryWait) != nil {
			return
		}
	}
}

// sendFileToChat sends f to a chat of a connection: in one multipart request when it is small, else
// through an upload session created for the connection, which the server refuses before any chunk is
// sent when the platform could not take the file (LF-05), and whose id the send then attaches (LF-04).
func (t *tools) sendFileToChat(ctx context.Context, f localFile, connection, chat, caption string) (string, error) {
	path := "agent/connections/" + url.PathEscape(connection) + "/chats/" + url.PathEscape(chat) + "/messages"
	msg := dto.SendToChatRequest{Text: caption}
	if f.size > multipartMaxBytes {
		id, err := t.uploadFile(ctx, f, connection)
		switch {
		case errors.Is(err, errNoUploads) && f.size <= legacyRequestBytes:
			// A server before upload sessions still takes a file of one request.
		case errors.Is(err, errNoUploads):
			return "", connectionTooLarge(f, legacyRequestBytes)
		case err != nil:
			return "", err
		default:
			return t.postChatUpload(ctx, path, msg, id, f)
		}
	}
	resp, err := t.postMultipart(ctx, path, msg, f)
	if isTooLarge(err) {
		// The server's single-request cap is below this file: send it in chunks.
		id, err := t.uploadFile(ctx, f, connection)
		if errors.Is(err, errNoUploads) {
			return "", connectionTooLarge(f, 0)
		}
		if err != nil {
			return "", err
		}
		return t.postChatUpload(ctx, path, msg, id, f)
	}
	if err != nil {
		return "", fileError(err, f)
	}
	defer func() { _ = resp.Body.Close() }()
	var res dto.PlatformSendResult
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", fmt.Errorf("partyline: unexpected answer: %w", err)
	}
	return platformSent(res), nil
}

// postChatUpload sends msg to the chat at path with the finished upload id as its file. The server sends
// it to the platform before it answers, which for a large file takes as long as the upload to the
// platform: the request waits for the server's own bound of the attempt. When the post fails the upload
// is deleted (one the send used up is gone already). A server whose connections take no uploads yet gets
// the file in one request when it fits.
func (t *tools) postChatUpload(ctx context.Context, path string, msg dto.SendToChatRequest, id string, f localFile) (string, error) {
	postCtx, cancel := context.WithTimeout(ctx, connectionSendTimeout(f.size))
	defer cancel()
	msg.UploadIDs = []string{id}
	var res dto.PlatformSendResult
	err := t.api.json(postCtx, http.MethodPost, path, nil, msg, &res)
	if err == nil {
		return platformSent(res), nil
	}
	t.dropUpload(ctx, id)
	if takesNoUploads(err) {
		if f.size > legacyRequestBytes {
			return "", connectionTooLarge(f, legacyRequestBytes)
		}
		resp, err := t.postMultipart(ctx, path, dto.SendToChatRequest{Text: msg.Text}, f)
		if err != nil {
			return "", fileError(err, f)
		}
		defer func() { _ = resp.Body.Close() }()
		if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
			return "", fmt.Errorf("partyline: unexpected answer: %w", err)
		}
		return platformSent(res), nil
	}
	return "", fileError(err, f)
}

// connectionSendTimeout bounds the post of a file of size bytes through a connection: the time of a tool
// call on top of the server's bound of the attempt (a minute and a half, and a second for every MB).
func connectionSendTimeout(size int64) time.Duration {
	return toolTimeout + 90*time.Second + time.Duration(size>>20)*time.Second
}

// takesNoUploads reports whether a server refused upload_ids on a send through a connection, as servers
// before they streamed a connection's files did.
func takesNoUploads(err error) bool {
	var se *statusError
	return errors.As(err, &se) && se.status == http.StatusBadRequest && strings.Contains(se.msg, "upload_ids cannot be sent through a connection")
}

// connectionTooLarge is the refusal of a file too large for a server whose connections take no upload
// sessions; maxBytes is the limit when it is known.
func connectionTooLarge(f localFile, maxBytes int64) error {
	limit := "more than this Partyline server takes in one request"
	if maxBytes > 0 {
		limit = "over the " + humanSize(maxBytes) + " this Partyline server takes through a connection"
	}
	return fmt.Errorf("file too large: %s is %s, %s; a newer Partyline server sends larger files through a connection. Nothing was sent: share it in a room with send_file and room instead, or send a smaller file", f.name, humanSize(f.size), limit)
}

// postMultipart posts message (the JSON of the message part) and f as one multipart request,
// streamed from the file with its exact length, so the server refuses a body over its limit before
// it is sent.
func (t *tools) postMultipart(ctx context.Context, path string, message any, f localFile) (*http.Response, error) {
	ct, b, err := multipartBody(message, f)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, transferTimeout(f.size))
	resp, err := t.api.stream(ctx, http.MethodPost, path, nil, ct, b)
	if err != nil {
		cancel()
		return nil, err
	}
	resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

// cancelOnClose cancels the context of a response's request once its body is closed.
type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

// transferTimeout bounds a request that carries size bytes: the time of a tool call, and a second
// more for every 256 KB.
func transferTimeout(size int64) time.Duration {
	return toolTimeout + time.Duration(size>>18)*time.Second
}

// multipartBody lays out a multipart body of the message part and the file part of f. The framing is
// a few hundred bytes held in memory; the file's bytes are read from disk as the body is sent.
func multipartBody(message any, f localFile) (string, streamBody, error) {
	var frame bytes.Buffer
	mw := multipart.NewWriter(&frame)
	part, err := mw.CreateFormField(dto.MultipartMessageField)
	if err != nil {
		return "", streamBody{}, err
	}
	// The message part is a PostMessageRequest for a room and a SendToChatRequest for a chat; the
	// caption is the text of both.
	if err := json.NewEncoder(part).Encode(message); err != nil {
		return "", streamBody{}, err
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": dto.MultipartFileField, "filename": f.name}))
	h.Set("Content-Type", f.mime)
	if _, err := mw.CreatePart(h); err != nil {
		return "", streamBody{}, err
	}
	// The file's bytes go between the header of its part and the closing boundary.
	head := frame.Len()
	if err := mw.Close(); err != nil {
		return "", streamBody{}, err
	}
	all := frame.Bytes()
	prefix, suffix := all[:head], all[head:]
	open := func() io.Reader {
		return io.MultiReader(bytes.NewReader(prefix), io.NewSectionReader(f.f, 0, f.size), bytes.NewReader(suffix))
	}
	return mw.FormDataContentType(), streamBody{open: open, length: int64(len(all)) + f.size}, nil
}

// uploadFile sends f through an upload session, chunk by chunk as the server sizes them, and returns
// the id of the finished upload. connection, when set, names the connection the file is for: the server
// refuses a file it could not send before any chunk goes (LF-05). A chunk that fails is sent again up to three times, from where the
// server says the upload stands: a chunk is kept whole or not at all, and one whose answer was lost
// may have arrived (409 upload_offset says where to resume).
func (t *tools) uploadFile(ctx context.Context, f localFile, connection string) (string, error) {
	u, err := t.createUpload(ctx, f, connection)
	if err != nil {
		return "", err
	}
	id, err := t.sendChunks(ctx, u, f)
	if err != nil {
		// Given up: the upload goes, rather than holding a place until it expires.
		t.dropUpload(ctx, u.ID)
		return "", err
	}
	return id, nil
}

// sendChunks sends the chunks of f to the upload u until it is complete, and returns its id.
func (t *tools) sendChunks(ctx context.Context, u dto.Upload, f localFile) (string, error) {
	best, failures := u.Received, 0
	for !u.Complete {
		if u.Received < 0 || u.Received >= f.size || u.ChunkSize <= 0 {
			return "", fmt.Errorf("partyline: unexpected answer: upload at %d of %d bytes, chunk size %d", u.Received, f.size, u.ChunkSize)
		}
		next, err := t.putChunk(ctx, u, f)
		switch {
		case err == nil:
			u = next
		case ctx.Err() != nil:
			return "", ctx.Err()
		case !retryable(err):
			return "", uploadStopped(fileError(err, f), f, u.Received)
		default:
			failures++
			if received, complete, ok := uploadOffset(err); ok {
				// The upload stands elsewhere, most likely further on: a lost answer.
				u.Received, u.Complete = received, complete
				break
			}
			if failures >= chunkTries {
				return "", uploadStopped(fmt.Errorf("a chunk failed %d times: %w", failures, err), f, u.Received)
			}
			wait := time.Duration(failures) * chunkRetryWait
			var se *statusError
			if errors.As(err, &se) && se.retryAfter > 0 {
				if se.retryAfter > maxRetryWait {
					return "", uploadStopped(fileError(err, f), f, u.Received)
				}
				wait = se.retryAfter
			}
			if err := sleep(ctx, wait); err != nil {
				return "", err
			}
			if now, err := t.getUpload(ctx, u.ID); err == nil {
				u = now
			}
		}
		if u.Received > best {
			best, failures = u.Received, 0
		}
		if failures >= chunkTries {
			return "", uploadStopped(fmt.Errorf("a chunk failed %d times: %w", failures, err), f, u.Received)
		}
	}
	return u.ID, nil
}

// createUpload starts the upload session of f, for the connection when one is named; errNoUploads when
// the server has no such route.
func (t *tools) createUpload(ctx context.Context, f localFile, connection string) (dto.Upload, error) {
	ctx, cancel := context.WithTimeout(ctx, toolTimeout)
	defer cancel()
	var u dto.Upload
	req := dto.CreateUploadRequest{Name: f.name, Mime: f.mime, Size: f.size, Connection: connection}
	err := t.api.json(ctx, http.MethodPost, "agent/uploads", nil, req, &u)
	var se *statusError
	if errors.As(err, &se) && se.status == http.StatusNotFound {
		return dto.Upload{}, errNoUploads
	}
	if err != nil {
		return dto.Upload{}, fileError(err, f)
	}
	return u, nil
}

// putChunk sends the chunk of f that starts where u stands, read from disk as it is sent.
func (t *tools) putChunk(ctx context.Context, u dto.Upload, f localFile) (dto.Upload, error) {
	off := u.Received
	n := min(u.ChunkSize, f.size-off)
	ctx, cancel := context.WithTimeout(ctx, chunkTimeout)
	defer cancel()
	q := url.Values{"offset": {strconv.FormatInt(off, 10)}}
	b := streamBody{open: func() io.Reader { return io.NewSectionReader(f.f, off, n) }, length: n}
	resp, err := t.api.stream(ctx, http.MethodPut, "agent/uploads/"+url.PathEscape(u.ID), q, "application/octet-stream", b)
	if err != nil {
		return dto.Upload{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	var next dto.Upload
	if err := json.NewDecoder(resp.Body).Decode(&next); err != nil {
		return dto.Upload{}, fmt.Errorf("partyline: unexpected answer: %w", err)
	}
	return next, nil
}

// getUpload reads where an upload stands.
func (t *tools) getUpload(ctx context.Context, id string) (dto.Upload, error) {
	ctx, cancel := context.WithTimeout(ctx, toolTimeout)
	defer cancel()
	var u dto.Upload
	err := t.api.json(ctx, http.MethodGet, "agent/uploads/"+url.PathEscape(id), nil, nil, &u)
	return u, err
}

// retryable reports whether a failed chunk may be sent again: the server could not be reached, the
// request stalled, the answer was cut, the server failed, or it asked to wait. The upload being gone
// (404), a refused size (413), a full disk (507) and a refused token are final.
func retryable(err error) bool {
	if errors.Is(err, errInvalidToken) || errors.Is(err, context.Canceled) {
		return false
	}
	var se *statusError
	if errors.As(err, &se) {
		switch se.status {
		case http.StatusNotFound, http.StatusRequestEntityTooLarge, http.StatusInsufficientStorage,
			http.StatusUnauthorized, http.StatusForbidden:
			return false
		}
	}
	return true
}

// uploadOffset is where a 409 upload_offset says the upload stands.
func uploadOffset(err error) (received int64, complete, ok bool) {
	var se *statusError
	if !errors.As(err, &se) || se.code != dto.ErrorCodeUploadOffset {
		return 0, false, false
	}
	received, perr := strconv.ParseInt(se.details["received"], 10, 64)
	if perr != nil || received < 0 {
		return 0, false, false
	}
	return received, se.details["complete"] == "true", true
}

// uploadStopped says how far the upload of f went before err stopped it.
func uploadStopped(err error, f localFile, received int64) error {
	return fmt.Errorf("%s was not sent: its upload stopped at %s of %s: %w", f.name, humanSize(received), humanSize(f.size), err)
}

// isTooLarge reports whether the server refused a request body over its single-request cap.
func isTooLarge(err error) bool {
	var se *statusError
	return errors.As(err, &se) && se.status == http.StatusRequestEntityTooLarge && se.code == dto.ErrorCodeTooLarge
}

// fileError words the refusals about a file's size, the disk and the rate limits for the AI; other
// errors are returned as they are.
func fileError(err error, f localFile) error {
	var se *statusError
	if !errors.As(err, &se) {
		return err
	}
	switch {
	case se.code == dto.ErrorCodeFileTooLarge && se.details["platform"] != "":
		// LF-05: the platform of the connection takes smaller files than this one.
		limit := "more than " + se.details["platform"] + " takes"
		if maxBytes, perr := strconv.ParseInt(se.details["max_size"], 10, 64); perr == nil && maxBytes > 0 {
			limit = "over the " + humanSize(maxBytes) + " " + se.details["platform"] + " takes"
		}
		return fmt.Errorf("file_too_large: %s is %s, %s for a file sent through a connection; nothing was sent: send a smaller file, or share it in a room with send_file and room instead", f.name, humanSize(f.size), limit)
	case se.code == dto.ErrorCodeFileTooLarge:
		limit := "more than this Partyline server takes"
		if maxBytes, perr := strconv.ParseInt(se.details["max_size"], 10, 64); perr == nil && maxBytes > 0 {
			limit = "and this Partyline server takes files up to " + humanSize(maxBytes)
		}
		return fmt.Errorf("file_too_large: %s is %s, %s; nothing was sent: send a smaller file, or split it", f.name, humanSize(f.size), limit)
	case se.status == http.StatusInsufficientStorage:
		what := "this file"
		if f.name != "" {
			what = f.name + " (" + humanSize(f.size) + ")"
		}
		return fmt.Errorf("insufficient_storage: the Partyline server does not have enough free disk space for %s; nothing was sent: send a smaller file, or ask whoever runs the server to free space", what)
	case se.status == http.StatusTooManyRequests:
		msg := se.msg
		if se.retryAfter > 0 {
			msg += "; try again in " + humanWait(se.retryAfter)
		}
		return errors.New(msg)
	}
	return err
}

// humanSize renders a byte count: bytes, KB, MB or GB (of 1024).
func humanSize(n int64) string {
	one := func(v float64) string { return strings.TrimSuffix(strconv.FormatFloat(v, 'f', 1, 64), ".0") }
	switch {
	case n >= 1<<30:
		return one(float64(n)/(1<<30)) + " GB"
	case n >= 1<<20:
		return one(float64(n)/(1<<20)) + " MB"
	case n >= 1<<10:
		return strconv.FormatInt(n>>10, 10) + " KB"
	}
	return strconv.FormatInt(n, 10) + " bytes"
}

// humanWait renders a wait in seconds, minutes or hours.
func humanWait(d time.Duration) string {
	switch {
	case d >= 2*time.Hour:
		return strconv.FormatInt(int64(d/time.Hour), 10) + " hours"
	case d >= 2*time.Minute:
		return strconv.FormatInt(int64(d/time.Minute), 10) + " minutes"
	}
	n := int64((d + time.Second - 1) / time.Second)
	if n == 1 {
		return "1 second"
	}
	return strconv.FormatInt(n, 10) + " seconds"
}

// sleep waits d or until ctx is done.
func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// stallWatch cancels a transfer that brings no byte for its stall time: every read that brings bytes
// starts the wait again, so a slow but steady transfer goes on for as long as it takes.
type stallWatch struct {
	stall   time.Duration
	timer   *time.Timer
	stalled atomic.Bool
}

// watchStall starts watching a transfer under ctx, waiting for its answer first.
func (t *tools) watchStall(ctx context.Context) (context.Context, *stallWatch) {
	w := &stallWatch{stall: t.stall}
	if w.stall <= 0 {
		w.stall = readStall
	}
	ctx, cancel := context.WithCancel(ctx)
	w.timer = time.AfterFunc(w.stall, func() {
		w.stalled.Store(true)
		cancel()
	})
	return ctx, w
}

// reader is r, each read that brings bytes postponing the stall.
func (w *stallWatch) reader(r io.Reader) io.Reader { return &stallReader{r: r, w: w} }

func (w *stallWatch) stop() { w.timer.Stop() }

// explain says that err came of a stall, when it did.
func (w *stallWatch) explain(err error) error {
	if err == nil || !w.stalled.Load() {
		return err
	}
	return fmt.Errorf("download stalled: the server sent nothing for %s; try again", humanWait(w.stall))
}

type stallReader struct {
	r io.Reader
	w *stallWatch
}

func (s *stallReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if n > 0 && !s.w.stalled.Load() {
		s.w.timer.Reset(s.w.stall)
	}
	return n, err
}
