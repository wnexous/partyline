package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/audit"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/connectors/webhook"
	"github.com/wnexous/partyline/internal/testkit"
)

// received is an outbound webhook call.
type received struct {
	Header http.Header
	Body   []byte
}

// receiver is the configured outbound URL: it records calls and answers status.
type receiver struct {
	srv    *httptest.Server
	mu     sync.Mutex
	status int
	calls  chan received
}

func newReceiver(t *testing.T) *receiver {
	rc := &receiver{status: http.StatusOK, calls: make(chan received, 16)}
	rc.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rc.calls <- received{Header: r.Header.Clone(), Body: body}
		rc.mu.Lock()
		status := rc.status
		rc.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(rc.srv.Close)
	return rc
}

func (rc *receiver) next(t *testing.T) received {
	t.Helper()
	select {
	case c := <-rc.calls:
		return c
	case <-time.After(connectortest.Timeout):
		t.Fatal("no outbound webhook call")
		return received{}
	}
}

type hookEnv struct {
	env    *testkit.Env
	alice  *testkit.Client
	conn   dto.Connector
	secret string
	out    *receiver
}

// newHookEnv runs an app with the webhook kind and the outbox worker, and a running
// webhook connector of alice whose secret was generated. The outbound receiver listens on
// loopback, so private networks are allowed unless opts say otherwise.
func newHookEnv(t *testing.T, opts ...testkit.Option) *hookEnv {
	t.Helper()
	out := newReceiver(t)
	opts = append([]testkit.Option{
		testkit.WithConnectorKinds(webhook.Kind()), testkit.WithOutbox(),
		testkit.WithEnv(map[string]string{"PARTYLINE_ALLOW_PRIVATE_NETWORKS": "true"}),
	}, opts...)
	env := testkit.New(t, opts...)
	alice := env.NewUser("alice")
	conn := testkit.Decode[dto.Connector](alice.Post("/api/v1/connectors", dto.CreateConnectorRequest{
		Kind: webhook.KindName, Name: "ci", Config: map[string]any{"outbound_url": out.srv.URL},
	}).RequireStatus(http.StatusCreated))
	e := &hookEnv{env: env, alice: alice, conn: conn, secret: conn.GeneratedSecrets["secret"], out: out}
	e.waitConnected(t)
	return e
}

func (e *hookEnv) waitConnected(t *testing.T) {
	t.Helper()
	waitHookConnected(t, e.alice, e.conn.ID)
}

