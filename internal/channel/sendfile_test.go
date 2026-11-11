package channel

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/testkit"
)

// sentRequest is a request of the agent API a recorder saw.
type sentRequest struct {
	method, path, contentType string
	length                    int64
}

// fault is what a recorder does to a chunk.
type fault int

const (
	// cutChunk sends half of the chunk's body, then fails the request as a dropped connection does.
	cutChunk fault = iota + 1
	// loseAnswer lets the chunk reach the server, then fails the request as if its answer was lost.
	loseAnswer
)

// recorder is an HTTP transport that records the agent API requests and injects faults into the
// chunks of upload sessions, by their order (1 is the first PUT).
type recorder struct {
	base http.RoundTripper
	// faults maps the number of a chunk request to its fault.
	faults map[int]fault
	// noUploads answers 404 to the creation of an upload session, as a server without them does.
	noUploads bool
	// proxyTimeout lets a send through a connection reach the server, then answers 524 as a proxy
	// that gave up waiting does.
	proxyTimeout bool
	mu           sync.Mutex
	// slowDownload, when set, gives the body of an attachment download 4 KB at a time, each read
	// after this wait, or never a byte when it is noBytes (setSlowDownload).
	slowDownload time.Duration
	reqs         []sentRequest
	puts         int
}

func newRecorder() *recorder {
	return &recorder{base: http.DefaultTransport.(*http.Transport).Clone(), faults: map[int]fault{}}
}

func (r *recorder) client() *http.Client { return &http.Client{Transport: r} }

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.reqs = append(r.reqs, sentRequest{method: req.Method, path: req.URL.Path, contentType: req.Header.Get("Content-Type"), length: req.ContentLength})
	var f fault
	if req.Method == http.MethodPut && strings.HasPrefix(req.URL.Path, "/api/v1/agent/uploads/") {
		r.puts++
		f = r.faults[r.puts]
	}
	noUploads := r.noUploads && req.Method == http.MethodPost && req.URL.Path == "/api/v1/agent/uploads"
	cut := r.proxyTimeout && req.Method == http.MethodPost && strings.Contains(req.URL.Path, "/connections/")
	slow := r.slowDownload
	r.mu.Unlock()
	switch {
	case cut:
		resp, err := r.base.RoundTrip(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
		return &http.Response{
			StatusCode: 524, Header: http.Header{"Content-Type": {"text/html"}}, Request: req,
			Body: io.NopCloser(strings.NewReader("<html>A timeout occurred</html>")),
		}, nil
	case noUploads:
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return &http.Response{
			StatusCode: http.StatusNotFound, Header: http.Header{"Content-Type": {"application/json"}}, Request: req,
			Body: io.NopCloser(strings.NewReader(`{"error":{"code":"not_found","message":"not found"}}`)),
		}, nil
	case f == cutChunk:
		req.Body = &cutBody{r: req.Body, left: req.ContentLength / 2}
		req.GetBody = nil // a dropped connection is not replayed by the transport
		resp, err := r.base.RoundTrip(req)
		if err == nil {
			_ = resp.Body.Close()
			return nil, errors.New("connection dropped")
		}
		return nil, err
	case f == loseAnswer:
		resp, err := r.base.RoundTrip(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
		return nil, errors.New("answer lost")
	}
	resp, err := r.base.RoundTrip(req)
	if err == nil && slow != 0 && strings.HasPrefix(req.URL.Path, "/api/v1/agent/attachments/") {
		resp.Body = &slowBody{ReadCloser: resp.Body, wait: slow, ctx: req.Context()}
	}
	return resp, err
}

func (r *recorder) setSlowDownload(d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.slowDownload = d
}

// noBytes is a slowDownload whose body never brings a byte: a read waits until the request is canceled.
const noBytes time.Duration = -1

// slowBody gives its body 4 KB at a time, each read after wait (never, for noBytes), and stops when the
// context of its request is done.
type slowBody struct {
	io.ReadCloser
	wait time.Duration
	ctx  context.Context
}

func (s *slowBody) Read(p []byte) (int, error) {
	var due <-chan time.Time // nil for noBytes: never
	if s.wait != noBytes {
		timer := time.NewTimer(s.wait)
		defer timer.Stop()
		due = timer.C
	}
	select {
	case <-due:
	case <-s.ctx.Done():
		return 0, s.ctx.Err()
	}
	return s.ReadCloser.Read(p[:min(len(p), 4<<10)])
}

// sent returns the requests of the agent API whose path holds part.
func (r *recorder) sent(part string) []sentRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []sentRequest
	for _, q := range r.reqs {
		if strings.Contains(q.path, part) {
			out = append(out, q)
		}
	}
	return out
}

