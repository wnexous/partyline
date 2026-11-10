package api_test

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

// offsetClock is the wall clock moved forward by a test.
type offsetClock struct{ offset atomic.Int64 }

func (c *offsetClock) now() time.Time          { return time.Now().Add(time.Duration(c.offset.Load())) }
func (c *offsetClock) advance(d time.Duration) { c.offset.Add(int64(d)) }

func days(n int) *int { return &n }

// RET-02: only an instance admin reads or changes the instance settings.
func TestAdminSettingsRequireAdmin(t *testing.T) {
	env := testkit.New(t)
	env.SetupAdmin()
	bob := env.NewUser("bob")
	anon := env.Anon()
	for _, method := range []string{http.MethodGet, http.MethodPatch} {
		bob.Do(method, "/api/v1/admin/settings", map[string]any{"attachment_retention_days": 1}).
			RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
		anon.Do(method, "/api/v1/admin/settings", map[string]any{"attachment_retention_days": 1}).
			RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	}
}

// RET-01, RET-02: the variable gives the default, an admin's value wins until the admin resets it, a
// value out of bounds or both fields at once are 422, and each change is in the audit log.
func TestAdminSettingsUpdate(t *testing.T) {
	env := testkit.New(t, testkit.WithEnv(map[string]string{"PARTYLINE_ATTACHMENT_RETENTION_DAYS": "90"}))
	admin := env.SetupAdmin()

	got := testkit.Decode[dto.InstanceSettings](admin.Get("/api/v1/admin/settings").RequireStatus(http.StatusOK))
	require.Equal(t, dto.InstanceSettings{AttachmentRetentionDays: 90, AttachmentRetentionDefaultDays: 90}, got)

	got = testkit.Decode[dto.InstanceSettings](admin.Patch("/api/v1/admin/settings",
		dto.UpdateInstanceSettingsRequest{AttachmentRetentionDays: days(0)}).RequireStatus(http.StatusOK))
	require.Equal(t, dto.InstanceSettings{AttachmentRetentionDays: 0, AttachmentRetentionDefaultDays: 90, AttachmentRetentionOverridden: true}, got)
	require.Equal(t, got, testkit.Decode[dto.InstanceSettings](admin.Get("/api/v1/admin/settings").RequireStatus(http.StatusOK)))

	// An empty body changes nothing.
	require.Equal(t, got, testkit.Decode[dto.InstanceSettings](admin.Patch("/api/v1/admin/settings", map[string]any{}).RequireStatus(http.StatusOK)))

	for _, n := range []int{-1, 36501} {
		e := admin.Patch("/api/v1/admin/settings", dto.UpdateInstanceSettingsRequest{AttachmentRetentionDays: days(n)}).
			RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
		require.Contains(t, e.Fields, "attachment_retention_days")
	}
	admin.Patch("/api/v1/admin/settings", dto.UpdateInstanceSettingsRequest{AttachmentRetentionDays: days(3), ResetAttachmentRetention: true}).
		RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)

	got = testkit.Decode[dto.InstanceSettings](admin.Patch("/api/v1/admin/settings",
		dto.UpdateInstanceSettingsRequest{ResetAttachmentRetention: true}).RequireStatus(http.StatusOK))
	require.Equal(t, dto.InstanceSettings{AttachmentRetentionDays: 90, AttachmentRetentionDefaultDays: 90}, got)

	audit := testkit.Decode[dto.Page[dto.AuditEntry]](admin.Get("/api/v1/admin/audit").RequireStatus(http.StatusOK))
	var changes int
	for _, e := range audit.Items {
		if e.Action == "instance.settings_update" {
			changes++
			require.Equal(t, "instance", e.TargetKind)
			require.Equal(t, "attachment_retention_days", e.TargetID)
		}
	}
	require.Equal(t, 2, changes, "the set and the reset; refused requests change nothing")
}

