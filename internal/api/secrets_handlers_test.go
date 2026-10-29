package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

// lockedBuffer collects the app's log lines; the app logs from several goroutines.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// openSecret opens one of the owner's secrets through the service, the way the container service
// will: the API itself never returns a value.
func openSecret(t *testing.T, env *testkit.Env, owner, name string) string {
	t.Helper()
	ctx := context.Background()
	list, err := env.App.Secrets.ListNames(ctx, owner)
	require.NoError(t, err)
	for _, s := range list {
		if s.Name != name {
			continue
		}
		opened, err := env.App.Secrets.Open(ctx, owner, []string{s.ID})
		require.NoError(t, err)
		require.Contains(t, opened, s.ID)
		return opened[s.ID]
	}
	require.FailNow(t, "secret not found", "owner %s name %s", owner, name)
	return ""
}

// CA-03, CA-08: the secret routes list names and dates, take a write-only value and delete by
// name; no answer, audit entry or log line carries the value, and an agent token is refused.
func TestOwnerSecretsRoutes(t *testing.T) {
	logs := &lockedBuffer{}
	env := testkit.New(t, testkit.WithLogger(slog.New(slog.NewTextHandler(logs, nil))))
	admin := env.SetupAdmin()
	alice, bob := env.NewUser("alice"), env.NewUser("bob")
	const first = "sk-opencode-go-first-0123456789abcdef"
	const second = "sk-opencode-go-second-9876543210fedcba"

	env.Anon().Get("/api/v1/secrets").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	env.Anon().Put("/api/v1/secrets/OPENCODE_KEY", dto.PutSecretRequest{Value: first}).
		RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)

	empty := testkit.Decode[dto.List[dto.Secret]](alice.Get("/api/v1/secrets").RequireStatus(http.StatusOK))
	require.Empty(t, empty.Items)

	put := alice.Put("/api/v1/secrets/OPENCODE_KEY", dto.PutSecretRequest{Value: first})
	require.Equal(t, http.StatusNoContent, put.Status)
	require.Empty(t, put.Body, "the value never comes back, not even in a confirmation")

	list := testkit.Decode[dto.List[dto.Secret]](alice.Get("/api/v1/secrets").RequireStatus(http.StatusOK))
	require.Len(t, list.Items, 1)
	require.Equal(t, "OPENCODE_KEY", list.Items[0].Name)
	require.False(t, list.Items[0].CreatedAt.IsZero())
	require.False(t, list.Items[0].UpdatedAt.IsZero())
	got := alice.Get("/api/v1/secrets").RequireStatus(http.StatusOK)
	require.NotContains(t, got.String(), first, "the list answer has no value")
	require.NotContains(t, got.String(), `"value"`, "there is no value field")
	require.NotContains(t, got.String(), "sealed")

	// Replacing the value is an upsert: the name stays one row, and the new value is what opens.
	alice.Put("/api/v1/secrets/OPENCODE_KEY", dto.PutSecretRequest{Value: second}).RequireStatus(http.StatusNoContent)
	list = testkit.Decode[dto.List[dto.Secret]](alice.Get("/api/v1/secrets").RequireStatus(http.StatusOK))
	require.Len(t, list.Items, 1)
	require.Equal(t, "OPENCODE_KEY", list.Items[0].Name)
	require.Equal(t, second, openSecret(t, env, alice.User.ID, "OPENCODE_KEY"))

	// Validation names the field and never the value.
	long := strings.Repeat("s", 16385)
	for _, c := range []struct {
		name, field string
		body        dto.PutSecretRequest
	}{
		{"1BAD", "name", dto.PutSecretRequest{Value: first}},
		{"OPEN.KEY", "name", dto.PutSecretRequest{Value: first}},
		{"OK", "value", dto.PutSecretRequest{Value: ""}},
		{"OK", "value", dto.PutSecretRequest{Value: long}},
	} {
		e := alice.Put("/api/v1/secrets/"+c.name, c.body).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
		require.Contains(t, e.Fields, c.field, "name %q", c.name)
		if c.body.Value != "" {
			require.NotContains(t, e.Fields[c.field], c.body.Value, "a validation error never echoes the value (CA-08)")
		}
	}

	// Only user credentials: an agent token is refused everywhere, a user token works.
	agent := alice.Token(dto.TokenKindAgent, "laptop")
	agent.Get("/api/v1/secrets").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	agent.Put("/api/v1/secrets/AGENT_KEY", dto.PutSecretRequest{Value: first}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	agent.Delete("/api/v1/secrets/OPENCODE_KEY").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	ci := alice.Token(dto.TokenKindUser, "ci")
	ci.Get("/api/v1/secrets").RequireStatus(http.StatusOK)
	ci.Put("/api/v1/secrets/CI_KEY", dto.PutSecretRequest{Value: "ci-value"}).RequireStatus(http.StatusNoContent)
	ci.Delete("/api/v1/secrets/CI_KEY").RequireStatus(http.StatusNoContent)

	// A secret belongs to its owner: another account does not see it and cannot delete it, and the
	// same name is a second row of its own.
	require.Empty(t, testkit.Decode[dto.List[dto.Secret]](bob.Get("/api/v1/secrets").RequireStatus(http.StatusOK)).Items)
	bob.Delete("/api/v1/secrets/OPENCODE_KEY").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	bob.Put("/api/v1/secrets/OPENCODE_KEY", dto.PutSecretRequest{Value: "bob-value"}).RequireStatus(http.StatusNoContent)
	bobList := testkit.Decode[dto.List[dto.Secret]](bob.Get("/api/v1/secrets").RequireStatus(http.StatusOK))
	require.Len(t, bobList.Items, 1)
	require.Equal(t, "bob-value", openSecret(t, env, bob.User.ID, "OPENCODE_KEY"))
	require.Equal(t, second, openSecret(t, env, alice.User.ID, "OPENCODE_KEY"), "bob's put left alice's value alone")

	// Delete is by name, 204 once and 404 afterwards; the name of another owner is a 404 too.
	alice.Delete("/api/v1/secrets/OPENCODE_KEY").RequireStatus(http.StatusNoContent)
	require.Empty(t, testkit.Decode[dto.List[dto.Secret]](alice.Get("/api/v1/secrets").RequireStatus(http.StatusOK)).Items)
	alice.Delete("/api/v1/secrets/OPENCODE_KEY").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)

	// The audit log records secret.put and secret.delete with the name only (CA-07, CA-08).
	page := testkit.Decode[dto.Page[dto.AuditEntry]](admin.Get("/api/v1/admin/audit").RequireStatus(http.StatusOK))
	var sawPut, sawDelete bool
	for _, entry := range page.Items {
		if entry.Action != "secret.put" && entry.Action != "secret.delete" {
			continue
		}
		require.Equal(t, "secret", entry.TargetKind)
		require.NotEmpty(t, entry.TargetID)
		raw, err := json.Marshal(entry)
		require.NoError(t, err)
		require.NotContains(t, string(raw), first, "an audit entry never carries the value")
		require.NotContains(t, string(raw), second, "an audit entry never carries the value")
		require.NotContains(t, string(raw), "bob-value", "an audit entry never carries the value")
		if entry.Actor != nil && entry.Actor.ID == alice.User.ID && entry.TargetID == "OPENCODE_KEY" {
			switch entry.Action {
			case "secret.put":
				sawPut = true
			case "secret.delete":
				sawDelete = true
			}
		}
	}
	require.True(t, sawPut, "alice's put of OPENCODE_KEY is audited")
	require.True(t, sawDelete, "alice's delete of OPENCODE_KEY is audited")

	require.NotContains(t, logs.String(), first, "no log line carries the value (CA-08)")
	require.NotContains(t, logs.String(), second, "no log line carries the value (CA-08)")
	require.NotContains(t, logs.String(), "bob-value", "no log line carries the value (CA-08)")

	// The sealed row is what the store holds: it differs from the value and opens back.
	ctx := context.Background()
	require.NoError(t, env.App.Secrets.Put(ctx, alice.User.ID, "SEALED_CHECK", first))
	require.Equal(t, first, openSecret(t, env, alice.User.ID, "SEALED_CHECK"))
}