// cutBody gives left bytes of r, then fails.
type cutBody struct {
	r    io.ReadCloser
	left int64
}

func (c *cutBody) Read(p []byte) (int, error) {
	if c.left <= 0 {
		return 0, errors.New("cut")
	}
	if int64(len(p)) > c.left {
		p = p[:c.left]
	}
	n, err := c.r.Read(p)
	c.left -= int64(n)
	return n, err
}

func (c *cutBody) Close() error { return c.r.Close() }

// randomFile writes size pseudo-random bytes to a new file named name, a block at a time, and returns
// its path and its sha256.
func randomFile(t *testing.T, name string, size int64) (string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	f, err := os.Create(path)
	require.NoError(t, err)
	h := sha256.New()
	rng := rand.New(rand.NewPCG(uint64(size), 7))
	block := make([]byte, 64<<10)
	for left := size; left > 0; {
		n := min(int64(len(block)), left)
		for i := 0; i < len(block); i += 8 {
			binary.LittleEndian.PutUint64(block[i:], rng.Uint64())
		}
		_, err := f.Write(block[:n])
		require.NoError(t, err)
		h.Write(block[:n])
		left -= n
	}
	require.NoError(t, f.Close())
	return path, hex.EncodeToString(h.Sum(nil))
}

// fileSHA256 hashes a file as a stream.
func fileSHA256(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path) // #nosec G304 -- a test file
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	h := sha256.New()
	_, err = io.Copy(h, f)
	require.NoError(t, err)
	return hex.EncodeToString(h.Sum(nil))
}

// requireDelivered checks that the newest message of the room is the agent's with the caption and
// the one attachment named name, and that downloading it gives back the bytes of sha.
func requireDelivered(t *testing.T, e *chanEnv, s *session, text, name string, size int64, sha string) dto.Message {
	t.Helper()
	stored := e.messages(e.room.ID)[0]
	require.Equal(t, "sent (message_id "+stored.ID+")", text)
	require.Equal(t, e.agentID, stored.AuthorID)
	require.Len(t, stored.Attachments, 1)
	require.Equal(t, []any{name, size}, []any{stored.Attachments[0].Name, stored.Attachments[0].Size})
	path, isErr := s.call("download_attachment", toolArgs("attachment_id", stored.Attachments[0].ID))
	require.False(t, isErr, path)
	require.Equal(t, sha, fileSHA256(t, path), "the attachment is the file, byte for byte")
	return stored
}

// settleAllocs waits until the process allocates less than 64 KB in 50 ms, at most 10 s, so the work
// a step started in the background (the dispatcher's handling of a post, the channel's notification of
// it) is done before a measure of allocations begins.
func settleAllocs(t *testing.T) {
	t.Helper()
	var m runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m)
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		last := m.TotalAlloc
		time.Sleep(50 * time.Millisecond)
		runtime.ReadMemStats(&m)
		if m.TotalAlloc-last < 64<<10 {
			return
		}
	}
}

