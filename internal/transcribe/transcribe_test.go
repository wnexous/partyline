package transcribe_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
	"github.com/wnexous/partyline/internal/transcribe"
)

const wait = 5 * time.Second

// request is what the fake service received in one call.
type request struct {
	model, format, filename, mime string
	audio                         []byte
}

// fakeService stands in for the OpenAI-compatible transcription server. By default it answers with
// text; block makes it hold every request until released or canceled; fail makes it answer a status.
type fakeService struct {
	srv *httptest.Server

	mu       sync.Mutex
	text     string
	status   int
	gate     chan struct{}
	requests []request
	started  chan struct{}
}

func newFakeService(t *testing.T) *fakeService {
	t.Helper()
	f := &fakeService{text: "hello from the voice note", started: make(chan struct{}, 16)}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeService) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/v1/audio/transcriptions" {
		http.NotFound(w, r)
		return
	}
	var got request
	if err := r.ParseMultipartForm(64 << 20); err == nil {
		got.model, got.format = r.FormValue("model"), r.FormValue("response_format")
		if file, h, err := r.FormFile("file"); err == nil {
			got.filename, got.mime = h.Filename, h.Header.Get("Content-Type")
			got.audio, _ = io.ReadAll(file)
			_ = file.Close()
		}
	}
	_, _ = io.Copy(io.Discard, r.Body)
	f.mu.Lock()
	f.requests = append(f.requests, got)
	gate, status, text := f.gate, f.status, f.text
	f.mu.Unlock()
	f.started <- struct{}{}
	if gate != nil {
		select {
		case <-gate:
		case <-r.Context().Done():
			return
		}
		f.mu.Lock()
		status, text = f.status, f.text
		f.mu.Unlock()
	}
	if status != 0 {
		w.WriteHeader(status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"text": text})
}

// block holds every request from now on; the returned function answers them with text.
func (f *fakeService) block() (release func(text string)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	gate := make(chan struct{})
	f.gate = gate
	return func(text string) {
		f.mu.Lock()
		f.text, f.gate = text, nil
		f.mu.Unlock()
		close(gate)
	}
}

func (f *fakeService) fail(status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status = status
}

func (f *fakeService) calls() []request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]request(nil), f.requests...)
}

// voiceRoom is a group of alice with her AI "builder" connected, on an app whose transcription
// service is svc.
type voiceRoom struct {
	env   *testkit.Env
	alice *testkit.Client
	room  dto.Room
	aiID  string
	ai    *testkit.AgentConn
	svc   *fakeService
}

func newVoiceRoom(t *testing.T, opts ...testkit.Option) *voiceRoom {
	t.Helper()
	svc := newFakeService(t)
	opts = append([]testkit.Option{
		testkit.WithEnv(map[string]string{"PARTYLINE_TRANSCRIBE_URL": svc.srv.URL}),
		testkit.WithTranscribeWait(func(context.Context, time.Duration) error { return nil }),
	}, opts...)
	return newRoomOn(t, testkit.New(t, opts...), svc)
}

func newRoomOn(t *testing.T, env *testkit.Env, svc *fakeService) *voiceRoom {
	t.Helper()
	alice := env.NewUser("alice")
	f := &voiceRoom{env: env, alice: alice, svc: svc}
	f.aiID = env.NewAI(alice, "builder")
	f.room = f.newRoom(t, "Support")
	conn, resp := alice.Token(dto.TokenKindAgent, "laptop").DialAgent("builder")
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	t.Cleanup(conn.Close)
	conn.Welcome(wait)
	f.ai = conn
	return f
}

// newRoom creates another group of alice with the AI in it.
func (f *voiceRoom) newRoom(t *testing.T, name string) dto.Room {
	t.Helper()
	r := testkit.Decode[dto.Room](f.alice.Post("/api/v1/rooms", dto.CreateRoomRequest{Name: name}).RequireStatus(http.StatusCreated))
	f.alice.Post("/api/v1/rooms/"+r.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: f.aiID}).RequireStatus(http.StatusCreated)
	return r
}

