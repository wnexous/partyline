package transcribe_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/channel"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/platform"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
	"github.com/wnexous/partyline/internal/transcribe"
)

// voiceConnection is alice's connection operated by her AI "relay", running on a fake platform, with
// the AI connected as a client that receives platform events.
type voiceConnection struct {
	env   *testkit.Env
	alice *testkit.Client
	conn  dto.Connector
	inst  *connectortest.Instance
	aiID  string
	ai    *testkit.AgentConn
	svc   *fakeService
	next  int
}

// newVoiceConnection starts an app whose transcription service is svc, or none when service is false.
func newVoiceConnection(t *testing.T, service bool, opts ...testkit.Option) *voiceConnection {
	t.Helper()
	svc := newFakeService(t)
	fake := connectortest.New()
	base := []testkit.Option{testkit.WithConnectorKinds(fake.Kind())}
	if service {
		base = append(base,
			testkit.WithEnv(map[string]string{"PARTYLINE_TRANSCRIBE_URL": svc.srv.URL}),
			testkit.WithTranscribeWait(func(context.Context, time.Duration) error { return nil }))
	}
	env := testkit.New(t, append(base, opts...)...)
	alice := env.NewUser("alice")
	on := true
	conn := testkit.Decode[dto.Connector](alice.Post("/api/v1/connectors", dto.CreateConnectorRequest{
		Kind: connectortest.KindName, Name: "wa", Config: map[string]any{"token": "fake-token-not-real"}, Enabled: &on,
	}).RequireStatus(http.StatusCreated))
	f := &voiceConnection{env: env, alice: alice, svc: svc, aiID: alice.NewAI("relay")}
	f.conn = alice.OperateConnector(conn.ID, f.aiID)
	f.inst = fake.Instance(t, conn.ID)
	f.inst.WaitRun(t)
	return f
}

// dial connects the AI as a client that receives platform events.
func (f *voiceConnection) dial(t *testing.T) {
	t.Helper()
	conn, resp := f.alice.Token(dto.TokenKindAgent, "laptop").DialPlatform("relay")
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	t.Cleanup(conn.Close)
	conn.Welcome(wait)
	f.ai = conn
}

// emit hands a message of a direct chat to the pipeline, as the platform would, and returns the id of
// the item it queued.
func (f *voiceConnection) emit(t *testing.T, m connectors.Inbound) string {
	t.Helper()
	f.next++
	m.ChatID, m.ChatTitle, m.ChatKind = "chat-1", "Pat", "dm"
	m.MessageID = fmt.Sprintf("M%d", f.next)
	m.SenderID, m.SenderName, m.At = "sender-1", "Pat", time.Now()
	before := len(f.queued(t))
	f.inst.Emit(context.Background(), m)
	var items []platform.Event
	require.Eventually(t, func() bool { items = f.queued(t); return len(items) > before }, wait, 10*time.Millisecond)
	return items[len(items)-1].ID
}

// voice emits a voice note without text.
func (f *voiceConnection) voice(t *testing.T, data []byte) string {
	t.Helper()
	return f.emit(t, connectors.Inbound{Kind: string(dto.MessageKindAudio), Media: []connectors.Media{{
		Name: "voice.ogg", Mime: "audio/ogg; codecs=opus",
		Open: func(context.Context) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(data)), nil },
	}}})
}

func (f *voiceConnection) text(t *testing.T, body string) string {
	t.Helper()
	return f.emit(t, connectors.Inbound{Text: body})
}

// queued is the AI's queue, unacknowledged items oldest first.
func (f *voiceConnection) queued(t *testing.T) []platform.Event {
	t.Helper()
	items, err := f.env.App.Platform.Unacked(context.Background(), f.aiID, 0, 100)
	require.NoError(t, err)
	return items
}

// rows reads the transcription rows of an item.
func (f *voiceConnection) rows(t *testing.T, itemID string) []db.ListPlatformTranscriptionsByItemsRow {
	t.Helper()
	rows, err := f.env.App.Store.R().ListPlatformTranscriptionsByItems(context.Background(), []string{itemID})
	require.NoError(t, err)
	return rows
}

// state waits until the item's one transcription row is in the state.
func (f *voiceConnection) state(t *testing.T, itemID string, state dto.TranscriptState) {
	t.Helper()
	require.Eventually(t, func() bool {
		rows := f.rows(t, itemID)
		return len(rows) == 1 && rows[0].State == string(state)
	}, wait, 10*time.Millisecond)
}

