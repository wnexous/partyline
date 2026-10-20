package rooms

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/attachments"
)

const day = 24 * time.Hour

// putBlob stores content and backdates the blob file so it is past the sweep grace period.
func putBlob(t *testing.T, blobs *attachments.Store, dir, content string, mtime time.Time) File {
	t.Helper()
	sha, size, err := blobs.Put(strings.NewReader(content), 1<<20)
	require.NoError(t, err)
	require.NoError(t, os.Chtimes(filepath.Join(dir, sha[:2], sha), mtime, mtime))
	return File{SHA256: sha, Name: content + ".txt", Mime: "text/plain", Size: size}
}

func blobExists(dir, sha string) bool {
	_, err := os.Stat(filepath.Join(dir, sha[:2], sha))
	return err == nil
}

// OPS-02: messages older than the room's retention days are deleted (0 keeps all); blobs no
// message references any more are removed while blobs still referenced are kept.
func TestRetentionCleanup(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	blobDir := filepath.Join(f.dir, "blobs")
	blobs, err := attachments.Open(blobDir)
	require.NoError(t, err)
	f.svc = New(Deps{Store: f.st, Access: f.az, Bus: f.bus, Now: f.clock.Now, Blobs: blobs})

	alice := f.user(t, "alice", dto.RoleMember)
	short := f.room(t, alice, "short")
	_, err = f.svc.Update(ctx, alice, short.ID, UpdateInput{RetentionDays: ptr(7)})
	require.NoError(t, err)
	forever := f.room(t, alice, "forever")
	_, err = f.svc.Update(ctx, alice, forever.ID, UpdateInput{RetentionDays: ptr(0)})
	require.NoError(t, err)

	old := t0.Add(-time.Hour)
	only := putBlob(t, blobs, blobDir, "only-in-old", old)
	shared := putBlob(t, blobs, blobDir, "shared", old)

	// Day 0: old messages in both rooms; the short room's one carries both blobs.
	expired, err := f.svc.Post(ctx, alice, short.ID, PostInput{Text: "old", Files: []File{only, shared}})
	require.NoError(t, err)
	f.post(t, alice, forever.ID, "kept forever")

	// Day 5: a message that is still within 7 days at day 8 keeps the shared blob alive.
	f.clock.now = t0.Add(5 * day)
	recent, err := f.svc.Post(ctx, alice, short.ID, PostInput{Text: "recent", Files: []File{shared}})
	require.NoError(t, err)

	// Exactly 7 days old is not yet older than the retention.
	f.clock.now = t0.Add(7 * day)
	res, err := f.svc.Cleanup(ctx)
	require.NoError(t, err)
	require.Zero(t, res.Messages)
	require.Equal(t, 1, f.count(t, "SELECT COUNT(*) FROM messages WHERE id = ?", expired.ID))

	// An unreferenced blob stored a minute ago survives: it may belong to an upload in flight.
	f.clock.now = t0.Add(8 * day)
	fresh := putBlob(t, blobs, blobDir, "in-flight", f.clock.now.Add(-time.Minute))
	res, err = f.svc.Cleanup(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), res.Messages)
	require.Equal(t, 1, res.Blobs)

	require.Zero(t, f.count(t, "SELECT COUNT(*) FROM messages WHERE id = ?", expired.ID))
	require.Zero(t, f.count(t, "SELECT COUNT(*) FROM attachments WHERE message_id = ?", expired.ID))
	require.Equal(t, 1, f.count(t, "SELECT COUNT(*) FROM messages WHERE id = ?", recent.ID))
	require.Equal(t, 1, f.count(t, "SELECT COUNT(*) FROM messages WHERE room_id = ?", forever.ID), "retention 0 keeps all")
	require.False(t, blobExists(blobDir, only.SHA256), "orphan blob removed")
	require.True(t, blobExists(blobDir, shared.SHA256), "blob still referenced is kept")
	require.True(t, blobExists(blobDir, fresh.SHA256), "fresh blob within grace is kept")

	// Much later every short-room message is gone, the forever room is untouched.
	f.clock.now = t0.Add(400 * day)
	_, err = f.svc.Cleanup(ctx)
	require.NoError(t, err)
	require.Zero(t, f.count(t, "SELECT COUNT(*) FROM messages WHERE room_id = ?", short.ID))
	require.Equal(t, 1, f.count(t, "SELECT COUNT(*) FROM messages WHERE room_id = ?", forever.ID))
	require.False(t, blobExists(blobDir, shared.SHA256))
}

// ROOM-02: deleting a room removes its blob files right after the delete commits, except
// blobs another room's attachments still reference and blobs written in the last minute
// (an upload of the same content may still be committing; the sweep removes those later).
func TestDeleteRoomRemovesUnsharedBlobs(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	blobDir := filepath.Join(f.dir, "blobs")
	blobs, err := attachments.Open(blobDir)
	require.NoError(t, err)
	f.svc = New(Deps{Store: f.st, Access: f.az, Bus: f.bus, Now: f.clock.Now, Blobs: blobs})

	alice := f.user(t, "alice", dto.RoleMember)
	doomed := f.room(t, alice, "doomed")
	kept := f.room(t, alice, "kept")
	old := t0.Add(-time.Hour)
	only := putBlob(t, blobs, blobDir, "only-in-doomed", old)
	twice := putBlob(t, blobs, blobDir, "twice-in-doomed", old)
	shared := putBlob(t, blobs, blobDir, "shared", old)
	fresh := putBlob(t, blobs, blobDir, "fresh", t0)
	_, err = f.svc.Post(ctx, alice, doomed.ID, PostInput{Text: "files", Files: []File{only, twice, shared, fresh}})
	require.NoError(t, err)
	_, err = f.svc.Post(ctx, alice, doomed.ID, PostInput{Text: "again", Files: []File{twice}})
	require.NoError(t, err)
	_, err = f.svc.Post(ctx, alice, kept.ID, PostInput{Text: "shared", Files: []File{shared}})
	require.NoError(t, err)

	require.NoError(t, f.svc.Delete(ctx, alice, doomed.ID))

	require.False(t, blobExists(blobDir, only.SHA256), "a blob only the deleted room used is removed at once")
	require.False(t, blobExists(blobDir, twice.SHA256), "a blob used twice in the deleted room is removed")
	require.True(t, blobExists(blobDir, shared.SHA256), "a blob another room references stays")
	require.True(t, blobExists(blobDir, fresh.SHA256), "a blob written in the last minute is left to the sweep")
}