// postVoice posts a voice note (multipart, as the web app does) with the given text.
func (f *voiceRoom) postVoice(t *testing.T, roomID, text string, audio []byte) dto.Message {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormField(dto.MultipartMessageField)
	require.NoError(t, err)
	require.NoError(t, json.NewEncoder(part).Encode(dto.PostMessageRequest{Text: text}))
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, dto.MultipartFileField, "voice.webm"))
	h.Set("Content-Type", "audio/webm;codecs=opus")
	w, err := mw.CreatePart(h)
	require.NoError(t, err)
	_, err = w.Write(audio)
	require.NoError(t, err)
	require.NoError(t, mw.Close())
	return testkit.Decode[dto.Message](f.alice.DoRaw(http.MethodPost, "/api/v1/rooms/"+roomID+"/messages",
		mw.FormDataContentType(), &buf).RequireStatus(http.StatusCreated))
}

func (f *voiceRoom) postText(t *testing.T, roomID, text string) dto.Message {
	t.Helper()
	return testkit.Decode[dto.Message](f.alice.Post("/api/v1/rooms/"+roomID+"/messages",
		dto.PostMessageRequest{Text: text}).RequireStatus(http.StatusCreated))
}

// stored reads a message of the room as the web app does.
func (f *voiceRoom) stored(t *testing.T, roomID, messageID string) dto.Message {
	t.Helper()
	for _, m := range testkit.Decode[dto.Page[dto.Message]](f.alice.Get("/api/v1/rooms/" + roomID + "/messages").RequireStatus(http.StatusOK)).Items {
		if m.ID == messageID {
			return m
		}
	}
	require.FailNow(t, "message not found", messageID)
	return dto.Message{}
}

// rows counts the transcription rows of a message.
func (f *voiceRoom) rows(t *testing.T, messageID string) int {
	t.Helper()
	rows, err := f.env.App.Store.R().ListTranscriptionsByMessages(context.Background(), []string{messageID})
	require.NoError(t, err)
	return len(rows)
}

var audio = []byte("OggS fake opus bytes")

// TR-01, TR-03: a voice message without a transcript is transcribed by the service, and the AI gets it
// once, with the text under the attachment.
func TestAVoiceMessageReachesTheAIOnceWithTheServerTranscript(t *testing.T) {
	f := newVoiceRoom(t)
	release := f.svc.block()

	posted := f.postVoice(t, f.room.ID, "", audio)
	require.Equal(t, dto.TranscriptStatePending, posted.Attachments[0].TranscriptState)
	require.Empty(t, posted.Attachments[0].Transcript)
	release("hello from the voice note")

	got := f.ai.Message(wait)
	require.Equal(t, posted.ID, got.Message.ID)
	require.Equal(t, dto.TranscriptStateDone, got.Message.Attachments[0].TranscriptState)
	require.Equal(t, "hello from the voice note", got.Message.Attachments[0].Transcript)
	require.Empty(t, got.Message.Text, "the transcript is no edit of the text")
	f.ai.ExpectNone(400 * time.Millisecond)

	calls := f.svc.calls()
	require.Len(t, calls, 1)
	require.Equal(t, transcribe.DefaultModel, calls[0].model)
	require.Equal(t, "json", calls[0].format)
	require.Equal(t, "voice.webm", calls[0].filename)
	require.Equal(t, "audio/webm;codecs=opus", calls[0].mime)
	require.Equal(t, audio, calls[0].audio)

	m := f.stored(t, f.room.ID, posted.ID)
	require.Equal(t, dto.TranscriptStateDone, m.Attachments[0].TranscriptState)
	require.Equal(t, "hello from the voice note", m.Attachments[0].Transcript)
}

// TR-03: while a voice message waits for its transcript, the next message of its room waits behind it,
// so the AI gets both in order.
func TestTheNextMessageOfTheRoomWaitsBehindTheVoiceMessage(t *testing.T) {
	f := newVoiceRoom(t)
	release := f.svc.block()

	voice := f.postVoice(t, f.room.ID, "", audio)
	after := f.postText(t, f.room.ID, "and one more thing")
	f.ai.ExpectNone(500 * time.Millisecond)

	release("the voice note")
	got := f.ai.Message(wait)
	require.Equal(t, voice.ID, got.Message.ID)
	require.Equal(t, "the voice note", got.Message.Attachments[0].Transcript)
	got = f.ai.Message(wait)
	require.Equal(t, after.ID, got.Message.ID)
}

