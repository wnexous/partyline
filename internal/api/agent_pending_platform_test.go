package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/platform"
	"github.com/wnexous/partyline/internal/testkit"
)

// pendingAs calls GET /agent/pending as the AI of the folder name, with the agent token of the
// fixture's owner.
func (f *platformWS) pendingAs(name, query string) *testkit.Response {
	f.env.T.Helper()
	return f.token.AsAgent(name).Get("/api/v1/agent/pending" + query)
}

func (f *platformWS) pendingPlatform(t *testing.T, name, query string) dto.AgentPending {
	t.Helper()
	return testkit.Decode[dto.AgentPending](f.pendingAs(name, query).RequireStatus(http.StatusOK))
}

// keysOf returns the sorted top-level keys of a JSON object.
func keysOf(t *testing.T, body []byte) []string {
	t.Helper()
	var obj map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &obj))
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// CID-06, CID-08, CID-09: GET /agent/pending?platform=1 returns the AI's queued platform events,
// oldest first and complete, next to the room messages, and counts them as delivered: their text is
// deleted and they do not come again.
func TestPendingReturnsPlatformEventsAndAcknowledgesThem(t *testing.T) {
	f := newPlatformWS(t)
	sent := time.Date(2026, 10, 22, 12, 30, 0, 0, time.UTC)
	first := f.enqueue(t, f.atlas, f.group, "m1", "hello Atlas", func(it *platform.Item) {
		it.Trusted, it.Kind, it.ReceivedAt = true, dto.MessageKindImage, sent
		it.Attachments = []platform.Attachment{{Name: "photo.png", Mime: "image/png", Size: 3, Status: dto.AttachmentStatusStored}}
	})
	f.enqueue(t, f.notes, f.group, "n1", "for Notes")
	second := f.enqueue(t, f.atlas, f.dm, "m2", "typed on the phone", func(it *platform.Item) {
		it.SenderID, it.SenderName, it.FromSelf, it.ReceivedAt = "5511999990001", "Atlas phone", true, sent.Add(time.Minute)
	})

	res := f.pendingAs("Atlas", "?platform=1").RequireStatus(http.StatusOK)
	require.Equal(t, []string{"items", "platform"}, keysOf(t, res.Body))
	got := testkit.Decode[dto.AgentPending](res)
	require.Equal(t, []dto.AgentMessage{}, got.Items)
	require.Len(t, got.Platform, 2)
	a := got.Platform[0].Attachments
	require.Len(t, a, 1)
	require.NotEmpty(t, a[0].ID)
	require.True(t, sent.Equal(got.Platform[0].ReceivedAt))
	got.Platform[0].ReceivedAt = time.Time{}
	require.Equal(t, dto.PlatformEvent{
		ID: first, Platform: "fake", ConnectionID: f.conn.ID, ChatID: f.group.ID, ChatTitle: "Team", ChatKind: dto.ChatKindGroup,
		SenderName: "Pat", SenderID: "5511999990000", Trusted: true, Kind: dto.MessageKindImage, Text: "hello Atlas",
		Attachments: []dto.Attachment{{ID: a[0].ID, Name: "photo.png", Mime: "image/png", Size: 3, Status: dto.AttachmentStatusStored}},
	}, got.Platform[0])
	require.Equal(t, []any{second, "typed on the phone", true, false, dto.ChatKindDM},
		[]any{got.Platform[1].ID, got.Platform[1].Text, got.Platform[1].FromSelf, got.Platform[1].Trusted, got.Platform[1].ChatKind})

	// Delivered: the text is gone from the queue, the rest of the item stays, and nothing comes again.
	for id, sender := range map[string]string{first: "Pat", second: "Atlas phone"} {
		e := f.acked(t, f.atlas, id)
		require.Empty(t, e.Text)
		require.Equal(t, sender, e.SenderName)
	}
	again := f.pendingPlatform(t, "Atlas", "?platform=1")
	require.Equal(t, dto.AgentPending{Items: []dto.AgentMessage{}, Platform: []dto.PlatformEvent{}}, again)
	require.Equal(t, "for Notes", f.item(t, f.notes, f.itemIDOf(t, f.notes)).Text, "another AI's queue is untouched")
}

// itemIDOf returns the id of the only unacknowledged item of the AI.
func (f *platformWS) itemIDOf(t *testing.T, agent string) string {
	t.Helper()
	list, err := f.env.App.Platform.Unacked(t.Context(), agent, 0, 10)
	require.NoError(t, err)
	require.Len(t, list, 1)
	return list[0].ID
}

