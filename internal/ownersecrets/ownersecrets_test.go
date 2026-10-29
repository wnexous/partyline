package ownersecrets_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/ownersecrets"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
	"github.com/wnexous/partyline/internal/validation"
)

// ownerSecrets returns the owner's rows through the service and the sealed rows through the store,
// so a test can prove that the value is sealed and only the container service opens it.
func ownerSecrets(t *testing.T, env *testkit.Env, svc *ownersecrets.Service, owner string) ([]ownersecrets.Secret, []db.OwnerSecret) {
	t.Helper()
	ctx := context.Background()
	list, err := svc.ListNames(ctx, owner)
	require.NoError(t, err)
	ids := make([]string, len(list))
	for i, s := range list {
		ids[i] = s.ID
	}
	rows, err := env.App.Store.R().GetOwnerSecrets(ctx, db.GetOwnerSecretsParams{OwnerID: owner, Ids: ids})
	require.NoError(t, err)
	return list, rows
}

// CA-03, CA-08: a value is sealed at rest, the list returns the name and the dates only, and only
// Open gives the value back.
func TestPutSealsTheValueAndListNamesHasNoValue(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	start := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	clock := testkit.NewClock(start)
	svc := ownersecrets.New(ownersecrets.Deps{Store: env.App.Store, Box: env.App.Box, Now: clock.Now})
	ctx := context.Background()
	const value = "sk-opencode-go-0123456789abcdef"

	require.NoError(t, svc.Put(ctx, alice.User.ID, "OPENCODE_KEY", value))

	list, rows := ownerSecrets(t, env, svc, alice.User.ID)
	require.Len(t, list, 1)
	require.Equal(t, "OPENCODE_KEY", list[0].Name)
	require.NotEmpty(t, list[0].ID, "the id is what a container's env mapping keeps")
	require.Equal(t, start, list[0].CreatedAt)
	require.Equal(t, start, list[0].UpdatedAt)

	require.Len(t, rows, 1)
	require.NotEqual(t, []byte(value), rows[0].ValueSealed, "the database holds the sealed value")
	require.NotContains(t, string(rows[0].ValueSealed), value)

	opened, err := svc.Open(ctx, alice.User.ID, []string{list[0].ID})
	require.NoError(t, err)
	require.Equal(t, map[string]string{list[0].ID: value}, opened, "the sealed bytes open back to the value")
}

// CA-03: replacing a name keeps the row (and so every env mapping) and its creation time, and only
// the value and updated_at change.
func TestPutReplacesTheValueInPlace(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	start := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	clock := testkit.NewClock(start)
	svc := ownersecrets.New(ownersecrets.Deps{Store: env.App.Store, Box: env.App.Box, Now: clock.Now})
	ctx := context.Background()

	require.NoError(t, svc.Put(ctx, alice.User.ID, "CLAUDE_TOKEN", "first-value"))
	first, _ := ownerSecrets(t, env, svc, alice.User.ID)
	clock.Advance(30 * time.Minute)
	require.NoError(t, svc.Put(ctx, alice.User.ID, "CLAUDE_TOKEN", "second-value"))

	second, _ := ownerSecrets(t, env, svc, alice.User.ID)
	require.Len(t, second, 1)
	require.Equal(t, first[0].ID, second[0].ID, "the id survives a replacement")
	require.Equal(t, start, second[0].CreatedAt, "the creation time survives a replacement")
	require.Equal(t, start.Add(30*time.Minute), second[0].UpdatedAt)

	opened, err := svc.Open(ctx, alice.User.ID, []string{second[0].ID})
	require.NoError(t, err)
	require.Equal(t, "second-value", opened[second[0].ID])
}