func (f *voiceConnection) started(t *testing.T) {
	t.Helper()
	select {
	case <-f.svc.started:
	case <-time.After(wait):
		t.Fatal("the service got no request")
	}
}

// TR-04: a voice note that arrives on a connection reaches the AI that operates it once, after the
// server transcribed it, with the transcript under the attachment line of its frame.
func TestAPlatformVoiceNoteReachesItsAIOnceWithTheTranscript(t *testing.T) {
	f := newVoiceConnection(t, true)
	f.dial(t)
	release := f.svc.block()

	id := f.voice(t, audio)
	f.started(t)
	f.ai.ExpectNone(300 * time.Millisecond)
	release("hello from the platform")

	got := f.ai.Platform(wait).Event
	require.Equal(t, id, got.ID)
	require.Len(t, got.Attachments, 1)
	require.Equal(t, dto.TranscriptStateDone, got.Attachments[0].TranscriptState)
	require.Equal(t, "hello from the platform", got.Attachments[0].Transcript)
	require.Empty(t, got.Text, "the transcript is no text of the item")
	content := channel.PlatformRecord(got)["content"]
	require.Equal(t, "[attachment "+got.Attachments[0].ID+": voice.ogg, audio/ogg; codecs=opus]\nTranscript: hello from the platform", content)
	f.ai.ExpectNone(400 * time.Millisecond)

	calls := f.svc.calls()
	require.Len(t, calls, 1)
	require.Equal(t, "voice.ogg", calls[0].filename)
	require.Equal(t, "audio/ogg; codecs=opus", calls[0].mime)
	require.Equal(t, audio, calls[0].audio)

	// CID-09: the transcript is platform text, and goes with the acknowledgement.
	f.ai.AckPlatform(id)
	require.Eventually(t, func() bool { return len(f.rows(t, id)) == 0 }, wait, 10*time.Millisecond)
}

// TR-04: the items before a pending voice note flow; those after it wait behind it, so the AI gets
// its platform items in order.
func TestALaterPlatformItemDoesNotOvertakeAPendingVoiceNote(t *testing.T) {
	f := newVoiceConnection(t, true)
	f.dial(t)
	release := f.svc.block()

	first := f.text(t, "before the voice note")
	require.Equal(t, first, f.ai.Platform(wait).Event.ID, "an item before the voice note flows")
	voice := f.voice(t, audio)
	after := f.text(t, "and one more thing")
	f.started(t)
	f.ai.ExpectNone(500 * time.Millisecond)

	release("the voice note")
	got := f.ai.Platform(wait).Event
	require.Equal(t, voice, got.ID)
	require.Equal(t, "the voice note", got.Attachments[0].Transcript)
	require.Equal(t, after, f.ai.Platform(wait).Event.ID)
	f.ai.ExpectNone(300 * time.Millisecond)
}

// TR-04: an AI that connects while its voice note waits gets it when the transcript is ready.
func TestAnAIThatConnectsLaterGetsThePlatformVoiceNoteWithTheTranscript(t *testing.T) {
	f := newVoiceConnection(t, true)
	release := f.svc.block()
	voice := f.voice(t, audio)
	after := f.text(t, "later")
	f.started(t)

	f.dial(t)
	f.ai.ExpectNone(300 * time.Millisecond)
	release("words")
	got := f.ai.Platform(wait).Event
	require.Equal(t, voice, got.ID)
	require.Equal(t, "words", got.Attachments[0].Transcript)
	require.Equal(t, after, f.ai.Platform(wait).Event.ID)
}

// TR-04: past the limit the AI gets the audio alone and the items after it follow; a transcript that
// arrives later sends nothing again, whether the item was acknowledged or is still in flight.
func TestASlowServiceSendsThePlatformVoiceNoteAloneAtTheLimit(t *testing.T) {
	f := newVoiceConnection(t, true, testkit.WithTranscriptHold(300*time.Millisecond))
	f.dial(t)
	release := f.svc.block()

	start := time.Now()
	first := f.voice(t, []byte("first audio"))
	after := f.text(t, "after it")
	second := f.voice(t, []byte("second audio"))
	got := f.ai.Platform(wait).Event
	require.GreaterOrEqual(t, time.Since(start), 300*time.Millisecond)
	require.Equal(t, first, got.ID)
	require.Equal(t, dto.TranscriptStatePending, got.Attachments[0].TranscriptState)
	require.Empty(t, got.Attachments[0].Transcript)
	require.NotContains(t, channel.PlatformRecord(got)["content"], "Transcript:")
	require.Equal(t, after, f.ai.Platform(wait).Event.ID)
	got = f.ai.Platform(wait).Event
	require.Equal(t, second, got.ID, "the second voice note goes at its own limit")
	require.Equal(t, dto.TranscriptStatePending, got.Attachments[0].TranscriptState)

	// The first is acknowledged, the second stays in flight.
	f.ai.AckPlatform(first)
	require.Eventually(t, func() bool { return len(f.rows(t, first)) == 0 }, wait, 10*time.Millisecond)
	release("late words")
	f.state(t, second, dto.TranscriptStateDone)
	f.ai.ExpectNone(500 * time.Millisecond)
}