// TR-03: a voice message that waits holds its own room only.
func TestAVoiceMessageDoesNotHoldAnotherRoom(t *testing.T) {
	f := newVoiceRoom(t)
	other := f.newRoom(t, "Elsewhere")
	release := f.svc.block()

	voice := f.postVoice(t, f.room.ID, "", audio)
	elsewhere := f.postText(t, other.ID, "meanwhile")
	got := f.ai.Message(wait)
	require.Equal(t, elsewhere.ID, got.Message.ID, "the other room flows while the voice message waits")

	release("the voice note")
	got = f.ai.Message(wait)
	require.Equal(t, voice.ID, got.Message.ID)
	require.Equal(t, "the voice note", got.Message.Attachments[0].Transcript)
}

// TR-03, TR-02: past the limit the AI gets the audio alone, once; the transcript still reaches the web
// app when it arrives, live.
func TestASlowServiceDeliversTheAudioAloneAtTheLimit(t *testing.T) {
	f := newVoiceRoom(t, testkit.WithTranscriptHold(300*time.Millisecond))
	live, resp := f.alice.DialLive(f.env.Origin)
	require.NotNil(t, live, "upgrade refused: %d", resp.Status)
	live.Expect(dto.LiveEventTypeHello, nil, wait)
	release := f.svc.block()

	start := time.Now()
	voice := f.postVoice(t, f.room.ID, "", audio)
	got := f.ai.Message(wait)
	require.GreaterOrEqual(t, time.Since(start), 300*time.Millisecond)
	require.Equal(t, voice.ID, got.Message.ID)
	require.Equal(t, dto.TranscriptStatePending, got.Message.Attachments[0].TranscriptState)
	require.Empty(t, got.Message.Attachments[0].Transcript)

	var created dto.Message
	live.Expect(dto.LiveEventTypeRoomMessage, &created, wait)
	require.Equal(t, dto.TranscriptStatePending, created.Attachments[0].TranscriptState)

	release("late words")
	var updated dto.Message
	live.Expect(dto.LiveEventTypeRoomMessageUpdated, &updated, wait)
	require.Equal(t, voice.ID, updated.ID)
	require.Equal(t, dto.TranscriptStateDone, updated.Attachments[0].TranscriptState)
	require.Equal(t, "late words", updated.Attachments[0].Transcript)
	require.Nil(t, updated.EditedAt, "a transcript is no edit")
	f.ai.ExpectNone(500 * time.Millisecond)
}

// TR-06, TR-08: a service that keeps failing is tried three times; the row fails, the AI still gets
// the message, and nobody sees an error.
func TestAFailingServiceLeavesTheMessageAsItWas(t *testing.T) {
	f := newVoiceRoom(t)
	f.svc.fail(http.StatusInternalServerError)

	voice := f.postVoice(t, f.room.ID, "", audio)
	got := f.ai.Message(wait)
	require.Equal(t, voice.ID, got.Message.ID)
	require.Equal(t, dto.TranscriptStateFailed, got.Message.Attachments[0].TranscriptState)
	require.Empty(t, got.Message.Attachments[0].Transcript)
	require.Len(t, f.svc.calls(), transcribe.Attempts)
	require.Equal(t, dto.TranscriptStateFailed, f.stored(t, f.room.ID, voice.ID).Attachments[0].TranscriptState)
}

// TR-06: an answer another attempt would not fix fails the row at once.
func TestARefusedRequestIsNotRetried(t *testing.T) {
	f := newVoiceRoom(t)
	f.svc.fail(http.StatusBadRequest)

	voice := f.postVoice(t, f.room.ID, "", audio)
	got := f.ai.Message(wait)
	require.Equal(t, voice.ID, got.Message.ID)
	require.Equal(t, dto.TranscriptStateFailed, got.Message.Attachments[0].TranscriptState)
	require.Len(t, f.svc.calls(), 1)
}