// CA-03, CA-08: names follow the environment variable pattern and values have 1 to 16384 bytes; a
// refusal names the field and never echoes the value.
func TestPutValidatesNameAndValue(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	svc := ownersecrets.New(ownersecrets.Deps{Store: env.App.Store, Box: env.App.Box})
	ctx := context.Background()

	longValue := strings.Repeat("s", ownersecrets.MaxValueBytes+1)
	for _, name := range []string{"", "1KEY", "OPEN KEY", "OPEN.KEY", "OPEN;KEY", "OPEN\nKEY", strings.Repeat("A", ownersecrets.MaxNameLength+1)} {
		err := svc.Put(ctx, alice.User.ID, name, "value")
		var invalid *validation.Error
		require.ErrorAs(t, err, &invalid, "name %q", name)
		require.Equal(t, "must start with a letter and have 1 to 64 letters, digits, _ or -", invalid.Fields["name"], "name %q", name)
	}
	for _, value := range []string{"", longValue} {
		err := svc.Put(ctx, alice.User.ID, "OPENCODE_KEY", value)
		var invalid *validation.Error
		require.ErrorAs(t, err, &invalid, "value of %d bytes", len(value))
		require.Equal(t, "must have 1 to 16384 bytes", invalid.Fields["value"])
		if value != "" {
			require.NotContains(t, err.Error(), value, "an error never carries the value (CA-08)")
		}
	}

	for _, name := range []string{"A", "OPENCODE_KEY", "open-code_KEY9", strings.Repeat("A", ownersecrets.MaxNameLength)} {
		require.NoError(t, svc.Put(ctx, alice.User.ID, name, "value"), "name %q", name)
	}
	require.NoError(t, svc.Put(ctx, alice.User.ID, "BIG", strings.Repeat("s", ownersecrets.MaxValueBytes)),
		"16384 bytes is the largest accepted value")
	list, err := svc.ListNames(ctx, alice.User.ID)
	require.NoError(t, err)
	require.Len(t, list, 5, "nothing invalid was stored")
}

// CA-07: a secret belongs to its owner. Another owner never opens it, deletes it or sees it, and a
// same-named secret of both is two rows.
func TestSecretsAreScopedToTheirOwner(t *testing.T) {
	env := testkit.New(t)
	alice, bob := env.NewUser("alice"), env.NewUser("bob")
	svc := ownersecrets.New(ownersecrets.Deps{Store: env.App.Store, Box: env.App.Box})
	ctx := context.Background()

	require.NoError(t, svc.Put(ctx, alice.User.ID, "OPENCODE_KEY", "alice-value"))
	require.NoError(t, svc.Put(ctx, bob.User.ID, "OPENCODE_KEY", "bob-value"))

	aliceList, _ := ownerSecrets(t, env, svc, alice.User.ID)
	bobList, _ := ownerSecrets(t, env, svc, bob.User.ID)
	require.Len(t, aliceList, 1)
	require.Len(t, bobList, 1)
	require.NotEqual(t, aliceList[0].ID, bobList[0].ID, "one row per owner")

	opened, err := svc.Open(ctx, bob.User.ID, []string{aliceList[0].ID, bobList[0].ID})
	require.NoError(t, err)
	require.Equal(t, map[string]string{bobList[0].ID: "bob-value"}, opened, "an id of another owner is not opened (CA-07)")

	require.NoError(t, svc.Delete(ctx, bob.User.ID, "OPENCODE_KEY"), "bob deletes his own")
	require.NoError(t, svc.Put(ctx, bob.User.ID, "OPENCODE_KEY", "bob-value"))
	require.ErrorIs(t, svc.Delete(ctx, bob.User.ID, "CLAUDE_TOKEN"), ownersecrets.ErrNotFound, "a name bob does not have is not found")
	opened, err = svc.Open(ctx, alice.User.ID, []string{aliceList[0].ID})
	require.NoError(t, err)
	require.Equal(t, "alice-value", opened[aliceList[0].ID], "alice's secret is untouched")

	require.NoError(t, svc.Delete(ctx, alice.User.ID, "OPENCODE_KEY"))
	require.ErrorIs(t, svc.Delete(ctx, alice.User.ID, "OPENCODE_KEY"), ownersecrets.ErrNotFound, "a second delete finds nothing")
}

// CA-07, CA-08: Open returns exactly the ids asked for and an empty map for none.
func TestOpenReturnsOnlyTheRequestedIDs(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	svc := ownersecrets.New(ownersecrets.Deps{Store: env.App.Store, Box: env.App.Box})
	ctx := context.Background()

	require.NoError(t, svc.Put(ctx, alice.User.ID, "ONE", "first"))
	require.NoError(t, svc.Put(ctx, alice.User.ID, "TWO", "second"))
	list, _ := ownerSecrets(t, env, svc, alice.User.ID)
	require.Len(t, list, 2)

	opened, err := svc.Open(ctx, alice.User.ID, []string{list[0].ID})
	require.NoError(t, err)
	require.Equal(t, map[string]string{list[0].ID: "first"}, opened)

	empty, err := svc.Open(ctx, alice.User.ID, nil)
	require.NoError(t, err)
	require.Empty(t, empty)
	require.NotNil(t, empty, "no ids is an empty map, not an error")
}