// LF-02: a file over the size of one multipart post goes to the room through an upload session, in
// chunks of the size the server gives (a lowered PARTYLINE_MAX_UPLOAD_MB), streamed from the file:
// the room gets one attachment, byte-identical, with the caption and reply_to; no multipart post is
// tried, and the channel holds no more than a few chunks' worth of memory. The tools-only server (pi,
// OpenCode) runs the same tool.
func TestSendFileGoesUpInChunks(t *testing.T) {
	for _, toolsOnly := range []bool{false, true} {
		t.Run("tools-only="+strconv.FormatBool(toolsOnly), func(t *testing.T) {
			e := newChanEnv(t, testkit.WithEnv(map[string]string{"PARTYLINE_MAX_UPLOAD_MB": "1"}))
			rec := newRecorder()
			o := e.options()
			o.HTTPClient = rec.client()
			var s *session
			if toolsOnly {
				o.ToolsOnly = true
				s = startSession(t, o)
				s.handshake()
			} else {
				s = e.connected(o)
			}
			question := e.post("send the recording")
			if !toolsOnly {
				require.Equal(t, question.ID, s.notification().Meta["message_id"])
			}
			// Four times the size of one multipart post: what the server and the channel allocate grows with
			// the chunks, while what other work of the process allocates in the meantime does not, so the
			// bound keeps room for it on a slow runner.
			size := int64(4*multipartMaxBytes) + 4321
			path, sha := randomFile(t, "recording.bin", size)

			var before, after runtime.MemStats
			settleAllocs(t)
			runtime.ReadMemStats(&before)
			text, isErr := s.call("send_file", toolArgs("room", "Support", "path", path, "caption", "the recording", "reply_to", question.ID))
			runtime.ReadMemStats(&after)
			require.False(t, isErr, text)
			// The server and the channel run in this process: together they allocate a fraction of the
			// file, where a body built in memory would allocate it whole at least once.
			require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(size/2), "allocated while sending")

			stored := requireDelivered(t, e, s, text, "recording.bin", size, sha)
			require.Equal(t, []string{"the recording", question.ID}, []string{stored.Text, stored.ReplyTo})

			require.Len(t, rec.sent("/api/v1/agent/uploads"), 1+33, "one session and 33 chunks of up to 1 MB")
			for _, put := range rec.sent("/api/v1/agent/uploads/") {
				require.Equal(t, http.MethodPut, put.method)
				require.LessOrEqual(t, put.length, int64(1<<20))
				require.Positive(t, put.length, "every chunk has its exact length")
			}
			posts := rec.sent("/messages")
			require.Len(t, posts, 1)
			require.Equal(t, "application/json", posts[0].contentType, "the post attaches the upload, it carries no file")
		})
	}
}

// LF-02: a chunk whose connection drops half way is sent again whole, and a chunk whose answer was lost
// after it arrived is not sent twice: the channel asks the server where the upload stands and goes on
// from there. The room gets the file byte for byte.
func TestSendFileResumesAnInterruptedChunk(t *testing.T) {
	e := newChanEnv(t, testkit.WithEnv(map[string]string{"PARTYLINE_MAX_UPLOAD_MB": "1"}))
	rec := newRecorder()
	rec.faults[2] = cutChunk   // the second chunk: dropped half way
	rec.faults[3] = loseAnswer // its retry arrives, but its answer is lost
	rec.faults[6] = loseAnswer // a later chunk, too
	o := e.options()
	o.HTTPClient = rec.client()
	s := e.connected(o)
	size := int64(multipartMaxBytes) + 3<<19
	path, sha := randomFile(t, "video.mp4", size)

	text, isErr := s.call("send_file", toolArgs("room", "Support", "path", path))
	require.False(t, isErr, text)
	requireDelivered(t, e, s, text, "video.mp4", size, sha)

	gets := 0
	for _, q := range rec.sent("/api/v1/agent/uploads/") {
		if q.method == http.MethodGet {
			gets++
		}
	}
	require.Equal(t, 3, gets, "each failure asks where the upload stands")
	// Ten chunks, and one more for the chunk that was cut: the chunks whose answers were lost were
	// not sent again.
	require.Len(t, rec.sent("/api/v1/agent/uploads/"), 10+1+gets)
}

