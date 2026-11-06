// Package transcribe turns the voice files of room messages into text on the server (TR-01). The
// rows are written with the message (internal/rooms); one worker takes them oldest first, posts each
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
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"time"
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

// RequestTimeout bounds one request to the service.
const RequestTimeout = 5 * time.Minute

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
// resumes them, and looks again after every new message.
func (w *Worker) Run(ctx context.Context) error {
	sub, cancel := w.d.Bus.Subscribe(16)
	defer cancel()
	wake := make(chan struct{}, 1)
	wake <- struct{}{}
	go func() {
		for e := range sub {
			switch e.(type) {
			case events.MessageCreated, events.Lagged:
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
			job, err := w.d.Store.R().NextPendingTranscription(ctx)
			if errors.Is(err, sql.ErrNoRows) {
				break
			}
			if err != nil {
				if ctx.Err() == nil {
					w.d.Logger.Error("transcribe: read the next job failed", "err", err)
				}
				break
			}
			w.process(ctx, job)
		}
	}
}

// errPermanent marks a failure that another attempt would not fix.
var errPermanent = errors.New("permanent")

// process tries one job until it is done, failed or the worker stops; a stop leaves it pending.
func (w *Worker) process(ctx context.Context, job db.NextPendingTranscriptionRow) {
	start := w.d.Now()
	attempt := job.Attempts
	for {
		attempt++
		text, err := w.transcribe(ctx, job)
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			w.finish(ctx, job, dto.TranscriptStateDone, text, attempt, start)
			return
		}
		if errors.Is(err, errPermanent) || attempt >= Attempts {
			w.d.Logger.Warn("transcribe: transcription failed", "attachment_id", job.AttachmentID,
				"message_id", job.MessageID, "attempts", attempt, "err", err)
			w.finish(ctx, job, dto.TranscriptStateFailed, "", attempt, start)
			return
		}
		w.d.Logger.Info("transcribe: attempt failed, retrying", "attachment_id", job.AttachmentID,
			"attempt", attempt, "err", err)
		if err := w.d.Store.WithTx(ctx, func(q *db.Queries) error {
			return q.SetTranscriptionAttempts(ctx, db.SetTranscriptionAttemptsParams{
				Attempts: attempt, UpdatedAt: w.d.Now().UnixMilli(), AttachmentID: job.AttachmentID,
			})
		}); err != nil {
			if ctx.Err() == nil {
				w.d.Logger.Error("transcribe: store the attempt failed", "attachment_id", job.AttachmentID, "err", err)
			}
			return
		}
		if err := w.d.Wait(ctx, time.Duration(1<<(attempt-1))*2*time.Second); err != nil {
			return
		}
	}
}

// finish stores the outcome and tells the dispatcher and the live clients (TR-02, TR-03).
func (w *Worker) finish(ctx context.Context, job db.NextPendingTranscriptionRow, state dto.TranscriptState, text string, attempts int64, start time.Time) {
	var n int64
	err := w.d.Store.WithTx(ctx, func(q *db.Queries) error {
		var err error
		n, err = q.FinishTranscription(ctx, db.FinishTranscriptionParams{
			State: string(state), Text: text, Attempts: attempts, UpdatedAt: w.d.Now().UnixMilli(), AttachmentID: job.AttachmentID,
		})
		return err
	})
	if err != nil {
		if ctx.Err() == nil {
			w.d.Logger.Error("transcribe: store the result failed", "attachment_id", job.AttachmentID, "err", err)
		}
		return
	}
	if n == 0 {
		return // the file went away meanwhile
	}
	if state == dto.TranscriptStateDone {
		w.d.Logger.Info("transcribe: transcribed", "attachment_id", job.AttachmentID, "message_id", job.MessageID,
			"attempts", attempts, "duration_ms", w.d.Now().Sub(start).Milliseconds())
	}
	w.d.Bus.Publish(events.MessageTranscribed{RoomID: job.RoomID, MessageID: job.MessageID})
}

// transcribe posts the file to the service and returns the text it heard.
func (w *Worker) transcribe(ctx context.Context, job db.NextPendingTranscriptionRow) (string, error) {
	f, err := w.d.Blobs.Open(job.Sha256)
	if err != nil {
		return "", fmt.Errorf("%w: open the audio: %w", errPermanent, err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("%w: open the audio: %w", errPermanent, err)
	}
	body, size, contentType, err := form(w.d.Model, job, f, info.Size())
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
func form(model string, job db.NextPendingTranscriptionRow, audio io.Reader, audioSize int64) (io.Reader, int64, string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("model", model); err != nil {
		return nil, 0, "", err
	}
	if err := mw.WriteField("response_format", "json"); err != nil {
		return nil, 0, "", err
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", multipart.FileContentDisposition("file", job.Name))
	h.Set("Content-Type", job.Mime)
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

// clip keeps a transcript within the length of a message.
func clip(s string) string {
	if utf8.RuneCountInString(s) <= dto.MaxTextLength {
		return s
	}
	return string([]rune(s)[:dto.MaxTextLength])
}
