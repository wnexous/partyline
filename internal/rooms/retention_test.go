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
	"github.com/wnexous/partyline/internal/platform"
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
	// The one-minute grace boundary, on each side.
	fresh := putBlob(t, blobs, blobDir, "fresh", t0.Add(-59*time.Second))
	justOld := putBlob(t, blobs, blobDir, "just-old", t0.Add(-61*time.Second))
	_, err = f.svc.Post(ctx, alice, doomed.ID, PostInput{Text: "files", Files: []File{only, twice, shared, fresh, justOld}})
	require.NoError(t, err)
	_, err = f.svc.Post(ctx, alice, doomed.ID, PostInput{Text: "again", Files: []File{twice}})
	require.NoError(t, err)
	_, err = f.svc.Post(ctx, alice, kept.ID, PostInput{Text: "shared", Files: []File{shared}})
	require.NoError(t, err)

	require.NoError(t, f.svc.Delete(ctx, alice, doomed.ID))

	require.False(t, blobExists(blobDir, only.SHA256), "a blob only the deleted room used is removed at once")
	require.False(t, blobExists(blobDir, twice.SHA256), "a blob used twice in the deleted room is removed")
	require.True(t, blobExists(blobDir, shared.SHA256), "a blob another room references stays")
	require.True(t, blobExists(blobDir, fresh.SHA256), "a blob written 59 s ago is left to the sweep")
	require.False(t, blobExists(blobDir, justOld.SHA256), "a blob written 61 s ago is removed")
}

// platformFiles queues an item of Atlas (owned by alice) with one stored file per blob and returns
// the platform queue and the item id.
func (f *fixture) platformFiles(t *testing.T, q *platform.Queue, agent, connector, chat, external string, files ...File) string {
	t.Helper()
	it := platform.Item{AgentID: agent, ConnectorID: connector, ChatID: chat, ExternalID: external, SenderName: "Pat", ReceivedAt: t0}
	for _, file := range files {
		it.Attachments = append(it.Attachments, platform.Attachment{
			Name: file.Name, Mime: file.Mime, Size: file.Size, SHA256: file.SHA256, Status: dto.AttachmentStatusStored,
		})
	}
	id, queued, err := q.Enqueue(context.Background(), it)
	require.NoError(t, err)
	require.True(t, queued)
	return id
}

// CID-09: the blob sweep counts the files of platform items as references: a blob only a queued
// item uses survives the sweep however old it is, and goes once the item is gone.
func TestRetentionSweepKeepsBlobsOfPlatformItems(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	blobDir := filepath.Join(f.dir, "blobs")
	blobs, err := attachments.Open(blobDir)
	require.NoError(t, err)
	f.svc = New(Deps{Store: f.st, Access: f.az, Bus: f.bus, Now: f.clock.Now, Blobs: blobs})
	alice := f.user(t, "alice", dto.RoleMember)
	atlas := f.agent(t, alice, "atlas")
	connector, chat := f.chat(t, alice, "Family")
	q := platform.New(platform.Deps{Store: f.st, Bus: f.bus, Now: f.clock.Now})

	old := t0.Add(-time.Hour)
	queued := putBlob(t, blobs, blobDir, "in-the-queue", old)
	orphan := putBlob(t, blobs, blobDir, "orphan", old)
	f.platformFiles(t, q, atlas, connector, chat, "1", queued)

	f.clock.now = t0.Add(3 * day)
	res, err := f.svc.Cleanup(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, res.Blobs)
	require.True(t, blobExists(blobDir, queued.SHA256), "a blob a queued item uses is a reference")
	require.False(t, blobExists(blobDir, orphan.SHA256), "an unreferenced blob goes")

	_, err = q.Purge(ctx, t0.Add(platform.UnackedTTL)) // the item expires
	require.NoError(t, err)
	res, err = f.svc.Cleanup(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, res.Blobs)
	require.False(t, blobExists(blobDir, queued.SHA256), "no item uses it any more")
}

// CID-09: the blobs of purged platform items are released at once, except those a room
// attachment, another platform item or an avatar still uses and those written in the last
// minute; the sweep removes those later.
func TestReleaseBlobsKeepsWhatIsStillReferenced(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	blobDir := filepath.Join(f.dir, "blobs")
	blobs, err := attachments.Open(blobDir)
	require.NoError(t, err)
	f.svc = New(Deps{Store: f.st, Access: f.az, Bus: f.bus, Now: f.clock.Now, Blobs: blobs})
	alice := f.user(t, "alice", dto.RoleMember)
	atlas := f.agent(t, alice, "atlas")
	connector, chat := f.chat(t, alice, "Family")
	q := platform.New(platform.Deps{Store: f.st, Bus: f.bus, Now: f.clock.Now})

	old := t0.Add(-time.Hour)
	only := putBlob(t, blobs, blobDir, "only-in-the-item", old)
	inRoom := putBlob(t, blobs, blobDir, "also-in-a-room", old)
	inItem := putBlob(t, blobs, blobDir, "also-in-another-item", old)
	avatar := putBlob(t, blobs, blobDir, "also-an-avatar", old)
	fresh := putBlob(t, blobs, blobDir, "fresh", t0.Add(-59*time.Second))
	justOld := putBlob(t, blobs, blobDir, "just-old", t0.Add(-61*time.Second))
	acked := f.platformFiles(t, q, atlas, connector, chat, "1", only, inRoom, inItem, avatar, fresh, justOld)
	f.platformFiles(t, q, atlas, connector, chat, "2", inItem)
	room := f.room(t, alice, "files")
	_, err = f.svc.Post(ctx, alice, room.ID, PostInput{Text: "a file", Files: []File{inRoom}})
	require.NoError(t, err)
	f.exec(t, "UPDATE intelligences SET avatar_sha256 = ? WHERE id = ?", avatar.SHA256, alice.UserID)
	require.NoError(t, q.Ack(ctx, atlas, acked))

	// 24 hours minus a millisecond after the ack nothing goes, not even the blobs.
	res, err := q.Purge(ctx, t0.Add(platform.AckedTTL-time.Millisecond))
	require.NoError(t, err)
	require.Zero(t, res.Acked)
	f.svc.ReleaseBlobs(ctx, res.Files)
	require.True(t, blobExists(blobDir, only.SHA256), "kept until 24 hours after the ack")

	res, err = q.Purge(ctx, t0.Add(platform.AckedTTL))
	require.NoError(t, err)
	require.Equal(t, int64(1), res.Acked)
	f.svc.ReleaseBlobs(ctx, res.Files)

	require.False(t, blobExists(blobDir, only.SHA256), "a blob only the purged item used is released")
	require.True(t, blobExists(blobDir, inRoom.SHA256), "a blob a room attachment uses stays")
	require.True(t, blobExists(blobDir, inItem.SHA256), "a blob another item uses stays")
	require.True(t, blobExists(blobDir, avatar.SHA256), "a blob an avatar uses stays")
	require.True(t, blobExists(blobDir, fresh.SHA256), "a blob written 59 s ago is left to the sweep")
	require.False(t, blobExists(blobDir, justOld.SHA256), "a blob written 61 s ago is released")

	// The other item goes too: its blob was the last use.
	res, err = q.Purge(ctx, t0.Add(platform.UnackedTTL))
	require.NoError(t, err)
	require.Equal(t, int64(1), res.Expired)
	f.svc.ReleaseBlobs(ctx, res.Files)
	require.False(t, blobExists(blobDir, inItem.SHA256), "the last item that used it expired")
	require.True(t, blobExists(blobDir, inRoom.SHA256), "the room attachment still uses its blob")
}