// LF-02: a chunk that keeps failing stops the upload after three retries, and the tool says how far it
// went; nothing is posted.
func TestSendFileGivesUpAfterThreeRetries(t *testing.T) {
	e := newChanEnv(t, testkit.WithEnv(map[string]string{"PARTYLINE_MAX_UPLOAD_MB": "1"}))
	rec := newRecorder()
	for i := 2; i <= 5; i++ {
		rec.faults[i] = cutChunk
	}
	o := e.options()
	o.HTTPClient = rec.client()
	s := e.connected(o)
	before := len(e.messages(e.room.ID))
	path, _ := randomFile(t, "archive.zip", int64(multipartMaxBytes)+1)

	text, isErr := s.call("send_file", toolArgs("room", "Support", "path", path))
	require.True(t, isErr)
	require.Contains(t, text, "archive.zip was not sent: its upload stopped at 1 MB of 8 MB: a chunk failed 4 times")
	require.Equal(t, 5, rec.puts, "the first chunk, then one chunk sent four times")
	require.Len(t, e.messages(e.room.ID), before)
	requireUploadsDeleted(t, e, rec, 1)
}

// requireUploadsDeleted checks that the channel deleted the upload sessions, sending at least n
// deletes, and that the server holds none any more.
func requireUploadsDeleted(t *testing.T, e *chanEnv, rec *recorder, n int) {
	t.Helper()
	deletes := 0
	for _, q := range rec.sent("/api/v1/agent/uploads/") {
		if q.method == http.MethodDelete {
			deletes++
		}
	}
	require.GreaterOrEqual(t, deletes, n, "a delete is sent again while the failed chunk is still being written")
	ids, err := e.env.App.Store.R().ListUploadIDs(context.Background())
	require.NoError(t, err)
	require.Empty(t, ids, "the upload that failed holds no place on the server")
}

// LF-02: an upload whose post fails (a room the AI is not in) is deleted; the tool says why it failed.
func TestSendFileDeletesTheUploadWhenThePostFails(t *testing.T) {
	e := newChanEnv(t)
	rec := newRecorder()
	o := e.options()
	o.HTTPClient = rec.client()
	s := e.connected(o)
	path, _ := randomFile(t, "notes.bin", int64(multipartMaxBytes)+1)

	text, isErr := s.call("send_file", toolArgs("room", "Elsewhere", "path", path))
	require.True(t, isErr)
	require.Contains(t, text, "Elsewhere")
	require.Len(t, rec.sent("/messages"), 1)
	requireUploadsDeleted(t, e, rec, 1)
}

// LF-02: a small file stays one multipart post, streamed with its exact length; no upload session.
func TestSendFileSmallFileIsOneMultipartPost(t *testing.T) {
	e := newChanEnv(t)
	rec := newRecorder()
	o := e.options()
	o.HTTPClient = rec.client()
	s := e.connected(o)
	size := int64(300 << 10)
	path, sha := randomFile(t, "photo.jpg", size)

	text, isErr := s.call("send_file", toolArgs("room", "Support", "path", path, "caption", "a photo"))
	require.False(t, isErr, text)
	requireDelivered(t, e, s, text, "photo.jpg", size, sha)
	require.Empty(t, rec.sent("/api/v1/agent/uploads"))
	posts := rec.sent("/messages")
	require.Len(t, posts, 1)
	require.True(t, strings.HasPrefix(posts[0].contentType, "multipart/form-data; boundary="), posts[0].contentType)
	require.Greater(t, posts[0].length, size, "the body has its length: the file and its framing")
}

// LF-02: a small file over a server's single-request cap is refused as one post before it is sent, and
// goes up in chunks instead.
func TestSendFileOverTheRequestCapGoesUpInChunks(t *testing.T) {
	e := newChanEnv(t, testkit.WithEnv(map[string]string{"PARTYLINE_MAX_UPLOAD_MB": "1"}))
	rec := newRecorder()
	o := e.options()
	o.HTTPClient = rec.client()
	s := e.connected(o)
	size := int64(3<<20 + 99)
	path, sha := randomFile(t, "scan.pdf", size)

	text, isErr := s.call("send_file", toolArgs("room", "Support", "path", path))
	require.False(t, isErr, text)
	requireDelivered(t, e, s, text, "scan.pdf", size, sha)
	posts := rec.sent("/messages")
	require.Len(t, posts, 2, "the refused post, then the post of the upload")
	require.True(t, strings.HasPrefix(posts[0].contentType, "multipart/form-data"))
	require.Equal(t, "application/json", posts[1].contentType)
	require.Len(t, rec.sent("/api/v1/agent/uploads/"), 4)
}