// Without platform=1 the response is what it always was: a list of room messages with no platform
// key, and the queue is neither returned nor acknowledged (also with platform=0); it is still
// there for a caller that asks.
func TestPendingWithoutThePlatformParameterIsUnchanged(t *testing.T) {
	f := newPlatformWS(t)
	id := f.enqueue(t, f.atlas, f.group, "m1", "still queued")

	for _, query := range []string{"", "?platform=0", "?wait=0"} {
		res := f.pendingAs("Atlas", query).RequireStatus(http.StatusOK)
		require.Equal(t, []string{"items"}, keysOf(t, res.Body), "pending%s", query)
		require.Empty(t, testkit.Decode[dto.List[dto.AgentMessage]](res).Items)
		e := f.item(t, f.atlas, id)
		require.True(t, e.AckedAt.IsZero(), "pending%s does not acknowledge", query)
		require.Equal(t, "still queued", e.Text)
	}

	got := f.pendingPlatform(t, "Atlas", "?platform=1")
	require.Len(t, got.Platform, 1)
	require.Equal(t, []string{id, "still queued"}, []string{got.Platform[0].ID, got.Platform[0].Text})
}

// platform= takes 1 or 0; anything else is refused and takes nothing.
func TestPendingRejectsABadPlatformValue(t *testing.T) {
	f := newPlatformWS(t)
	id := f.enqueue(t, f.atlas, f.group, "m1", "still queued")
	for _, q := range []string{"?platform=true", "?platform=2", "?platform=yes", "?platform=1&platform=x"} {
		e := f.pendingAs("Atlas", q).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
		require.Contains(t, e.Fields, "platform", q)
	}
	require.True(t, f.item(t, f.atlas, id).AckedAt.IsZero())
}

// The existing limit holds for the platform list: at most 20 per call, oldest first, and the rest
// comes with the next call.
func TestPendingPlatformLimit(t *testing.T) {
	f := newPlatformWS(t)
	ids := make([]string, 23)
	for i := range ids {
		ids[i] = f.enqueue(t, f.atlas, f.group, fmt.Sprintf("m%02d", i), fmt.Sprintf("message %d", i))
	}
	idsOf := func(p dto.AgentPending) []string {
		out := make([]string, len(p.Platform))
		for i, e := range p.Platform {
			out[i] = e.ID
		}
		return out
	}
	require.Equal(t, ids[:20], idsOf(f.pendingPlatform(t, "Atlas", "?platform=1")))
	require.Equal(t, ids[20:], idsOf(f.pendingPlatform(t, "Atlas", "?platform=1")))
	require.Empty(t, f.pendingPlatform(t, "Atlas", "?platform=1").Platform)
}

// The room messages of the response are the same with and without platform=1: the parameter only
// adds the platform list (each list has its own limit of 20).
func TestPendingPlatformAddsToTheRoomMessages(t *testing.T) {
	f := newPlatformWS(t)
	room := createRoom(f.alice, "Support")
	f.alice.Post("/api/v1/rooms/"+room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: f.atlas}).RequireStatus(http.StatusCreated)
	var posted []dto.Message
	for i := range 21 {
		posted = append(posted, testkit.Decode[dto.Message](f.alice.Post("/api/v1/rooms/"+room.ID+"/messages",
			dto.PostMessageRequest{Text: fmt.Sprintf("room %d", i)}).RequireStatus(http.StatusCreated)))
	}
	for i := range 21 {
		f.enqueue(t, f.atlas, f.group, fmt.Sprintf("m%02d", i), fmt.Sprintf("platform %d", i))
	}

	got := f.pendingPlatform(t, "Atlas", "?platform=1")
	require.Len(t, got.Items, 20, "the room messages keep their limit")
	require.Equal(t, posted[0].ID, got.Items[0].Message.ID)
	require.Len(t, got.Platform, 20, "so does the platform list")
	require.Equal(t, "platform 0", got.Platform[0].Text)
	last := f.pendingPlatform(t, "Atlas", "?platform=1")
	require.Equal(t, []string{posted[20].ID}, []string{last.Items[0].Message.ID})
	require.Len(t, last.Items, 1)
	require.Len(t, last.Platform, 1)
	require.Equal(t, "platform 20", last.Platform[0].Text)
}

