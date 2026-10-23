package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/connectors/webhook"
	"github.com/wnexous/partyline/internal/identities"
	"github.com/wnexous/partyline/internal/platform"
	"github.com/wnexous/partyline/internal/store/db"
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
	// operator is the AI that operates the connection: alice's "hookbot".
	operator string
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
	operator := alice.NewAI("hookbot")
	alice.OperateConnector(conn.ID, operator) // a connection runs once an AI operates it
	e := &hookEnv{env: env, alice: alice, conn: conn, secret: conn.GeneratedSecrets["secret"], out: out, operator: operator}
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

// queued returns the unacknowledged queue items of the AI, oldest first.
func (e *hookEnv) queued(t *testing.T, ai string) []platform.Event {
	t.Helper()
	list, err := e.env.App.Platform.Unacked(context.Background(), ai, 0, 1000)
	require.NoError(t, err)
	return list
}

// deliverEverything turns mentions-only off on the connection: webhook chats are channels, which
// mentions-only (on for every new connection, CID-27) delivers only when the payload says it
// mentions the account.
func (e *hookEnv) deliverEverything(t *testing.T) {
	t.Helper()
	e.alice.Patch("/api/v1/connectors/"+e.conn.ID, dto.UpdateConnectorRequest{GroupsMentionsOnly: ptr(false)}).RequireStatus(http.StatusOK)
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

// CON-01, CON-03: the webhook kind flags its secret as generated (the form may leave it
// empty); a PATCH sending it empty keeps the stored secret, and null cannot clear it.
func TestWebhookGeneratedSecretIsKeptWhenSentEmpty(t *testing.T) {
	e := newHookEnv(t)
	kinds := testkit.Decode[dto.List[dto.ConnectorKind]](e.alice.Get("/api/v1/connector-kinds").RequireStatus(http.StatusOK))
	require.Len(t, kinds.Items, 1)
	require.Equal(t, []dto.ConnectorField{
		{Name: "secret", Label: "Signing secret", Type: dto.FieldTypeSecret, Required: true, Secret: true, Generated: true,
			Help: "Leave empty to generate one; it is shown once"},
		{Name: "outbound_url", Label: "Outbound URL", Type: dto.FieldTypeURL,
			Help: "Room messages for this connector's chats are POSTed here"},
	}, kinds.Items[0].Fields)
	raw := string(e.alice.Get("/api/v1/connector-kinds").RequireStatus(http.StatusOK).Body)
	require.Equal(t, 1, strings.Count(raw, `"generated"`), "omitted on fields that are not generated: %s", raw)

	got := testkit.Decode[dto.Connector](e.alice.Patch("/api/v1/connectors/"+e.conn.ID, dto.UpdateConnectorRequest{
		Config: map[string]any{"secret": ""},
	}).RequireStatus(http.StatusOK))
	require.Equal(t, dto.SecretMask, got.Config["secret"])
	require.Empty(t, got.GeneratedSecrets, "nothing is generated on update")
	require.Equal(t, map[string]string{"config.secret": "required"}, e.alice.Patch("/api/v1/connectors/"+e.conn.ID, dto.UpdateConnectorRequest{
		Config: map[string]any{"secret": nil},
	}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid).Fields)

	// The PATCH restarted the connector; until the new instance runs the hook answers 404.
	deadline := time.Now().Add(connectortest.Timeout)
	for i := 0; ; i++ {
		body := payload(t, map[string]any{"chat_id": "build-alerts", "sender_id": "ci-bot", "text": "still signed " + strconv.Itoa(i)})
		r := e.post(t, e.secret, time.Now(), body)
		if r.Status != http.StatusNotFound || time.Now().After(deadline) {
			r.RequireStatus(http.StatusAccepted)
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// HOOK-01, CID-06: a valid signed payload is accepted with 202, its chat is recorded and, once the
// connection delivers it, the message is queued for the operator AI as a platform event of the
// webhook; nothing is stored in a room.
func TestWebhookValidPayloadIsQueuedForTheOperator(t *testing.T) {
	e := newHookEnv(t)
	e.deliverEverything(t)
	body := payload(t, map[string]any{"chat_id": "build-alerts", "chat_title": "Build alerts", "sender_id": "ci-bot", "sender_name": "CI", "text": "build 42 failed", "message_id": "m-1"})
	require.Equal(t, `{"status":"accepted"}`, e.post(t, e.secret, time.Now(), body).RequireStatus(http.StatusAccepted).String())
	chats := e.chats(t)
	require.Len(t, chats, 1)
	require.Equal(t, "build-alerts", chats[0].ExternalID)
	require.Equal(t, "Build alerts", chats[0].Title)

	got := e.queued(t, e.operator)
	require.Len(t, got, 1)
	require.Equal(t, []any{webhook.KindName, e.conn.ID, chats[0].ID, "Build alerts", dto.ChatKindChannel},
		[]any{got[0].Platform, got[0].ConnectionID, got[0].ChatID, got[0].ChatTitle, got[0].ChatKind})
	require.Equal(t, []any{"CI", "ci-bot", "build 42 failed", "m-1", false, false},
		[]any{got[0].SenderName, got[0].SenderID, got[0].Text, got[0].ExternalID, got[0].Trusted, got[0].FromSelf})
}

// CID-12, CID-27: a new connection delivers only the group messages that mention the account, and
// the chats of a webhook are channels: a payload that does not say it mentions the account is
// accepted and recorded but not delivered.
func TestWebhookPayloadWithoutAMentionIsNotDeliveredByDefault(t *testing.T) {
	e := newHookEnv(t)
	e.post(t, e.secret, time.Now(), payload(t, map[string]any{"chat_id": "build-alerts", "sender_id": "ci-bot", "text": "quiet", "message_id": "m-1"})).
		RequireStatus(http.StatusAccepted)
	require.Len(t, e.chats(t), 1, "the chat is recorded")
	require.Empty(t, e.queued(t, e.operator), "and nothing is delivered")

	e.deliverEverything(t)
	e.post(t, e.secret, time.Now(), payload(t, map[string]any{"chat_id": "build-alerts", "sender_id": "ci-bot", "text": "loud", "message_id": "m-2"})).
		RequireStatus(http.StatusAccepted)
	require.Equal(t, []string{"loud"}, func() []string {
		var out []string
		for _, it := range e.queued(t, e.operator) {
			out = append(out, it.Text)
		}
		return out
	}())
}

// CID-12: under the default mentions-only, only the payload that says it mentions the account is
// delivered to the operator; one that says it does not is accepted and recorded, and a
// non-boolean mentions_account is a 422 on that field with nothing stored. The new field does not
// change the route's authorization: a forged signature is still refused.
func TestWebhookMentionsAccountDecidesDelivery(t *testing.T) {
	e := newHookEnv(t)
	e.post(t, e.secret, time.Now(), payload(t, map[string]any{
		"chat_id": "build-alerts", "sender_id": "ci-bot", "text": "please look", "message_id": "m-1", "mentions_account": true,
	})).RequireStatus(http.StatusAccepted)
	queued := e.queued(t, e.operator)
	require.Len(t, queued, 1)
	require.Equal(t, "please look", queued[0].Text)

	e.post(t, e.secret, time.Now(), payload(t, map[string]any{
		"chat_id": "build-alerts", "sender_id": "ci-bot", "text": "quiet", "message_id": "m-2", "mentions_account": false,
	})).RequireStatus(http.StatusAccepted)
	require.Len(t, e.queued(t, e.operator), 1, "a payload that does not mention the account is not delivered")

	e.post(t, "wrong-secret", time.Now(), payload(t, map[string]any{
		"chat_id": "other-alerts", "sender_id": "ci-bot", "text": "forged", "message_id": "m-3", "mentions_account": true,
	})).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)

	invalid := e.post(t, e.secret, time.Now(), payload(t, map[string]any{
		"chat_id": "other-alerts", "sender_id": "ci-bot", "text": "bad", "message_id": "m-4", "mentions_account": "yes",
	})).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Equal(t, map[string]string{"mentions_account": "must be a boolean"}, invalid.Fields)
	require.Len(t, e.queued(t, e.operator), 1, "nothing more is delivered")
	require.Len(t, e.chats(t), 1, "the refused payload stores nothing, not even its chat")
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

// HOOK-04: a flood of bad-signature requests does not use the connector's budget, so a
// valid request still gets 202; beyond 30 per minute an IP's invalid requests get 429 and
// are no longer audited. (The 60/min budget of valid requests is checked with a fake clock
// in the webhook package.)
func TestWebhookInvalidRequestsDoNotBlockTheSender(t *testing.T) {
	e := newHookEnv(t)
	now := time.Now()
	body := payload(t, map[string]any{"chat_id": "build-alerts", "sender_id": "ci-bot", "text": "x"})
	for i := range 100 {
		resp := e.post(t, "wrong-secret", now, body)
		if i < webhook.InvalidRateLimit {
			resp.RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
		} else {
			resp.RequireError(http.StatusTooManyRequests, dto.ErrorCodeRateLimited)
		}
	}
	e.post(t, e.secret, now, payload(t, map[string]any{"chat_id": "build-alerts", "sender_id": "ci-bot", "text": "real", "message_id": "r-1"})).
		RequireStatus(http.StatusAccepted)
	require.Len(t, e.chats(t), 1, "the valid request was handled")

	rejected := 0
	for _, a := range testkit.Decode[dto.Page[dto.AuditEntry]](e.env.NewAdmin("auditor").Get("/api/v1/admin/audit").RequireStatus(http.StatusOK)).Items {
		if a.Action == audit.ActionWebhookRejected {
			rejected++
		}
	}
	require.Equal(t, webhook.InvalidRateLimit, rejected)
}

// HOOK-05: replaying a signed request answers 409 and queues nothing more, even without a
// message_id to dedupe on.
func TestWebhookReplayIsRefused(t *testing.T) {
	e := newHookEnv(t)
	e.deliverEverything(t)
	now := time.Now()

	body := payload(t, map[string]any{"chat_id": "build-alerts", "sender_id": "ci-bot", "text": "deploy approved"})
	e.post(t, e.secret, now, body).RequireStatus(http.StatusAccepted)
	e.post(t, e.secret, now, body).RequireError(http.StatusConflict, dto.ErrorCodeConflict)
	got := e.queued(t, e.operator)
	require.Len(t, got, 1)
	require.Equal(t, "deploy approved", got[0].Text)
}

// HOOK-05: accepted signatures outlive the connector instance: a replay after the connector
// is disabled and enabled again, or after a server restart, still answers 409.
func TestWebhookReplayIsRefusedAfterRestarts(t *testing.T) {
	e := newHookEnv(t)
	e.deliverEverything(t)
	now := time.Now()
	body := payload(t, map[string]any{"chat_id": "build-alerts", "sender_id": "ci-bot", "text": "deploy approved"})
	e.post(t, e.secret, now, body).RequireStatus(http.StatusAccepted)

	off, on := false, true
	e.alice.Patch("/api/v1/connectors/"+e.conn.ID, dto.UpdateConnectorRequest{Enabled: &off}).RequireStatus(http.StatusOK)
	e.alice.Patch("/api/v1/connectors/"+e.conn.ID, dto.UpdateConnectorRequest{Enabled: &on}).RequireStatus(http.StatusOK)
	e.waitConnected(t)
	e.post(t, e.secret, now, body).RequireError(http.StatusConflict, dto.ErrorCodeConflict)

	e.env.Restart()
	e.waitConnected(t)
	e.post(t, e.secret, now, body).RequireError(http.StatusConflict, dto.ErrorCodeConflict)

	got := e.queued(t, e.operator)
	require.Len(t, got, 1)
	require.Equal(t, "deploy approved", got[0].Text)
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

// aiSends sends text to the chat through the connection as the AI that operates it, with the
// agent API's connection route (CID-16).
func (e *hookEnv) aiSends(t *testing.T, chatID, text string) *testkit.Response {
	t.Helper()
	ai := e.alice.Token(dto.TokenKindAgent, "laptop").AsAgent("hookbot")
	return ai.Post("/api/v1/agent/connections/"+e.conn.ID+"/chats/"+chatID+"/messages", dto.SendToChatRequest{Text: text})
}

// sendRow returns the stored state of the send with the id.
func (e *hookEnv) sendRow(t *testing.T, id string) db.PlatformSend {
	t.Helper()
	row, err := e.env.App.Store.R().GetPlatformSend(context.Background(), id)
	require.NoError(t, err)
	return row
}

// hookChat records the webhook chat "deploys" by delivering a message to the connection and
// returns the chat's id.
func (e *hookEnv) hookChat(t *testing.T) string {
	t.Helper()
	e.post(t, e.secret, time.Now(), payload(t, map[string]any{"chat_id": "deploys", "sender_id": "ci", "text": "hi"})).
		RequireStatus(http.StatusAccepted)
	return e.chats(t)[0].ID
}

// HOOK-03, CID-16: what the operator AI sends to a webhook chat is POSTed to the outbound URL, signed
// like inbound requests; the payload names the chat and the connection, carries the text and the AI's
// name as data, and belongs to no room.
func TestWebhookOutboundIsSigned(t *testing.T) {
	e := newHookEnv(t)
	chat := e.hookChat(t)

	res := testkit.Decode[dto.PlatformSendResult](e.aiSends(t, chat, "deploy now").RequireStatus(http.StatusCreated))
	require.Equal(t, dto.PlatformSendStatusSent, res.Status)

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
		Attachments []any `json:"attachments"`
	}
	require.NoError(t, json.Unmarshal(call.Body, &got))
	require.Equal(t, "message", got.Event)
	require.Equal(t, e.conn.ID, got.ConnectorID)
	require.Equal(t, "deploys", got.ChatID)
	require.Equal(t, res.ID, got.Message.ID)
	require.Empty(t, got.Message.RoomID, "a send through a connection belongs to no room")
	require.Equal(t, "deploy now", got.Message.Text)
	require.Equal(t, "hookbot", got.Message.AuthorName)
	require.Empty(t, got.Attachments)
}

// HOOK-03: the expiring link the webhook connector puts in the outbound payload for a file serves the
// file without a login and only while its signature is intact. (Rooms are the only place files
// come from; a send through a connection carries none.)
func TestWebhookFileLinkServesTheAttachmentWithoutALogin(t *testing.T) {
	e := newHookEnv(t)
	room := testkit.Decode[dto.Room](e.alice.Post("/api/v1/rooms", dto.CreateRoomRequest{Name: "builds"}).RequireStatus(http.StatusCreated))
	msg := testkit.Decode[dto.Message](postMultipart(e.alice, room.ID, dto.PostMessageRequest{Text: "the plan"}, upload{"plan.txt", "text/plain", "synthetic plan"}).RequireStatus(http.StatusCreated))
	require.Len(t, msg.Attachments, 1)

	links := connectors.NewFileLinks(e.env.App.Box, e.env.App.Store, e.env.App.Config.PublicURL, nil)
	link := strings.TrimPrefix(links.URL(msg.Attachments[0].ID), e.env.App.Config.PublicURL.String())
	require.True(t, strings.HasPrefix(link, "/api/v1/hooks/files/"), link)
	require.Equal(t, "synthetic plan", string(e.env.Anon().Get(link).RequireStatus(http.StatusOK).Body), "the link works without a login")
	e.env.Anon().Get(link[:len(link)-2]+"AA").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
}

// HOOK-03, CID-18: a non-2xx answer is a failed attempt. The AI is told so right away, with the
// reason, and the send waits for its retry.
func TestWebhookOutboundFailureIsRetried(t *testing.T) {
	e := newHookEnv(t)
	e.out.mu.Lock()
	e.out.status = http.StatusInternalServerError
	e.out.mu.Unlock()
	chat := e.hookChat(t)

	res := testkit.Decode[dto.PlatformSendResult](e.aiSends(t, chat, "ping").RequireStatus(http.StatusAccepted))
	e.out.next(t)
	require.Equal(t, dto.PlatformSendStatusPending, res.Status, "queued for a retry")
	require.Equal(t, "outbound: status 500", res.Error)
	row := e.sendRow(t, res.ID)
	require.Equal(t, []any{"pending", int64(1), "outbound: status 500"}, []any{row.Status, row.Attempts, row.LastError})
}

// CON-13: without PARTYLINE_ALLOW_PRIVATE_NETWORKS an outbound URL on a private address is
// never called; the attempt fails with a short reason and is retried.
func TestWebhookOutboundToPrivateAddressIsRefusedByDefault(t *testing.T) {
	e := newHookEnv(t, testkit.WithEnv(map[string]string{"PARTYLINE_ALLOW_PRIVATE_NETWORKS": "false"}))
	chat := e.hookChat(t)

	res := testkit.Decode[dto.PlatformSendResult](e.aiSends(t, chat, "ping").RequireStatus(http.StatusAccepted))
	require.Equal(t, dto.PlatformSendStatusPending, res.Status)
	require.Equal(t, "outbound: destination address not allowed", res.Error)
	select {
	case <-e.out.calls:
		t.Fatal("the private outbound URL was called")
	default:
	}
}

// S6, CON-13: PARTYLINE_PRIVATE_NETWORK_ALLOWLIST lets a listed private destination through
// while PARTYLINE_ALLOW_PRIVATE_NETWORKS stays false; an unlisted one is still refused.
func TestWebhookOutboundToAllowlistedPrivateAddress(t *testing.T) {
	listed := newReceiver(t)
	unlisted := newReceiver(t)
	e := newHookEnv(t, testkit.WithEnv(map[string]string{
		"PARTYLINE_ALLOW_PRIVATE_NETWORKS":    "false",
		"PARTYLINE_PRIVATE_NETWORK_ALLOWLIST": strings.TrimPrefix(listed.srv.URL, "http://"),
	}))
	chat := e.hookChat(t)

	e.alice.Patch("/api/v1/connectors/"+e.conn.ID, dto.UpdateConnectorRequest{Config: map[string]any{"outbound_url": listed.srv.URL}}).
		RequireStatus(http.StatusOK)
	e.waitConnected(t)
	e.aiSends(t, chat, "to the listed receiver").RequireStatus(http.StatusCreated)
	var got map[string]any
	require.NoError(t, json.Unmarshal(listed.next(t).Body, &got))
	require.Equal(t, "to the listed receiver", got["message"].(map[string]any)["text"])

	e.alice.Patch("/api/v1/connectors/"+e.conn.ID, dto.UpdateConnectorRequest{Config: map[string]any{"outbound_url": unlisted.srv.URL}}).
		RequireStatus(http.StatusOK)
	e.waitConnected(t)
	res := testkit.Decode[dto.PlatformSendResult](e.aiSends(t, chat, "to the unlisted one").RequireStatus(http.StatusAccepted))
	require.Equal(t, "outbound: destination address not allowed", res.Error)
	select {
	case <-unlisted.calls:
		t.Fatal("the unlisted private URL was called")
	default:
	}
}

func identitiesOf(c *testkit.Client) []dto.Identity {
	c.T.Helper()
	return testkit.Decode[dto.List[dto.Identity]](c.Get("/api/v1/identities").RequireStatus(http.StatusOK)).Items
}

// AD-006, IDN-02, IDN-05 (validation probe S1): an identity linked through alice's webhook
// connector is trusted only on that connector. Mallory's own webhook connector, which can
// claim any sender id, neither resolves alice's identity nor links alice's pairing code.
func TestWebhookIdentityCannotBeForgedAcrossConnectors(t *testing.T) {
	e := newHookEnv(t)
	now := time.Now()
	code := testkit.Decode[dto.PairingCode](e.alice.Post("/api/v1/identities/pairing", dto.PairingRequest{ConnectorID: e.conn.ID}).RequireStatus(http.StatusCreated))
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
	malloryAI := mallory.NewAI("hookbot")
	mallory.OperateConnector(mconn.ID, malloryAI)
	waitHookConnected(t, mallory, mconn.ID)

	// IDN-05: a webhook connector links only its owner's codes, even a code issued for it (mallory
	// shared it with alice); the refused code stays usable.
	mallory.Put("/api/v1/connectors/"+mconn.ID+"/sharing", dto.Sharing{
		Mode: dto.VisibilityShared, Users: []dto.SharingUser{{UserID: e.alice.User.ID, Level: dto.LevelUse}},
	}).RequireStatus(http.StatusOK)
	code2 := testkit.Decode[dto.PairingCode](e.alice.Post("/api/v1/identities/pairing", dto.PairingRequest{ConnectorID: mconn.ID}).RequireStatus(http.StatusCreated))
	signedHook(e.env, mconn.ID, msecret, now, payload(t, map[string]any{"chat_id": "m-chat", "sender_id": "alice-id", "text": "!link " + code2.Code})).
		RequireStatus(http.StatusAccepted)
	require.Len(t, identitiesOf(e.alice), 1, "mallory's connector linked nothing")

	// The forgery: mallory's connector claims alice's sender id. Mallory's AI receives the message
	// and does not trust it: identities belong to the connector they were linked through.
	mallory.Patch("/api/v1/connectors/"+mconn.ID, dto.UpdateConnectorRequest{GroupsMentionsOnly: ptr(false)}).RequireStatus(http.StatusOK)
	signedHook(e.env, mconn.ID, msecret, now, payload(t, map[string]any{"chat_id": "m-chat", "sender_id": "alice-id", "text": "run the deploy", "message_id": "f-1"})).
		RequireStatus(http.StatusAccepted)
	forged := e.queued(t, malloryAI)
	require.Len(t, forged, 1)
	require.Equal(t, "run the deploy", forged[0].Text)
	require.False(t, forged[0].Trusted, "a forged sender id is not alice")

	// Control: the same sender id through alice's own connector is alice, for alice's AI.
	e.deliverEverything(t)
	e.post(t, e.secret, now, payload(t, map[string]any{"chat_id": "alice-chat", "sender_id": "alice-id", "text": "it is me", "message_id": "a-1"})).
		RequireStatus(http.StatusAccepted)
	mine := e.queued(t, e.operator)
	require.Len(t, mine, 1)
	require.Equal(t, "it is me", mine[0].Text)
	require.True(t, mine[0].Trusted)

	// The code mallory's connector refused was not consumed: it still links on its connector
	// when no owner check applies.
	_, ok, err := e.env.App.Identities.Link(context.Background(), code2.Code, identities.Account{
		ConnectorID: mconn.ID, ConnectorKind: webhook.KindName, SenderID: "alice-phone",
	}, "")
	require.NoError(t, err)
	require.True(t, ok)
	require.Len(t, identitiesOf(e.alice), 2)
}