// LF-02: a server without upload sessions still gets a file of one request as a multipart post.
func TestSendFileWithoutUploadSessionsPostsTheFile(t *testing.T) {
	e := newChanEnv(t)
	rec := newRecorder()
	rec.noUploads = true
	o := e.options()
	o.HTTPClient = rec.client()
	s := e.connected(o)
	size := int64(multipartMaxBytes) + 1
	path, sha := randomFile(t, "slides.pdf", size)

	text, isErr := s.call("send_file", toolArgs("room", "Support", "path", path))
	require.False(t, isErr, text)
	requireDelivered(t, e, s, text, "slides.pdf", size, sha)
	posts := rec.sent("/messages")
	require.Len(t, posts, 1)
	require.True(t, strings.HasPrefix(posts[0].contentType, "multipart/form-data"))
}

// LF-02, LF-03, LF-07: the refusals of an upload session read as what they are: a file over the
// largest one names the limit, and a full disk says that nothing was sent.
func TestSendFileRefusals(t *testing.T) {
	t.Run("file too large", func(t *testing.T) {
		e := newChanEnv(t, testkit.WithEnv(map[string]string{"PARTYLINE_MAX_UPLOAD_MB": "1", "PARTYLINE_MAX_FILE_MB": "2"}))
		s := e.connected(e.options())
		before := len(e.messages(e.room.ID))
		path, _ := randomFile(t, "movie.mkv", 3<<20)
		text, isErr := s.call("send_file", toolArgs("room", "Support", "path", path))
		require.True(t, isErr)
		require.Equal(t, "file_too_large: movie.mkv is 3 MB, and this Partyline server takes files up to 2 MB; nothing was sent: send a smaller file, or split it", text)
		require.Len(t, e.messages(e.room.ID), before)
	})
	t.Run("insufficient storage", func(t *testing.T) {
		e := newChanEnv(t, testkit.WithFreeSpace(func(string) (int64, bool) { return 1 << 20, true }))
		s := e.connected(e.options())
		path, _ := randomFile(t, "backup.tar", int64(multipartMaxBytes)+1)
		text, isErr := s.call("send_file", toolArgs("room", "Support", "path", path))
		require.True(t, isErr)
		require.Equal(t, "insufficient_storage: the Partyline server does not have enough free disk space for backup.tar (8 MB); nothing was sent: send a smaller file, or ask whoever runs the server to free space", text)
	})
}

// LF-02: a rate limit says when to try again, from Retry-After.
func TestFileErrorRateLimited(t *testing.T) {
	err := fileError(&statusError{status: http.StatusTooManyRequests, code: dto.ErrorCodeRateLimited, retryAfter: 90 * time.Second,
		msg: "rate_limited: too many uploads: post or wait for the ones you have before starting another"}, localFile{name: "a.bin"})
	require.EqualError(t, err, "rate_limited: too many uploads: post or wait for the ones you have before starting another; try again in 90 seconds")
	require.Equal(t, "3 hours", humanWait(3*time.Hour))
	require.Equal(t, "5 minutes", humanWait(5*time.Minute))
}

