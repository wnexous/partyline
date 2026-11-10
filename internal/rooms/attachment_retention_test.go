package rooms

import (
	"context"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/attachments"
	"github.com/wnexous/partyline/internal/platform"
)

// retentionFixture is a fixture whose service has a blob store and an attachment retention the test
// changes through days, with a room that keeps its messages forever, so only the retention of
// attachments takes files away.
type retentionFixture struct {
	*fixture
	blobs   *attachments.Store
	blobDir string
	days    atomic.Int64
	// reads counts the reads of the setting, one per retention pass.
	reads atomic.Int64
	room  Room
}

func newRetentionFixture(t *testing.T, days int) (*retentionFixture, func(...File) Message) {
	t.Helper()
	f := newFixture(t)
	rf := &retentionFixture{fixture: f, blobDir: filepath.Join(f.dir, "blobs")}
	rf.days.Store(int64(days))
	var err error
	rf.blobs, err = attachments.Open(rf.blobDir)
	require.NoError(t, err)
	f.svc = New(Deps{
		Store: f.st, Access: f.az, Bus: f.bus, Now: f.clock.Now, Blobs: rf.blobs, Transcribe: true,
		AttachmentRetentionDays: func(context.Context) (int, error) {
			rf.reads.Add(1)
			return int(rf.days.Load()), nil
		},
	})
	alice := f.user(t, "alice", dto.RoleMember)
	rf.room = f.room(t, alice, "forever")
	_, err = f.svc.Update(context.Background(), alice, rf.room.ID, UpdateInput{RetentionDays: ptr(0)})
	require.NoError(t, err)
	post := func(files ...File) Message {
		t.Helper()
		m, err := f.svc.Post(context.Background(), alice, rf.room.ID, PostInput{Files: files})
		require.NoError(t, err)
		return m
	}
	return rf, post
}

func (rf *retentionFixture) status(t *testing.T, id string) (status, sha string) {
	t.Helper()
	require.NoError(t, rf.raw.QueryRowContext(context.Background(),
		"SELECT status, sha256 FROM attachments WHERE id = ?", id).Scan(&status, &sha))
	return status, sha
}

// RET-03, RET-04, RET-05: a stored attachment older than the retention expires (status expired, no
// hash), its blob goes when nothing else uses it and its server transcript goes with it; a younger one
// stays stored, and the message itself stays.
func TestAttachmentRetentionExpiresOldFiles(t *testing.T) {
	ctx := context.Background()
	rf, post := newRetentionFixture(t, 30)
	old := t0.Add(-2 * time.Hour)
	voice := putBlob(t, rf.blobs, rf.blobDir, "old-voice", old)
	voice.Mime = "audio/ogg"
	report := putBlob(t, rf.blobs, rf.blobDir, "old-report", old)
	oldMsg := post(voice, report)
	require.Equal(t, 1, rf.count(t, "SELECT COUNT(*) FROM transcriptions WHERE message_id = ?", oldMsg.ID))

	rf.clock.now = t0.Add(20 * day)
	young := putBlob(t, rf.blobs, rf.blobDir, "young", rf.clock.now.Add(-2*time.Hour))
	youngMsg := post(young)

	// Exactly 30 days old is not older than the retention.
	rf.clock.now = t0.Add(30 * day)
	res, err := rf.svc.Cleanup(ctx)
	require.NoError(t, err)
	require.Zero(t, res.Attachments)

	rf.clock.now = t0.Add(31 * day)
	res, err = rf.svc.Cleanup(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(2), res.Attachments)
	require.Equal(t, 2, res.Blobs)
	for _, a := range oldMsg.Attachments {
		status, sha := rf.status(t, a.ID)
		require.Equal(t, string(dto.AttachmentStatusExpired), status)
		require.Empty(t, sha, "an expired attachment keeps no hash of its content")
	}
	require.False(t, blobExists(rf.blobDir, voice.SHA256), "the unshared blob is deleted")
	require.False(t, blobExists(rf.blobDir, report.SHA256))
	require.Zero(t, rf.count(t, "SELECT COUNT(*) FROM transcriptions WHERE message_id = ?", oldMsg.ID), "RET-05")
	require.Equal(t, 1, rf.count(t, "SELECT COUNT(*) FROM messages WHERE id = ?", oldMsg.ID), "the message stays")

	status, _ := rf.status(t, youngMsg.Attachments[0].ID)
	require.Equal(t, string(dto.AttachmentStatusStored), status)
	require.True(t, blobExists(rf.blobDir, young.SHA256))

	// The message reads back with its file expired, its name and type kept.
	m, err := rf.svc.message(ctx, oldMsg.ID)
	require.NoError(t, err)
	require.Len(t, m.Attachments, 2)
	require.Equal(t, string(dto.AttachmentStatusExpired), m.Attachments[0].Status)
	require.Equal(t, voice.Name, m.Attachments[0].Name)

	// A second pass finds nothing more.
	res, err = rf.svc.Cleanup(ctx)
	require.NoError(t, err)
	require.Zero(t, res.Attachments)
}