// TR-05: a transcript the sender's browser made is the message text and is kept: nothing is sent to
// the service.
func TestABrowserTranscriptIsKept(t *testing.T) {
	f := newVoiceRoom(t)

	voice := f.postVoice(t, f.room.ID, "what I said", audio)
	got := f.ai.Message(wait)
	require.Equal(t, voice.ID, got.Message.ID)
	require.Equal(t, "what I said", got.Message.Text)
	require.Empty(t, got.Message.Attachments[0].TranscriptState)
	require.Zero(t, f.rows(t, voice.ID))
	require.Empty(t, f.svc.calls())
}

// TR-08: audio over the cap is not sent; the AI gets it at once, without a transcript.
func TestOversizedAudioIsSkipped(t *testing.T) {
	f := newVoiceRoom(t, testkit.WithEnv(map[string]string{"PARTYLINE_MAX_UPLOAD_MB": "26"}))

	voice := f.postVoice(t, f.room.ID, "", bytes.Repeat([]byte{1}, int(transcribe.MaxBytes)+1))
	require.Equal(t, dto.TranscriptStateSkipped, voice.Attachments[0].TranscriptState)
	got := f.ai.Message(wait)
	require.Equal(t, voice.ID, got.Message.ID)
	require.Equal(t, dto.TranscriptStateSkipped, got.Message.Attachments[0].TranscriptState)
	time.Sleep(200 * time.Millisecond)
	require.Empty(t, f.svc.calls())
}

// TR-06: without a service, a voice message is as it was: no row, no wait.
func TestWithoutAServiceNothingIsTranscribed(t *testing.T) {
	svc := newFakeService(t)
	f := newRoomOn(t, testkit.New(t), svc)

	voice := f.postVoice(t, f.room.ID, "", audio)
	got := f.ai.Message(wait)
	require.Equal(t, voice.ID, got.Message.ID)
	require.Empty(t, got.Message.Attachments[0].TranscriptState)
	require.Zero(t, f.rows(t, voice.ID))
	require.Empty(t, svc.calls())
}

// TR-08: a job interrupted by a restart is finished by the next run.
func TestARestartResumesAPendingTranscription(t *testing.T) {
	f := newVoiceRoom(t)
	release := f.svc.block()

	voice := f.postVoice(t, f.room.ID, "", audio)
	select {
	case <-f.svc.started:
	case <-time.After(wait):
		t.Fatal("the service got no request")
	}
	f.env.Restart() // cancels the request in flight; the row stays pending
	require.Equal(t, dto.TranscriptStatePending, f.stored(t, f.room.ID, voice.ID).Attachments[0].TranscriptState)

	release("after the restart")
	require.Eventually(t, func() bool {
		return f.stored(t, f.room.ID, voice.ID).Attachments[0].TranscriptState == dto.TranscriptStateDone
	}, wait, 20*time.Millisecond)
	require.Equal(t, "after the restart", f.stored(t, f.room.ID, voice.ID).Attachments[0].Transcript)
}

// TR-08: the jobs run one at a time, oldest first.
func TestJobsRunOneAtATimeOldestFirst(t *testing.T) {
	f := newVoiceRoom(t)
	release := f.svc.block()

	first := f.postVoice(t, f.room.ID, "", []byte("first audio"))
	second := f.postVoice(t, f.room.ID, "", []byte("second audio"))
	<-f.svc.started
	time.Sleep(200 * time.Millisecond)
	require.Len(t, f.svc.calls(), 1, "one job at a time")
	require.Equal(t, []byte("first audio"), f.svc.calls()[0].audio)

	release("words")
	got := f.ai.Message(wait)
	require.Equal(t, first.ID, got.Message.ID)
	got = f.ai.Message(wait)
	require.Equal(t, second.ID, got.Message.ID)
	require.Equal(t, "words", got.Message.Attachments[0].Transcript)
	calls := f.svc.calls()
	require.Len(t, calls, 2)
	require.Equal(t, []byte("second audio"), calls[1].audio)
}

// TR-01: only stored audio is a voice file; the size cap sends larger files to skipped.
func TestIsVoiceAndInitialState(t *testing.T) {
	require.True(t, transcribe.IsVoice("audio/ogg; codecs=opus", dto.AttachmentStatusStored))
	require.True(t, transcribe.IsVoice("Audio/MPEG", dto.AttachmentStatusStored))
	require.False(t, transcribe.IsVoice("audio/ogg", dto.AttachmentStatusTooLarge))
	require.False(t, transcribe.IsVoice("video/mp4", dto.AttachmentStatusStored))
	require.False(t, transcribe.IsVoice(strings.Repeat("x", 3), dto.AttachmentStatusStored))
	require.Equal(t, dto.TranscriptStatePending, transcribe.InitialState(transcribe.MaxBytes))
	require.Equal(t, dto.TranscriptStateSkipped, transcribe.InitialState(transcribe.MaxBytes+1))
}