// LF-02, LF-04: a file over the size of one multipart post goes to a chat of a connection through an
// upload session created for the connection, in chunks streamed from the file; the send then attaches it
// with a JSON post, and the platform gets the file byte for byte with the caption.
func TestSendFileThroughAConnectionGoesUpInChunks(t *testing.T) {
	c := newConnectionChan(t, testkit.WithEnv(map[string]string{"PARTYLINE_MAX_UPLOAD_MB": "4"}))
	rec := newRecorder()
	o := c.options()
	o.HTTPClient = rec.client()
	s := c.connected(o)
	size := int64(multipartMaxBytes) + 1<<20 + 77
	path, sha := randomFile(t, "recording.mp4", size)

	text, isErr := s.call("send_file", toolArgs("connection", c.conn, "chat", c.group.ID, "path", path, "caption", "the recording"))
	require.False(t, isErr, text)
	require.Contains(t, text, "sent")
	sent := c.inst.WaitSend(t)
	require.Equal(t, []any{"the recording", "recording.mp4", size}, []any{sent.Text, sent.Files[0].Name, sent.Files[0].Size})
	r, err := sent.Files[0].Open()
	require.NoError(t, err)
	h := sha256.New()
	_, err = io.Copy(h, r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	require.Equal(t, sha, hex.EncodeToString(h.Sum(nil)), "the platform got the file")

	require.Len(t, rec.sent("/api/v1/agent/uploads/"), 3, "three chunks of 4 MB")
	posts := rec.sent("/connections/")
	require.Len(t, posts, 1)
	require.Equal(t, "application/json", posts[0].contentType, "the send attaches the upload, it carries no file")
}

// LF-05: a file over the platform's own limit is refused when its upload session is created, before a
// chunk of it is read or sent, in words that name the limit and the platform; a small one over it is
// refused the same from its multipart post.
func TestSendFileThroughAConnectionOverThePlatformLimit(t *testing.T) {
	c := newConnectionChanOfKind(t, func(k *connectors.Kind) { k.MaxFileBytes = 10 << 20 })
	rec := newRecorder()
	o := c.options()
	o.HTTPClient = rec.client()
	s := c.connected(o)
	path := filepath.Join(t.TempDir(), "huge.mov")
	f, err := os.Create(path)
	require.NoError(t, err)
	require.NoError(t, f.Truncate(12<<20)) // sparse: nothing written, nothing read
	require.NoError(t, f.Close())

	text, isErr := s.call("send_file", toolArgs("connection", c.conn, "chat", c.group.ID, "path", path))
	require.True(t, isErr)
	require.Equal(t, "file_too_large: huge.mov is 12 MB, over the 10 MB Fake takes for a file sent through a connection; nothing was sent: send a smaller file, or share it in a room with send_file and room instead", text)
	require.Len(t, rec.sent("/api/v1/agent/uploads"), 1, "the session was refused")
	require.Empty(t, rec.sent("/api/v1/agent/uploads/"), "no chunk was sent")
	require.Empty(t, rec.sent("/connections/"))

	small := newConnectionChanOfKind(t, func(k *connectors.Kind) { k.MaxFileBytes = 1000 })
	ss := small.connected(small.options())
	path, _ = randomFile(t, "photo.jpg", 2000)
	text, isErr = ss.call("send_file", toolArgs("connection", small.conn, "chat", small.group.ID, "path", path))
	require.True(t, isErr)
	require.Equal(t, "file_too_large: photo.jpg is 1 KB, over the 1000 bytes Fake takes for a file sent through a connection; nothing was sent: send a smaller file, or share it in a room with send_file and room instead", text)
	require.Empty(t, small.inst.Sent())
}

// LF-04: a send through a connection whose answer a proxy cut off (524) may have gone out, and here
// did: the tool says that the file may have been delivered and to check the chat, and keeps the upload,
// which the server refuses to send twice. The same holds for a small file of one multipart post.
func TestSendFileThroughAConnectionWhoseAnswerIsLost(t *testing.T) {
	c := newConnectionChan(t)
	rec := newRecorder()
	rec.proxyTimeout = true
	o := c.options()
	o.HTTPClient = rec.client()
	s := c.connected(o)
	path, _ := randomFile(t, "recording.mp4", int64(multipartMaxBytes)+1)

	text, isErr := s.call("send_file", toolArgs("connection", c.conn, "chat", c.group.ID, "path", path))
	require.True(t, isErr)
	require.Contains(t, text, "recording.mp4 may have been delivered")
	require.Contains(t, text, "check the chat before sending it again")
	require.Equal(t, int64(multipartMaxBytes)+1, c.inst.WaitSend(t).Files[0].Size, "it was")
	for _, q := range rec.sent("/api/v1/agent/uploads/") {
		require.NotEqual(t, http.MethodDelete, q.method, "the upload is kept")
	}

	path, _ = randomFile(t, "photo.jpg", 2000)
	text, isErr = s.call("send_file", toolArgs("connection", c.conn, "chat", c.group.ID, "path", path))
	require.True(t, isErr)
	require.Contains(t, text, "photo.jpg may have been delivered")
	c.inst.WaitSend(t)
}

// LF-02: a server whose single-request cap is below a small file refuses its multipart post from its
// length; the file then goes in chunks and arrives.
func TestSendFileThroughAConnectionOverTheServersRequestCap(t *testing.T) {
	c := newConnectionChan(t, testkit.WithEnv(map[string]string{"PARTYLINE_MAX_UPLOAD_MB": "1"}))
	s := c.connected(c.options())
	path, _ := randomFile(t, "clip.mp4", 3<<20)
	text, isErr := s.call("send_file", toolArgs("connection", c.conn, "chat", c.group.ID, "path", path))
	require.False(t, isErr, text)
	require.Equal(t, int64(3<<20), c.inst.WaitSend(t).Files[0].Size)
}

// LF-02: a server without upload sessions still gets a file of one request through a connection, and a
// larger one is refused before anything is read or sent.
func TestSendFileThroughAConnectionToAServerWithoutUploads(t *testing.T) {
	c := newConnectionChan(t)
	rec := newRecorder()
	rec.noUploads = true
	o := c.options()
	o.HTTPClient = rec.client()
	s := c.connected(o)
	path, _ := randomFile(t, "clip.mp4", int64(multipartMaxBytes)+1)
	text, isErr := s.call("send_file", toolArgs("connection", c.conn, "chat", c.group.ID, "path", path))
	require.False(t, isErr, text)
	require.Equal(t, int64(multipartMaxBytes)+1, c.inst.WaitSend(t).Files[0].Size)

	path = filepath.Join(t.TempDir(), "huge.mov")
	f, err := os.Create(path)
	require.NoError(t, err)
	require.NoError(t, f.Truncate(120<<20))
	require.NoError(t, f.Close())
	text, isErr = s.call("send_file", toolArgs("connection", c.conn, "chat", c.group.ID, "path", path))
	require.True(t, isErr)
	require.Equal(t, "file too large: huge.mov is 120 MB, over the 95 MB this Partyline server takes through a connection; a newer Partyline server sends larger files through a connection. Nothing was sent: share it in a room with send_file and room instead, or send a smaller file", text)
	require.Len(t, rec.sent("/connections/"), 1, "only the first file was posted")
}

// LF-08: a download that keeps bringing bytes goes on past the bound of an ordinary tool call and past
// the stall time, streamed to disk; one that brings nothing for the stall time fails and says so. The
// bounds are shortened here: 200 ms for a tool call, 1 s of stall, and a download of 32 reads of 4 KB,
// each 40 ms after the last, so at least 1.28 s. The stall's wait starts before the request, so it also
// covers the wait for the answer: 1 s leaves a slow runner most of a second for it, while the download
// still lasts longer than the stall only because every read that brings bytes starts its wait again.
func TestDownloadAttachmentOutlastsTheToolBoundWhileBytesArrive(t *testing.T) {
	const toolLimit, stall = 200 * time.Millisecond, time.Second
	e := newChanEnv(t)
	content := strings.Repeat("0123456789abcdef", 128<<10/16)
	m := e.postFile("big.txt", content)

	rec := newRecorder()
	rec.setSlowDownload(40 * time.Millisecond)
	o := e.options()
	o.HTTPClient = rec.client()
	o.toolLimit, o.readStall = toolLimit, stall
	s := e.connected(o)

	start := time.Now()
	path, isErr := s.call("download_attachment", toolArgs("attachment_id", m.Attachments[0].ID))
	require.False(t, isErr, path)
	require.Greater(t, time.Since(start), stall, "longer than the stall, and so than the bound of a tool call")
	got, err := os.ReadFile(path) // #nosec G304 -- the test's download
	require.NoError(t, err)
	require.Equal(t, content, string(got))

	// The answer comes and then no byte: the download ends by the stall, whatever the runner's speed.
	rec.setSlowDownload(noBytes)
	text, isErr := s.call("download_attachment", toolArgs("attachment_id", m.Attachments[0].ID))
	require.True(t, isErr)
	require.Equal(t, "download stalled: the server sent nothing for 1 second; try again", text)
}