// waitHookConnected waits until the owner sees the connector connected.
func waitHookConnected(t *testing.T, owner *testkit.Client, connectorID string) {
	t.Helper()
	deadline := time.Now().Add(connectortest.Timeout)
	for testkit.Decode[dto.Connector](owner.Get("/api/v1/connectors/"+connectorID).RequireStatus(http.StatusOK)).Status != dto.ConnectorStatusConnected {
		if time.Now().After(deadline) {
			t.Fatal("the webhook connector never ran")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// post sends a hook request signed with secret at ts.
func (e *hookEnv) post(t *testing.T, secret string, ts time.Time, body []byte) *testkit.Response {
	t.Helper()
	return signedHook(e.env, e.conn.ID, secret, ts, body)
}

// signedHook posts body to the connector's hook, signed with secret at ts.
func signedHook(env *testkit.Env, connectorID, secret string, ts time.Time, body []byte) *testkit.Response {
	stamp := strconv.FormatInt(ts.Unix(), 10)
	return env.Anon().
		WithHeader(webhook.TimestampHeader, stamp).
		WithHeader(webhook.SignatureHeader, webhook.Sign(secret, stamp, body)).
		DoRaw(http.MethodPost, "/api/v1/hooks/"+connectorID, "application/json", bytes.NewReader(body))
}

func payload(t *testing.T, v map[string]any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return data
}

func (e *hookEnv) chats(t *testing.T) []dto.Chat {
	t.Helper()
	return testkit.Decode[dto.List[dto.Chat]](e.alice.Get("/api/v1/connectors/" + e.conn.ID + "/chats").RequireStatus(http.StatusOK)).Items
}

func (e *hookEnv) roomWith(t *testing.T, chatID string) dto.Room {
	t.Helper()
	room := testkit.Decode[dto.Room](e.alice.Post("/api/v1/rooms", dto.CreateRoomRequest{Name: "builds"}).RequireStatus(http.StatusCreated))
	e.alice.Post("/api/v1/rooms/"+room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindChat, MemberID: chatID}).RequireStatus(http.StatusCreated)
	return room
}

func (e *hookEnv) messages(t *testing.T, roomID string) []dto.Message {
	t.Helper()
	return testkit.Decode[dto.Page[dto.Message]](e.alice.Get("/api/v1/rooms/" + roomID + "/messages").RequireStatus(http.StatusOK)).Items
}

// The generated secret is shown once in the create response; reads mask it and report the
// inbound URL.
func TestWebhookSecretIsGeneratedAndShownOnce(t *testing.T) {
	e := newHookEnv(t)
	require.Regexp(t, regexp.MustCompile(`^[0-9a-f]{64}$`), e.secret)
	require.Equal(t, dto.SecretMask, e.conn.Config["secret"])
	require.True(t, strings.HasSuffix(e.conn.InboundURL, "/api/v1/hooks/"+e.conn.ID), e.conn.InboundURL)

	got := testkit.Decode[dto.Connector](e.alice.Get("/api/v1/connectors/" + e.conn.ID).RequireStatus(http.StatusOK))
	require.Empty(t, got.GeneratedSecrets)
	require.Equal(t, dto.SecretMask, got.Config["secret"])
	require.NotContains(t, string(e.alice.Get("/api/v1/connectors").RequireStatus(http.StatusOK).Body), e.secret)

	given := testkit.Decode[dto.Connector](e.alice.Post("/api/v1/connectors", dto.CreateConnectorRequest{
		Kind: webhook.KindName, Name: "own", Config: map[string]any{"secret": "my-own-secret-value"},
	}).RequireStatus(http.StatusCreated))
	require.Empty(t, given.GeneratedSecrets, "a secret the user chose is not echoed")
}

// HOOK-01: a valid signed payload is accepted with 202 and stored in the chat's room.
func TestWebhookValidPayloadIsStoredInRoutedRoom(t *testing.T) {
	e := newHookEnv(t)
	body := payload(t, map[string]any{"chat_id": "build-alerts", "chat_title": "Build alerts", "sender_id": "ci-bot", "sender_name": "CI", "text": "warming up", "message_id": "m-1"})
	require.Equal(t, `{"status":"accepted"}`, e.post(t, e.secret, time.Now(), body).RequireStatus(http.StatusAccepted).String())
	chats := e.chats(t)
	require.Len(t, chats, 1)
	require.Equal(t, "build-alerts", chats[0].ExternalID)
	require.Equal(t, "Build alerts", chats[0].Title)
	room := e.roomWith(t, chats[0].ID)

	body = payload(t, map[string]any{"chat_id": "build-alerts", "sender_id": "ci-bot", "sender_name": "CI", "text": "build 42 failed", "message_id": "m-2"})
	e.post(t, e.secret, time.Now(), body).RequireStatus(http.StatusAccepted)
	msgs := e.messages(t, room.ID)
	require.Len(t, msgs, 1)
	require.Equal(t, "build 42 failed", msgs[0].Text)
	require.Equal(t, dto.AuthorKindChat, msgs[0].AuthorKind)
	require.Equal(t, "CI", msgs[0].SenderName)
}

// HOOK-02: a bad signature or a timestamp older than 5 minutes is refused with 401 and
// stores nothing; the refusal is audited.
func TestWebhookRejectsBadSignatureAndOldTimestamp(t *testing.T) {
	e := newHookEnv(t)
	body := payload(t, map[string]any{"chat_id": "build-alerts", "sender_id": "ci-bot", "text": "x"})

	e.post(t, "wrong-secret", time.Now(), body).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	e.post(t, e.secret, time.Now().Add(-301*time.Second), body).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	e.post(t, e.secret, time.Now().Add(301*time.Second), body).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	e.env.Anon().DoRaw(http.MethodPost, "/api/v1/hooks/"+e.conn.ID, "application/json", bytes.NewReader(body)).
		RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	require.Empty(t, e.chats(t), "nothing stored, not even the chat")

	entries := testkit.Decode[dto.Page[dto.AuditEntry]](e.env.NewAdmin("auditor").Get("/api/v1/admin/audit").RequireStatus(http.StatusOK)).Items
	rejected := 0
	for _, a := range entries {
		if a.Action == audit.ActionWebhookRejected {
			require.Equal(t, e.conn.ID, a.TargetID)
			rejected++
		}
	}
	require.Equal(t, 4, rejected)
}

// Payload and transport errors: missing fields 422, over 64 KiB 413, unknown or stopped
// connectors 404.
func TestWebhookRequestErrors(t *testing.T) {
	e := newHookEnv(t)
	bad := e.post(t, e.secret, time.Now(), payload(t, map[string]any{"chat_id": "a"})).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Equal(t, map[string]string{"sender_id": "required", "text": "required"}, bad.Fields)

	huge := payload(t, map[string]any{"chat_id": "a", "sender_id": "b", "text": strings.Repeat("x", 64<<10)})
	e.post(t, e.secret, time.Now(), huge).RequireError(http.StatusRequestEntityTooLarge, dto.ErrorCodeTooLarge)

	e.env.Anon().DoRaw(http.MethodPost, "/api/v1/hooks/no-such-connector", "application/json", strings.NewReader("{}")).
		RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)

	off := false
	e.alice.Patch("/api/v1/connectors/"+e.conn.ID, dto.UpdateConnectorRequest{Enabled: &off}).RequireStatus(http.StatusOK)
	e.post(t, e.secret, time.Now(), payload(t, map[string]any{"chat_id": "a", "sender_id": "b", "text": "c"})).
		RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
}

// HOOK-03: room messages for a webhook chat are POSTed to the outbound URL, signed like
// inbound requests, with expiring attachment links; a non-2xx answer is a failed attempt.
func TestWebhookOutboundIsSignedWithAttachmentLinks(t *testing.T) {
	e := newHookEnv(t)
	e.post(t, e.secret, time.Now(), payload(t, map[string]any{"chat_id": "deploys", "sender_id": "ci", "text": "hi", "message_id": "in-1"})).
		RequireStatus(http.StatusAccepted)
	room := e.roomWith(t, e.chats(t)[0].ID)

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormField(dto.MultipartMessageField)
	require.NoError(t, err)
	require.NoError(t, json.NewEncoder(part).Encode(dto.PostMessageRequest{Text: "deploy now"}))
	file, err := w.CreateFormFile(dto.MultipartFileField, "plan.txt")
	require.NoError(t, err)
	_, _ = file.Write([]byte("synthetic plan"))
	require.NoError(t, w.Close())
	msg := testkit.Decode[dto.Message](e.alice.DoRaw(http.MethodPost, "/api/v1/rooms/"+room.ID+"/messages", w.FormDataContentType(), &body).RequireStatus(http.StatusCreated))

	call := e.out.next(t)
	ts := call.Header.Get(webhook.TimestampHeader)
	require.NotEmpty(t, ts)
	require.Equal(t, webhook.Sign(e.secret, ts, call.Body), call.Header.Get(webhook.SignatureHeader), "valid signature")
	var got struct {
		Event       string `json:"event"`
		ConnectorID string `json:"connector_id"`
		ChatID      string `json:"chat_id"`
		Message     struct {
			ID         string `json:"id"`
			RoomID     string `json:"room_id"`
			Text       string `json:"text"`
			AuthorName string `json:"author_name"`
		} `json:"message"`
		Attachments []struct {
			Name, Mime, URL string
			Size            int64
		} `json:"attachments"`
	}
	require.NoError(t, json.Unmarshal(call.Body, &got))
	require.Equal(t, "message", got.Event)
	require.Equal(t, e.conn.ID, got.ConnectorID)
	require.Equal(t, "deploys", got.ChatID)
	require.Equal(t, msg.ID, got.Message.ID)
	require.Equal(t, room.ID, got.Message.RoomID)
	require.Equal(t, "deploy now", got.Message.Text)
	require.Equal(t, msg.AuthorName, got.Message.AuthorName)
	require.NotEmpty(t, got.Message.AuthorName)
	require.Len(t, got.Attachments, 1)
	require.Equal(t, "plan.txt", got.Attachments[0].Name)
	require.Equal(t, int64(len("synthetic plan")), got.Attachments[0].Size)

	link := strings.TrimPrefix(got.Attachments[0].URL, e.env.App.Config.PublicURL.String())
	require.True(t, strings.HasPrefix(link, "/api/v1/hooks/files/"), got.Attachments[0].URL)
	require.Equal(t, "synthetic plan", string(e.env.Anon().Get(link).RequireStatus(http.StatusOK).Body), "the link works without a login")
	e.env.Anon().Get(link[:len(link)-2]+"AA").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
}

func TestWebhookOutboundFailureIsRetried(t *testing.T) {
	e := newHookEnv(t)
	e.out.mu.Lock()
	e.out.status = http.StatusInternalServerError
	e.out.mu.Unlock()
	e.post(t, e.secret, time.Now(), payload(t, map[string]any{"chat_id": "deploys", "sender_id": "ci", "text": "hi"})).
		RequireStatus(http.StatusAccepted)
	room := e.roomWith(t, e.chats(t)[0].ID)
	msg := testkit.Decode[dto.Message](e.alice.Post("/api/v1/rooms/"+room.ID+"/messages", dto.PostMessageRequest{Text: "ping"}).RequireStatus(http.StatusCreated))
	e.out.next(t)

	item := e.firstAttempt(t, room.ID, msg.ID)
	require.Equal(t, dto.OutboxStatusPending, item.Status, "queued for a retry")
	require.Equal(t, "outbound: status 500", item.LastError)
}

// firstAttempt waits until the message's single delivery made an attempt.
func (e *hookEnv) firstAttempt(t *testing.T, roomID, messageID string) dto.OutboxItem {
	t.Helper()
	deadline := time.Now().Add(connectortest.Timeout)
	for {
		for _, m := range e.messages(t, roomID) {
			if m.ID == messageID && len(m.Outbox) == 1 && m.Outbox[0].Attempts >= 1 {
				return m.Outbox[0]
			}
		}
		require.False(t, time.Now().After(deadline), "the failed attempt was never recorded")
		time.Sleep(50 * time.Millisecond)
	}
}

// CON-13: without PARTYLINE_ALLOW_PRIVATE_NETWORKS an outbound URL on a private address is
// never called; the attempt fails with a short reason and is retried.
func TestWebhookOutboundToPrivateAddressIsRefusedByDefault(t *testing.T) {
	e := newHookEnv(t, testkit.WithEnv(map[string]string{"PARTYLINE_ALLOW_PRIVATE_NETWORKS": "false"}))
	e.post(t, e.secret, time.Now(), payload(t, map[string]any{"chat_id": "deploys", "sender_id": "ci", "text": "hi"})).
		RequireStatus(http.StatusAccepted)
	room := e.roomWith(t, e.chats(t)[0].ID)
	msg := testkit.Decode[dto.Message](e.alice.Post("/api/v1/rooms/"+room.ID+"/messages", dto.PostMessageRequest{Text: "ping"}).RequireStatus(http.StatusCreated))

	item := e.firstAttempt(t, room.ID, msg.ID)
	require.Equal(t, dto.OutboxStatusPending, item.Status)
	require.Equal(t, "outbound: destination address not allowed", item.LastError)
	select {
	case <-e.out.calls:
		t.Fatal("the private outbound URL was called")
	default:
	}
}

func identitiesOf(c *testkit.Client) []dto.Identity {
	c.T.Helper()
	return testkit.Decode[dto.List[dto.Identity]](c.Get("/api/v1/identities").RequireStatus(http.StatusOK)).Items
}

func chatByExternalID(t *testing.T, c *testkit.Client, connectorID, external string) dto.Chat {
	t.Helper()
	for _, ch := range testkit.Decode[dto.List[dto.Chat]](c.Get("/api/v1/connectors/" + connectorID + "/chats").RequireStatus(http.StatusOK)).Items {
		if ch.ExternalID == external {
			return ch
		}
	}
	t.Fatalf("no chat %s", external)
	return dto.Chat{}
}

// AD-006, IDN-02, IDN-05 (validation probe S1): an identity linked through alice's webhook
// connector is trusted only on that connector. Mallory's own webhook connector, which can
// claim any sender id, neither resolves alice's identity nor links alice's pairing code.
func TestWebhookIdentityCannotBeForgedAcrossConnectors(t *testing.T) {
	e := newHookEnv(t)
	now := time.Now()
	code := testkit.Decode[dto.PairingCode](e.alice.Post("/api/v1/identities/pairing", nil).RequireStatus(http.StatusCreated))
	e.post(t, e.secret, now, payload(t, map[string]any{"chat_id": "alice-chat", "sender_id": "alice-id", "text": "!link " + code.Code})).
		RequireStatus(http.StatusAccepted)
	ids := identitiesOf(e.alice)
	require.Len(t, ids, 1)
	require.Equal(t, e.conn.ID, ids[0].ConnectorID)
	require.Equal(t, "alice-id", ids[0].ExternalID)

	mallory := e.env.NewUser("mallory")
	mconn := testkit.Decode[dto.Connector](mallory.Post("/api/v1/connectors", dto.CreateConnectorRequest{
		Kind: webhook.KindName, Name: "mine", Config: map[string]any{},
	}).RequireStatus(http.StatusCreated))
	msecret := mconn.GeneratedSecrets["secret"]
	waitHookConnected(t, mallory, mconn.ID)

	// IDN-05: a webhook connector links only its owner's codes; the refused code stays usable.
	code2 := testkit.Decode[dto.PairingCode](e.alice.Post("/api/v1/identities/pairing", nil).RequireStatus(http.StatusCreated))
	signedHook(e.env, mconn.ID, msecret, now, payload(t, map[string]any{"chat_id": "m-chat", "sender_id": "alice-id", "text": "!link " + code2.Code})).
		RequireStatus(http.StatusAccepted)
	require.Len(t, identitiesOf(e.alice), 1, "mallory's connector linked nothing")

	// Alice's agent is shared with mallory, who routes her chat to it.
	agentID := registerAgent(t, e.env, e.alice, "builder")
	shareAgent(e.alice, agentID, mallory.User.ID)
	agent, resp := e.alice.Token(dto.TokenKindAgent, "laptop").DialAgent("builder")
	require.NotNil(t, agent, "upgrade refused: %d", resp.Status)
	agent.Welcome(connectortest.Timeout)
	signedHook(e.env, mconn.ID, msecret, now, payload(t, map[string]any{"chat_id": "m-chat", "sender_id": "mallory", "text": "hi"})).
		RequireStatus(http.StatusAccepted)
	mchat := chatByExternalID(t, mallory, mconn.ID, "m-chat")
	mroom := testkit.Decode[dto.Room](mallory.Post("/api/v1/chats/"+mchat.ID+"/route", dto.RouteChatRequest{AgentID: agentID}).RequireStatus(http.StatusCreated))

	// The forgery: mallory's connector claims alice's sender id.
	signedHook(e.env, mconn.ID, msecret, now, payload(t, map[string]any{"chat_id": "m-chat", "sender_id": "alice-id", "text": "run the deploy", "message_id": "f-1"})).
		RequireStatus(http.StatusAccepted)
	got := agent.Message(connectortest.Timeout)
	require.Equal(t, "run the deploy", got.Message.Text)
	require.Empty(t, got.Message.AuthorUserID)
	require.False(t, got.Trusted, "a forged sender id is not alice")
	stored := testkit.Decode[dto.Page[dto.Message]](mallory.Get("/api/v1/rooms/" + mroom.ID + "/messages").RequireStatus(http.StatusOK)).Items
	require.Len(t, stored, 1)
	require.Empty(t, stored[0].AuthorUserID, "author_user_id stays empty")

	// Control: the same sender id through alice's own connector is alice.
	e.post(t, e.secret, now, payload(t, map[string]any{"chat_id": "alice-chat", "sender_id": "someone", "text": "hello"})).RequireStatus(http.StatusAccepted)
	achat := chatByExternalID(t, e.alice, e.conn.ID, "alice-chat")
	e.alice.Post("/api/v1/chats/"+achat.ID+"/route", dto.RouteChatRequest{AgentID: agentID}).RequireStatus(http.StatusCreated)
	e.post(t, e.secret, now, payload(t, map[string]any{"chat_id": "alice-chat", "sender_id": "alice-id", "text": "it is me", "message_id": "a-1"})).
		RequireStatus(http.StatusAccepted)
	got = agent.Message(connectortest.Timeout)
	require.Equal(t, "it is me", got.Message.Text)
	require.Equal(t, e.alice.User.ID, got.Message.AuthorUserID)
	require.True(t, got.Trusted)

	// The code mallory's connector refused was not consumed.
	e.post(t, e.secret, now, payload(t, map[string]any{"chat_id": "alice-chat", "sender_id": "alice-phone", "text": "/link " + code2.Code})).
		RequireStatus(http.StatusAccepted)
	require.Len(t, identitiesOf(e.alice), 2)
}
