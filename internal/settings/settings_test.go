package settings

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/audit"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/validation"
)

func newService(t *testing.T, defaultDays int) (*Service, *store.Store, *int) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "partyline.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	require.NoError(t, st.Migrate(context.Background()))
	now := func() time.Time { return time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC) }
	changed := 0
	svc := New(Deps{
		Store: st, Audit: audit.New(st, now), AttachmentRetentionDays: defaultDays, Now: now,
		Changed: func() { changed++ },
	})
	return svc, st, &changed
}

// RET-01: without an admin's value the configuration's default applies.
func TestAttachmentRetentionDefault(t *testing.T) {
	svc, _, _ := newService(t, 30)
	got, err := svc.AttachmentRetention(context.Background())
	require.NoError(t, err)
	require.Equal(t, AttachmentRetention{Days: 30, DefaultDays: 30}, got)
	days, err := svc.AttachmentRetentionDays(context.Background())
	require.NoError(t, err)
	require.Equal(t, 30, days)
}

// RET-01, RET-02: an admin's value wins over the default, 0 included, until the admin resets it;
// each change is audited with the old and new values and wakes the retention job.
func TestSetAttachmentRetention(t *testing.T) {
	ctx := context.Background()
	svc, st, changed := newService(t, 30)

	zero := 0
	got, err := svc.SetAttachmentRetention(ctx, &zero)
	require.NoError(t, err)
	require.Equal(t, AttachmentRetention{Days: 0, DefaultDays: 30, Overridden: true}, got)
	require.Equal(t, 1, *changed)

	seven := 7
	got, err = svc.SetAttachmentRetention(ctx, &seven)
	require.NoError(t, err)
	require.Equal(t, AttachmentRetention{Days: 7, DefaultDays: 30, Overridden: true}, got)
	again, err := svc.AttachmentRetention(ctx)
	require.NoError(t, err)
	require.Equal(t, got, again)

	// The same value again changes nothing: no audit entry, no wake-up.
	_, err = svc.SetAttachmentRetention(ctx, &seven)
	require.NoError(t, err)
	require.Equal(t, 2, *changed)

	got, err = svc.SetAttachmentRetention(ctx, nil)
	require.NoError(t, err)
	require.Equal(t, AttachmentRetention{Days: 30, DefaultDays: 30}, got)
	require.Equal(t, 3, *changed)

	entries, _, err := audit.New(st, nil).List(ctx, "")
	require.NoError(t, err)
	require.Len(t, entries, 3)
	for _, e := range entries {
		require.Equal(t, audit.ActionInstanceSettingsUpdate, e.Action)
		require.Equal(t, audit.TargetInstance, e.TargetKind)
		require.Equal(t, KeyAttachmentRetentionDays, e.TargetID)
	}
	var detail map[string]any
	require.NoError(t, json.Unmarshal([]byte(entries[0].DetailJson), &detail), "newest first: the reset")
	require.Equal(t, float64(7), detail["from"])
	require.Equal(t, float64(30), detail["to"])
	require.Equal(t, false, detail["overridden"])
}

// RET-02: a value outside 0..36500 is refused and nothing changes.
func TestSetAttachmentRetentionBounds(t *testing.T) {
	ctx := context.Background()
	svc, _, changed := newService(t, 0)
	for _, days := range []int{-1, MaxAttachmentRetentionDays + 1} {
		_, err := svc.SetAttachmentRetention(ctx, &days)
		var verr *validation.Error
		require.ErrorAs(t, err, &verr, days)
		require.Contains(t, verr.Fields, "attachment_retention_days")
	}
	limit := MaxAttachmentRetentionDays
	got, err := svc.SetAttachmentRetention(ctx, &limit)
	require.NoError(t, err)
	require.Equal(t, MaxAttachmentRetentionDays, got.Days)
	require.Equal(t, 1, *changed)
}