// TURN-01: with platform=1, wait=N returns as soon as a platform event is queued, and an empty
// answer (both lists empty, not absent) after N seconds when nothing arrives.
func TestPendingPlatformWaitLongPolls(t *testing.T) {
	f := newPlatformWS(t)
	start := time.Now()
	res := f.pendingAs("Atlas", "?platform=1&wait=1").RequireStatus(http.StatusOK)
	require.GreaterOrEqual(t, time.Since(start), time.Second)
	require.JSONEq(t, `{"items":[],"platform":[]}`, string(res.Body))

	go func() {
		time.Sleep(300 * time.Millisecond)
		f.enqueue(t, f.atlas, f.group, "m1", "arrived while waiting")
	}()
	start = time.Now()
	got := f.pendingPlatform(t, "Atlas", "?platform=1&wait=30")
	require.Len(t, got.Platform, 1)
	require.Equal(t, "arrived while waiting", got.Platform[0].Text)
	require.Less(t, time.Since(start), 20*time.Second, "returned before the timeout")
}

// Without platform=1 a platform event does not end the wait and is not taken (L-018: only the
// parameter differs from the test above).
func TestPendingWaitIgnoresPlatformEventsWithoutTheParameter(t *testing.T) {
	f := newPlatformWS(t)
	var id string
	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(300 * time.Millisecond)
		id = f.enqueue(t, f.atlas, f.group, "m1", "arrived while waiting")
	}()
	start := time.Now()
	res := f.pendingAs("Atlas", "?wait=2").RequireStatus(http.StatusOK)
	require.GreaterOrEqual(t, time.Since(start), 2*time.Second, "nothing it asks for arrived")
	require.Equal(t, []string{"items"}, keysOf(t, res.Body))
	<-done
	require.Equal(t, "arrived while waiting", f.item(t, f.atlas, id).Text, "not taken")
}

// An AI that holds the WebSocket gets its events pushed: pending answers 409 and takes nothing,
// platform=1 included.
func TestPendingPlatformRefusesPushedSessions(t *testing.T) {
	f := newPlatformWS(t)
	id := f.enqueue(t, f.atlas, f.group, "m1", "queued")
	conn, resp := f.token.DialAgent("Atlas") // a client that does not take platform events: the item waits
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	conn.Welcome(wsWait)

	e := f.pendingAs("Atlas", "?platform=1").RequireError(http.StatusConflict, dto.ErrorCodeConflict)
	require.Equal(t, "messages are pushed to this agent's session; there is nothing to wait for", e.Message)
	require.True(t, f.item(t, f.atlas, id).AckedAt.IsZero())
}

// Authorization of GET /agent/pending?platform=1: agent tokens only, and an AI reads its own queue.
// The operator gets its events; another AI of the same owner, the AI of a user granted use of the
// operator and the AI of a stranger (each with the same folder name where it can be) get none of
// them and take nothing; users, with a session or a token, and anonymous callers are refused.
func TestPendingPlatformAuthorizationMatrix(t *testing.T) {
	f := newPlatformWS(t)
	bob, carol := f.env.NewUser("bob"), f.env.NewUser("carol")
	shareAgent(f.alice, f.atlas, bob.User.ID)
	id := f.enqueue(t, f.atlas, f.group, "m1", "for Atlas only")

	none := dto.AgentPending{Items: []dto.AgentMessage{}, Platform: []dto.PlatformEvent{}}
	require.Equal(t, none, f.pendingPlatform(t, "Notes", "?platform=1"), "another AI of the owner")
	for name, u := range map[string]*testkit.Client{"use grantee": bob, "stranger": carol} {
		got := testkit.Decode[dto.AgentPending](u.Token(dto.TokenKindAgent, "desk").AsAgent("Atlas").
			Get("/api/v1/agent/pending?platform=1").RequireStatus(http.StatusOK))
		require.Equal(t, none, got, "%s's own AI of the same folder", name)
	}
	f.alice.Get("/api/v1/agent/pending?platform=1").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	f.alice.Token(dto.TokenKindUser, "script").Get("/api/v1/agent/pending?platform=1").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	f.env.Anon().AsAgent("Atlas").Get("/api/v1/agent/pending?platform=1").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	require.True(t, f.item(t, f.atlas, id).AckedAt.IsZero(), "nobody but the operator took it")

	got := f.pendingPlatform(t, "Atlas", "?platform=1")
	require.Len(t, got.Platform, 1)
	require.Equal(t, id, got.Platform[0].ID)
}