// RET-03, RET-06, RET-07, RET-08: once the retention passed, the attachment reads back expired in the
// timeline, the files list, the AI's history and its frames; its download answers 410 to the user and
// to the AI, and a caller who cannot view the room still gets 404.
func TestExpiredAttachmentDownloadIsGone(t *testing.T) {
	clock := &offsetClock{}
	env := testkit.New(t, testkit.WithClock(clock.now))
	admin := env.SetupAdmin()
	f := newAgentRoomIn(t, env)
	m := testkit.Decode[dto.Message](postMultipart(f.alice, f.room.ID, dto.PostMessageRequest{Text: "the report"},
		upload{"report.txt", "text/plain", "all good"}).RequireStatus(http.StatusCreated))
	id := m.Attachments[0].ID
	f.alice.Get("/api/v1/attachments/" + id).RequireStatus(http.StatusOK)

	admin.Patch("/api/v1/admin/settings", dto.UpdateInstanceSettingsRequest{AttachmentRetentionDays: days(30)}).RequireStatus(http.StatusOK)
	// The retention pass runs 31 days later; the clock goes back afterwards, so the sessions stay valid.
	clock.advance(31 * 24 * time.Hour)
	_, err := env.App.Rooms.Cleanup(context.Background())
	require.NoError(t, err)
	clock.advance(-31 * 24 * time.Hour)

	e := f.alice.Get("/api/v1/attachments/"+id).RequireError(http.StatusGone, dto.ErrorCodeAttachmentExpired)
	require.Contains(t, e.Message, "expired")
	f.rest().Get("/api/v1/agent/attachments/"+id).RequireError(http.StatusGone, dto.ErrorCodeAttachmentExpired)
	env.NewUser("mallory").Get("/api/v1/attachments/"+id).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)

	page := testkit.Decode[dto.Page[dto.Message]](f.alice.Get("/api/v1/rooms/" + f.room.ID + "/messages").RequireStatus(http.StatusOK))
	timeline := messageByID(t, page.Items, m.ID)
	require.Equal(t, dto.AttachmentStatusExpired, timeline.Attachments[0].Status)
	require.Equal(t, "report.txt", timeline.Attachments[0].Name)
	files := roomFiles(f.alice, f.room.ID, "")
	require.Len(t, files.Items, 1)
	require.Equal(t, dto.AttachmentStatusExpired, files.Items[0].Status)

	history := testkit.Decode[dto.Page[dto.AgentMessage]](f.rest().Get(agentRoomPath(f.room.ID, "/messages")).RequireStatus(http.StatusOK))
	var fromHistory []dto.Message
	for _, it := range history.Items {
		fromHistory = append(fromHistory, it.Message)
	}
	require.Equal(t, dto.AttachmentStatusExpired, messageByID(t, fromHistory, m.ID).Attachments[0].Status)

	conn := f.dial(t)
	conn.Welcome(wsWait)
	frame := conn.Message(wsWait)
	require.Equal(t, m.ID, frame.Message.ID)
	require.Equal(t, dto.AttachmentStatusExpired, frame.Message.Attachments[0].Status)
}

func messageByID(t *testing.T, list []dto.Message, id string) dto.Message {
	t.Helper()
	for _, m := range list {
		if m.ID == id {
			return m
		}
	}
	require.Failf(t, "message not found", "id %s", id)
	return dto.Message{}
}

// newAgentRoomIn is newAgentRoom in an environment the test configured.
func newAgentRoomIn(t *testing.T, env *testkit.Env) *agentRoom {
	t.Helper()
	alice := env.NewUser("alice")
	f := &agentRoom{env: env, alice: alice, agent: alice.Token(dto.TokenKindAgent, "laptop")}
	f.agentID = registerAgent(t, env, alice, "builder")
	f.room = createRoom(alice, "Support")
	alice.Post("/api/v1/rooms/"+f.room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: f.agentID}).RequireStatus(http.StatusCreated)
	return f
}