// TR-03: a voice message deleted while it waits ends the wait: the AI is told nothing of it, and the
// room's next message follows at once.
func TestDeletingAHeldVoiceMessageEndsTheWait(t *testing.T) {
	f := newVoiceRoom(t)
	f.svc.block()

	voice := f.postVoice(t, f.room.ID, "", audio)
	f.alice.Delete("/api/v1/rooms/" + f.room.ID + "/messages/" + voice.ID).RequireStatus(http.StatusOK)
	after := f.postText(t, f.room.ID, "never mind")
	got := f.ai.Message(wait)
	require.Equal(t, after.ID, got.Message.ID)
	f.ai.ExpectNone(300 * time.Millisecond)
}

// TR-06: rows a run with a service left pending fail when the server starts without one, so no
// message shows "Transcribing…" for ever.
func TestStartingWithoutAServiceFailsTheLeftoverRows(t *testing.T) {
	f := newRoomOn(t, testkit.New(t), newFakeService(t))
	voice := f.postVoice(t, f.room.ID, "", audio)
	f.pendingRow(t, voice)
	require.Equal(t, dto.TranscriptStatePending, f.stored(t, f.room.ID, voice.ID).Attachments[0].TranscriptState)

	f.env.Restart()
	require.Equal(t, dto.TranscriptStateFailed, f.stored(t, f.room.ID, voice.ID).Attachments[0].TranscriptState)
}

// pendingRow writes the pending row the post of a voice message writes when a service is configured.
func (f *voiceRoom) pendingRow(t *testing.T, voice dto.Message) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, f.env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.CreateTranscription(ctx, db.CreateTranscriptionParams{
			AttachmentID: voice.Attachments[0].ID, MessageID: voice.ID, RoomID: f.room.ID,
			State: string(dto.TranscriptStatePending), CreatedAt: 1, UpdatedAt: 1,
		})
	}))
}

// ownWorker runs a worker of the test's own, on an app that has none (no URL), with change applied
// to its deps.
func ownWorker(t *testing.T, f *voiceRoom, change func(*transcribe.Deps)) {
	t.Helper()
	d := transcribe.Deps{
		Store: f.env.App.Store, Blobs: f.env.App.Blobs, Bus: f.env.App.Bus, URL: f.svc.srv.URL,
		Wait: func(context.Context, time.Duration) error { return nil },
	}
	change(&d)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = transcribe.New(d).Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
}

// TR-08: a store that fails to take the result is tried again after a wait, and the audio is not
// sent again.
func TestAFailingStoreDoesNotSendTheAudioAgain(t *testing.T) {
	f := newRoomOn(t, testkit.New(t), newFakeService(t))
	voice := f.postVoice(t, f.room.ID, "", audio)
	f.pendingRow(t, voice)

	var mu sync.Mutex
	var waits []time.Duration
	failures := 3
	ownWorker(t, f, func(d *transcribe.Deps) {
		d.Wait = func(_ context.Context, wait time.Duration) error {
			mu.Lock()
			defer mu.Unlock()
			waits = append(waits, wait)
			return nil
		}
		d.Tx = func(ctx context.Context, fn func(q *db.Queries) error) error {
			mu.Lock()
			fail := failures > 0
			failures--
			mu.Unlock()
			if fail {
				return errors.New("database is locked")
			}
			return f.env.App.Store.WithTx(ctx, fn)
		}
	})

	require.Eventually(t, func() bool {
		return f.stored(t, f.room.ID, voice.ID).Attachments[0].TranscriptState == dto.TranscriptStateDone
	}, wait, 20*time.Millisecond)
	require.Len(t, f.svc.calls(), 1, "the audio is sent once")
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []time.Duration{5 * time.Second, 5 * time.Second, 5 * time.Second}, waits)
}
