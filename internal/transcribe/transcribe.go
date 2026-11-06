// Package transcribe turns the voice files of room messages (TR-01) and of platform items (TR-04)
// into text on the server. The rows are written with the message (internal/rooms) or the item
// (internal/platform); one worker takes the rows of both kinds in one queue, oldest first, posts each
// audio file to an OpenAI-compatible transcription service (POST <URL>/v1/audio/transcriptions) and
// stores the text (TR-08). Logs never hold the audio or the text (TR-06).
package transcribe

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/attachments"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

// DefaultModel is the model asked for when PARTYLINE_TRANSCRIBE_MODEL is not set.
const DefaultModel = "Systran/faster-whisper-small"

// MaxBytes caps the audio sent to the service; a larger voice file is skipped (TR-08).
const MaxBytes int64 = 25 << 20

// Attempts is how many times a transient failure (network, 5xx, 429) is tried before the row fails.
const Attempts = 3

// RequestTimeout bounds one request to the service, so a service that hangs holds the queue for at
// most Attempts times as long.
const RequestTimeout = 90 * time.Second

// storeRetry is the wait before a store operation that failed is tried again: a failing store is
// never a reason to send the audio again.
const storeRetry = 5 * time.Second

// maxResponse caps the JSON answer read from the service.
const maxResponse = 1 << 20

// IsVoice reports whether a file of a message without text is one the server transcribes: audio
// that is stored here.
func IsVoice(mime string, status dto.AttachmentStatus) bool {
	return status == dto.AttachmentStatusStored && strings.HasPrefix(strings.ToLower(strings.TrimSpace(mime)), "audio/")
}

// InitialState is the state a voice file of size bytes starts in: pending, or skipped over MaxBytes.
func InitialState(size int64) dto.TranscriptState {
	if size > MaxBytes {
		return dto.TranscriptStateSkipped
	}
	return dto.TranscriptStatePending
}

// Deps configure a Worker; Store, Blobs, Bus and URL are required.
type Deps struct {
	Store *store.Store
	Blobs *attachments.Store
	Bus   *events.Bus
	// URL is the base URL of the service, e.g. http://whisper:8000.
	URL string
	// Model defaults to DefaultModel.
	Model string
	// Client defaults to a plain client with RequestTimeout: the URL is the operator's.
	Client *http.Client
	Logger *slog.Logger
	Now    func() time.Time
	// Wait sleeps between attempts; nil sleeps for real. Tests pass one that does not take seconds.
	Wait func(ctx context.Context, d time.Duration) error
	// Tx runs a write transaction; nil means Store.WithTx. Tests make it fail.
	Tx func(ctx context.Context, fn func(q *db.Queries) error) error
}

// Worker transcribes the pending rows one at a time, oldest first (TR-08).
type Worker struct {
	d        Deps
	endpoint string
}