// TR-04, TR-06: a service that keeps failing leaves the voice note as it was: the AI gets the audio
// alone, and nobody sees an error.
func TestAFailingServiceSendsThePlatformVoiceNoteAlone(t *testing.T) {
	f := newVoiceConnection(t, true)
	f.svc.fail(http.StatusInternalServerError)
	f.dial(t)

	id := f.voice(t, audio)
	got := f.ai.Platform(wait).Event
	require.Equal(t, id, got.ID)
	require.Equal(t, dto.TranscriptStateFailed, got.Attachments[0].TranscriptState)
	require.Empty(t, got.Attachments[0].Transcript)
	require.Equal(t, "[attachment "+got.Attachments[0].ID+": voice.ogg, audio/ogg; codecs=opus]", channel.PlatformRecord(got)["content"])
	require.Len(t, f.svc.calls(), transcribe.Attempts)
}

// TR-04: a voice note with text (a caption) is not transcribed and does not wait.
func TestAPlatformVoiceNoteWithTextIsNotTranscribed(t *testing.T) {
	f := newVoiceConnection(t, true)
	f.dial(t)

	id := f.emit(t, connectors.Inbound{Text: "listen to this", Kind: string(dto.MessageKindAudio), Media: []connectors.Media{{
		Name: "voice.ogg", Mime: "audio/ogg",
		Open: func(context.Context) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(audio)), nil },
	}}})
	got := f.ai.Platform(wait).Event
	require.Equal(t, id, got.ID)
	require.Empty(t, got.Attachments[0].TranscriptState)
	require.Empty(t, f.rows(t, id))
	require.Empty(t, f.svc.calls())
}

// TR-04, TR-08: platform audio over the cap is not sent; the AI gets it at once.
func TestOversizedPlatformAudioIsSkipped(t *testing.T) {
	f := newVoiceConnection(t, true, testkit.WithEnv(map[string]string{"PARTYLINE_MAX_UPLOAD_MB": "26"}))
	f.dial(t)

	id := f.voice(t, bytes.Repeat([]byte{1}, int(transcribe.MaxBytes)+1))
	got := f.ai.Platform(wait).Event
	require.Equal(t, id, got.ID)
	require.Equal(t, dto.TranscriptStateSkipped, got.Attachments[0].TranscriptState)
	time.Sleep(200 * time.Millisecond)
	require.Empty(t, f.svc.calls())
}

// TR-04, TR-06: without a service a platform voice note is as it was: no row, no wait.
func TestWithoutAServiceAPlatformVoiceNoteIsNotTranscribed(t *testing.T) {
	f := newVoiceConnection(t, false)
	f.dial(t)

	id := f.voice(t, audio)
	got := f.ai.Platform(wait).Event
	require.Equal(t, id, got.ID)
	require.Empty(t, got.Attachments[0].TranscriptState)
	require.Empty(t, f.rows(t, id))
	require.Empty(t, f.svc.calls())
}

// TR-04, TR-08: a platform job interrupted by a restart is finished by the next run, and the AI gets
// the voice note with its transcript.
func TestARestartResumesAPendingPlatformTranscription(t *testing.T) {
	f := newVoiceConnection(t, true)
	release := f.svc.block()

	id := f.voice(t, audio)
	f.started(t)
	f.env.Restart() // cancels the request in flight; the row stays pending
	rows := f.rows(t, id)
	require.Len(t, rows, 1)
	require.Equal(t, string(dto.TranscriptStatePending), rows[0].State)

	release("after the restart")
	f.state(t, id, dto.TranscriptStateDone)
	f.dial(t)
	got := f.ai.Platform(wait).Event
	require.Equal(t, id, got.ID)
	require.Equal(t, "after the restart", got.Attachments[0].Transcript)
}