// RET-04: a blob an expired attachment shares with a younger one stays until that one expires too.
func TestAttachmentRetentionKeepsSharedBlob(t *testing.T) {
	ctx := context.Background()
	rf, post := newRetentionFixture(t, 7)
	shared := putBlob(t, rf.blobs, rf.blobDir, "shared", t0.Add(-2*time.Hour))
	first := post(shared)
	rf.clock.now = t0.Add(5 * day)
	second := post(shared)

	rf.clock.now = t0.Add(8 * day)
	res, err := rf.svc.Cleanup(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), res.Attachments)
	status, _ := rf.status(t, first.Attachments[0].ID)
	require.Equal(t, string(dto.AttachmentStatusExpired), status)
	status, _ = rf.status(t, second.Attachments[0].ID)
	require.Equal(t, string(dto.AttachmentStatusStored), status)
	require.True(t, blobExists(rf.blobDir, shared.SHA256), "the younger attachment still uses the blob")

	rf.clock.now = t0.Add(13 * day)
	res, err = rf.svc.Cleanup(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), res.Attachments)
	require.False(t, blobExists(rf.blobDir, shared.SHA256), "no attachment that is not expired uses it any more")
}

// RET-01: a retention of 0 keeps every attachment, however old.
func TestAttachmentRetentionZeroKeepsEverything(t *testing.T) {
	ctx := context.Background()
	rf, post := newRetentionFixture(t, 0)
	file := putBlob(t, rf.blobs, rf.blobDir, "kept", t0.Add(-2*time.Hour))
	m := post(file)

	rf.clock.now = t0.Add(4000 * day)
	res, err := rf.svc.Cleanup(ctx)
	require.NoError(t, err)
	require.Zero(t, res.Attachments)
	require.Zero(t, res.Blobs)
	status, sha := rf.status(t, m.Attachments[0].ID)
	require.Equal(t, string(dto.AttachmentStatusStored), status)
	require.Equal(t, file.SHA256, sha)
	require.True(t, blobExists(rf.blobDir, file.SHA256))

	// A service without the setting keeps them too.
	rf.svc = New(Deps{Store: rf.st, Access: rf.az, Bus: rf.bus, Now: rf.clock.Now, Blobs: rf.blobs})
	res, err = rf.svc.Cleanup(ctx)
	require.NoError(t, err)
	require.Zero(t, res.Attachments)
	require.True(t, blobExists(rf.blobDir, file.SHA256))
}

// RET-04, RET-10: the same content as an avatar, a platform queue file or a blob written within the
// grace hour stays when the room attachment that used it expires; platform files are not expired.
func TestAttachmentRetentionLeavesOtherBlobs(t *testing.T) {
	ctx := context.Background()
	rf, post := newRetentionFixture(t, 1)
	old := t0.Add(-2 * time.Hour)
	avatar := putBlob(t, rf.blobs, rf.blobDir, "also-an-avatar", old)
	queued := putBlob(t, rf.blobs, rf.blobDir, "also-in-the-queue", old)
	post(avatar, queued)
	bob := rf.user(t, "bob", dto.RoleMember)
	rf.exec(t, "UPDATE intelligences SET avatar_sha256 = ? WHERE id = ?", avatar.SHA256, bob.UserID)
	atlas := rf.agent(t, bob, "atlas")
	connector, chat := rf.chat(t, bob, "Family")
	q := platform.New(platform.Deps{Store: rf.st, Bus: rf.bus, Now: rf.clock.Now})
	rf.platformFiles(t, q, atlas, connector, chat, "1", queued)

	rf.clock.now = t0.Add(3 * day)
	res, err := rf.svc.Cleanup(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(2), res.Attachments)
	require.Zero(t, res.Blobs)
	require.True(t, blobExists(rf.blobDir, avatar.SHA256), "an avatar's blob stays")
	require.True(t, blobExists(rf.blobDir, queued.SHA256), "a platform file's blob stays")
	require.Equal(t, 1, rf.count(t, "SELECT COUNT(*) FROM platform_attachments WHERE status = 'stored'"), "platform files are not expired")
}

// RET-03: KickRetention wakes the retention job before its interval, and never blocks.
func TestKickRetentionRunsAtOnce(t *testing.T) {
	rf, post := newRetentionFixture(t, 0)
	file := putBlob(t, rf.blobs, rf.blobDir, "kicked", t0.Add(-2*time.Hour))
	m := post(file)
	rf.clock.now = t0.Add(10 * day)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- rf.svc.RunRetention(ctx, time.Hour, slog.New(slog.DiscardHandler)) }()
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-done)
	})

	// The first pass runs at start with a retention of 0; then the setting changes and only a kick can
	// run the job again within the hour.
	require.Eventually(t, func() bool { return rf.reads.Load() >= 1 }, 5*time.Second, 5*time.Millisecond)
	rf.days.Store(1)
	rf.svc.KickRetention()
	rf.svc.KickRetention() // a second kick while one is pending does not block
	require.Eventually(t, func() bool {
		status, _ := rf.status(t, m.Attachments[0].ID)
		return status == string(dto.AttachmentStatusExpired)
	}, 5*time.Second, 10*time.Millisecond)
}