// New returns a Worker.
func New(d Deps) *Worker {
	if d.Model == "" {
		d.Model = DefaultModel
	}
	if d.Client == nil {
		d.Client = &http.Client{Timeout: RequestTimeout}
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Wait == nil {
		d.Wait = sleep
	}
	if d.Tx == nil {
		d.Tx = d.Store.WithTx
	}
	return &Worker{d: d, endpoint: strings.TrimRight(d.URL, "/") + "/v1/audio/transcriptions"}
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Run works until ctx ends. It starts with the rows left pending by an earlier run, so a restart
// resumes them, and looks again after every new message and every queued platform item.
func (w *Worker) Run(ctx context.Context) error {
	sub, cancel := w.d.Bus.Subscribe(16)
	defer cancel()
	wake := make(chan struct{}, 1)
	wake <- struct{}{}
	go func() {
		for e := range sub {
			switch e.(type) {
			case events.MessageCreated, events.PlatformQueued, events.Lagged:
				select {
				case wake <- struct{}{}:
				default:
				}
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-wake:
		}
		for ctx.Err() == nil {
			job, err := w.next(ctx)
			if errors.Is(err, sql.ErrNoRows) {
				break
			}
			if err != nil {
				if ctx.Err() == nil {
					w.d.Logger.Error("transcribe: read the next job failed", "err", err)
				}
				if w.d.Wait(ctx, storeRetry) != nil {
					return nil
				}
				continue
			}
			w.process(ctx, job)
		}
	}
}

// job is one pending transcription: a voice file of a room message, or of a platform item when
// itemID is set.
type job struct {
	attachmentID string
	// messageID and roomID name the message of a room file.
	messageID, roomID string
	// itemID names the platform item of a platform file.
	itemID    string
	attempts  int64
	createdAt int64
	sha256    string
	mime      string
	name      string
}

// before orders the jobs of both kinds: oldest first, then by file id (TR-08).
func (j job) before(o job) bool {
	if j.createdAt != o.createdAt {
		return j.createdAt < o.createdAt
	}
	return j.attachmentID < o.attachmentID
}

// ids are the log attributes that name the job: never its audio or its text.
func (j job) ids() []any {
	if j.itemID != "" {
		return []any{"attachment_id", j.attachmentID, "item_id", j.itemID}
	}
	return []any{"attachment_id", j.attachmentID, "message_id", j.messageID}
}

// next returns the oldest pending job of both kinds, or sql.ErrNoRows when there is none.
func (w *Worker) next(ctx context.Context) (job, error) {
	q := w.d.Store.R()
	var out job
	found := false
	r, err := q.NextPendingTranscription(ctx)
	switch {
	case err == nil:
		out, found = job{
			attachmentID: r.AttachmentID, messageID: r.MessageID, roomID: r.RoomID, attempts: r.Attempts,
			createdAt: r.CreatedAt, sha256: r.Sha256, mime: r.Mime, name: r.Name,
		}, true
	case !errors.Is(err, sql.ErrNoRows):
		return job{}, err
	}
	p, err := q.NextPendingPlatformTranscription(ctx)
	switch {
	case err == nil:
		pj := job{
			attachmentID: p.AttachmentID, itemID: p.ItemID, attempts: p.Attempts,
			createdAt: p.CreatedAt, sha256: p.Sha256, mime: p.Mime, name: p.Name,
		}
		if !found || pj.before(out) {
			out, found = pj, true
		}
	case !errors.Is(err, sql.ErrNoRows):
		return job{}, err
	}
	if !found {
		return job{}, sql.ErrNoRows
	}
	return out, nil
}

// errPermanent marks a failure that another attempt would not fix.
var errPermanent = errors.New("permanent")

// process tries one job until it is done, failed or the worker stops; a stop leaves it pending.
func (w *Worker) process(ctx context.Context, j job) {
	start := w.d.Now()
	attempt := j.attempts
	for {
		attempt++
		text, err := w.transcribe(ctx, j)
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			w.finish(ctx, j, dto.TranscriptStateDone, text, attempt, start)
			return
		}
		if errors.Is(err, errPermanent) || attempt >= Attempts {
			w.d.Logger.Warn("transcribe: transcription failed", append(j.ids(), "attempts", attempt, "err", err)...)
			w.finish(ctx, j, dto.TranscriptStateFailed, "", attempt, start)
			return
		}
		var n int64
		if !w.write(ctx, "store the attempt", j.attachmentID, func(q *db.Queries) error {
			at := w.d.Now().UnixMilli()
			var err error
			if j.itemID != "" {
				n, err = q.SetPlatformTranscriptionAttempts(ctx, db.SetPlatformTranscriptionAttemptsParams{
					Attempts: attempt, UpdatedAt: at, AttachmentID: j.attachmentID,
				})
				return err
			}
			n, err = q.SetTranscriptionAttempts(ctx, db.SetTranscriptionAttemptsParams{
				Attempts: attempt, UpdatedAt: at, AttachmentID: j.attachmentID,
			})
			return err
		}) {
			return
		}
		if n == 0 {
			// The file went away meanwhile (its message deleted, its item acknowledged): nobody waits
			// for the text any more, so the audio is not sent again.
			w.d.Logger.Info("transcribe: the job went away, dropped", j.ids()...)
			return
		}
		w.d.Logger.Info("transcribe: attempt failed, retrying", append(j.ids(), "attempt", attempt, "err", err)...)
		if err := w.d.Wait(ctx, time.Duration(1<<(attempt-1))*2*time.Second); err != nil {
			return
		}
	}
}

// finish stores the outcome and tells the dispatcher and the live clients (TR-02, TR-03), or the
// platform feed of the item's AI (TR-04).
func (w *Worker) finish(ctx context.Context, j job, state dto.TranscriptState, text string, attempts int64, start time.Time) {
	var stored bool
	var agentID string
	if !w.write(ctx, "store the result", j.attachmentID, func(q *db.Queries) error {
		at := w.d.Now().UnixMilli()
		if j.itemID != "" {
			var err error
			agentID, err = q.FinishPlatformTranscription(ctx, db.FinishPlatformTranscriptionParams{
				State: string(state), Text: text, Attempts: attempts, UpdatedAt: at, AttachmentID: j.attachmentID,
			})
			stored = err == nil
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		n, err := q.FinishTranscription(ctx, db.FinishTranscriptionParams{
			State: string(state), Text: text, Attempts: attempts, UpdatedAt: at, AttachmentID: j.attachmentID,
		})
		stored = n > 0
		return err
	}) {
		return // the worker stops: the row stays pending for the next run
	}
	if !stored {
		return // the file went away meanwhile, or its item was acknowledged
	}
	if state == dto.TranscriptStateDone {
		w.d.Logger.Info("transcribe: transcribed", append(j.ids(), "attempts", attempts,
			"duration_ms", w.d.Now().Sub(start).Milliseconds())...)
	}
	if j.itemID != "" {
		w.d.Bus.Publish(events.PlatformTranscribed{AgentID: agentID, ItemID: j.itemID})
		return
	}
	w.d.Bus.Publish(events.MessageTranscribed{RoomID: j.roomID, MessageID: j.messageID})
}

// write runs fn in a write transaction until it succeeds, waiting storeRetry after each failure. It
// returns false when the worker stops first.
func (w *Worker) write(ctx context.Context, what, attachmentID string, fn func(q *db.Queries) error) bool {
	for {
		err := w.d.Tx(ctx, fn)
		if err == nil {
			return true
		}
		if ctx.Err() != nil {
			return false
		}
		w.d.Logger.Error("transcribe: "+what+" failed, retrying", "attachment_id", attachmentID, "err", err)
		if w.d.Wait(ctx, storeRetry) != nil {
			return false
		}
	}
}

// transcribe posts the file to the service and returns the text it heard.
func (w *Worker) transcribe(ctx context.Context, j job) (string, error) {
	f, err := w.d.Blobs.Open(j.sha256)
	if err != nil {
		return "", fmt.Errorf("%w: open the audio: %w", errPermanent, err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("%w: open the audio: %w", errPermanent, err)
	}
	body, size, contentType, err := form(w.d.Model, j.name, j.mime, f, info.Size())
	if err != nil {
		return "", fmt.Errorf("%w: %w", errPermanent, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.endpoint, body)
	if err != nil {
		return "", fmt.Errorf("%w: %w", errPermanent, err)
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", contentType)
	resp, err := w.d.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request: %w", err) // transient
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests:
		return "", fmt.Errorf("service answered %d", resp.StatusCode)
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return "", fmt.Errorf("%w: service answered %d", errPermanent, resp.StatusCode)
	}
	var out struct {
		Text *string `json:"text"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponse)).Decode(&out); err != nil || out.Text == nil {
		return "", fmt.Errorf("%w: the answer is not a transcription", errPermanent)
	}
	return clip(strings.TrimSpace(*out.Text)), nil
}

// form is the multipart body of a request: the model, the response format and the file. The audio
// is streamed from the blob between the parts written around it, with a known length, so services
// that refuse a chunked body take it too.
func form(model, name, storedMime string, audio io.Reader, audioSize int64) (io.Reader, int64, string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("model", model); err != nil {
		return nil, 0, "", err
	}
	if err := mw.WriteField("response_format", "json"); err != nil {
		return nil, 0, "", err
	}
	mediaType := partType(storedMime)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", multipart.FileContentDisposition("file", partName(name, mediaType)))
	h.Set("Content-Type", mediaType)
	if _, err := mw.CreatePart(h); err != nil {
		return nil, 0, "", err
	}
	head := bytes.Clone(buf.Bytes())
	buf.Reset()
	if err := mw.Close(); err != nil {
		return nil, 0, "", err
	}
	tail := buf.Bytes()
	size := int64(len(head)) + audioSize + int64(len(tail))
	return io.MultiReader(bytes.NewReader(head), io.LimitReader(audio, audioSize), bytes.NewReader(tail)), size, mw.FormDataContentType(), nil
}

// partType is the stored type of the file as the part header carries it: an audio type parsed and
// written again, so no stray character of a type a platform sent reaches the header, else
// application/octet-stream.
func partType(stored string) string {
	mediaType, params, err := mime.ParseMediaType(stored)
	if err != nil || !strings.HasPrefix(mediaType, "audio/") {
		return "application/octet-stream"
	}
	if out := mime.FormatMediaType(mediaType, params); out != "" {
		return out
	}
	return "application/octet-stream"
}

// extensions names the file of an audio type for services that tell the format by the extension.
var extensions = map[string]string{
	"audio/webm": ".webm", "audio/ogg": ".ogg", "audio/opus": ".ogg",
	"audio/mp4": ".m4a", "audio/m4a": ".m4a", "audio/x-m4a": ".m4a", "audio/aac": ".aac",
	"audio/mpeg": ".mp3", "audio/mp3": ".mp3",
	"audio/wav": ".wav", "audio/x-wav": ".wav", "audio/wave": ".wav", "audio/vnd.wave": ".wav",
	"audio/flac": ".flac", "audio/x-flac": ".flac",
}

// audioExtensions are the extensions a service knows as audio; a name with another one, or none,
// gets its type's.
var audioExtensions = map[string]bool{
	".webm": true, ".ogg": true, ".oga": true, ".opus": true, ".m4a": true, ".mp4": true, ".aac": true,
	".mp3": true, ".mpga": true, ".mpeg": true, ".wav": true, ".flac": true,
}

// partName is the file name the part carries: the stored name without control characters or path,
// "voice" when nothing is left, with the extension of its type when it has no audio extension.
func partName(stored, mediaType string) string {
	name := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '/' || r == '\\' {
			return -1
		}
		return r
	}, stored)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		name = "voice"
	}
	if !audioExtensions[strings.ToLower(path.Ext(name))] {
		base, _, _ := strings.Cut(mediaType, ";")
		name += extensions[base]
	}
	return name
}

// clip keeps a transcript within the length of a message.
func clip(s string) string {
	if utf8.RuneCountInString(s) <= dto.MaxTextLength {
		return s
	}
	return string([]rune(s)[:dto.MaxTextLength])
}