// TR-06: platform rows a run with a service left pending fail when the server starts without one,
// and the voice note no longer waits.
func TestStartingWithoutAServiceFailsTheLeftoverPlatformRows(t *testing.T) {
	f := newVoiceConnection(t, false)
	id := f.voice(t, audio)
	items := f.queued(t)
	require.Len(t, items, 1)
	ctx := context.Background()
	now := time.Now().UnixMilli()
	require.NoError(t, f.env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.CreatePlatformTranscription(ctx, db.CreatePlatformTranscriptionParams{
			AttachmentID: items[0].Attachments[0].ID, ItemID: id, State: string(dto.TranscriptStatePending),
			CreatedAt: now, UpdatedAt: now,
		})
	}))

	f.env.Restart()
	f.state(t, id, dto.TranscriptStateFailed)
	f.dial(t)
	got := f.ai.Platform(wait).Event
	require.Equal(t, id, got.ID)
	require.Equal(t, dto.TranscriptStateFailed, got.Attachments[0].TranscriptState)
}

// TR-04, TR-08: room and platform voice files share one queue, oldest first, and the room voice
// message still reaches the AI with its transcript.
func TestRoomAndPlatformVoiceShareOneQueue(t *testing.T) {
	f := newVoiceConnection(t, true)
	f.dial(t)
	room := testkit.Decode[dto.Room](f.alice.Post("/api/v1/rooms", dto.CreateRoomRequest{Name: "Support"}).RequireStatus(http.StatusCreated))
	f.alice.Post("/api/v1/rooms/"+room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: f.aiID}).RequireStatus(http.StatusCreated)
	r := &voiceRoom{env: f.env, alice: f.alice, room: room, aiID: f.aiID, ai: f.ai, svc: f.svc}
	release := f.svc.block()

	posted := r.postVoice(t, room.ID, "", []byte("room audio"))
	f.started(t)
	item := f.voice(t, []byte("platform audio"))
	time.Sleep(200 * time.Millisecond)
	require.Len(t, f.svc.calls(), 1, "one job at a time")
	require.Equal(t, []byte("room audio"), f.svc.calls()[0].audio)

	release("words")
	var gotRoom, gotItem bool
	for range 2 {
		frame := f.ai.Next(wait)
		switch frame.Type {
		case dto.AgentFrameTypeMessage:
			var m dto.AgentMessageFrame
			require.NoError(t, json.Unmarshal(frame.Raw, &m))
			require.Equal(t, posted.ID, m.Message.ID)
			require.Equal(t, "words", m.Message.Attachments[0].Transcript)
			gotRoom = true
		case dto.AgentFrameTypePlatform:
			var p dto.AgentPlatformFrame
			require.NoError(t, json.Unmarshal(frame.Raw, &p))
			require.Equal(t, item, p.Event.ID)
			require.Equal(t, "words", p.Event.Attachments[0].Transcript)
			gotItem = true
		default:
			t.Fatalf("unexpected frame: %s", frame.Raw)
		}
	}
	require.True(t, gotRoom && gotItem)
	calls := f.svc.calls()
	require.Len(t, calls, 2)
	require.Equal(t, []byte("platform audio"), calls[1].audio)
	require.True(t, strings.HasSuffix(calls[1].filename, ".ogg"))
}

// TR-08: a job whose file goes away while its request runs (the item acknowledged) is not tried
// again after a transient failure: the worker moves on to the next job.
func TestAJobThatWentAwayIsNotTriedAgain(t *testing.T) {
	f := newVoiceConnection(t, true)
	release := f.svc.block()

	first := f.voice(t, []byte("first audio"))
	f.started(t)
	require.NoError(t, f.env.App.Platform.Ack(context.Background(), f.aiID, first))
	require.Empty(t, f.rows(t, first))
	second := f.voice(t, []byte("second audio"))
	f.svc.fail(http.StatusInternalServerError)
	release("")

	f.state(t, second, dto.TranscriptStateFailed)
	var firsts int
	for _, c := range f.svc.calls() {
		if bytes.Equal(c.audio, []byte("first audio")) {
			firsts++
		}
	}
	require.Equal(t, 1, firsts, "the audio of the acknowledged item was sent once")
	require.Len(t, f.svc.calls(), 1+transcribe.Attempts)
}
