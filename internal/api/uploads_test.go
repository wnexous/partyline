package api_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/app"
	"github.com/wnexous/partyline/internal/testkit"
	"github.com/wnexous/partyline/internal/uploads"
)

const (
	userUploads  = "/api/v1/uploads"
	agentUploads = "/api/v1/agent/uploads"
)

// randomBytes returns n reproducible bytes.
func randomBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.NewChaCha8([32]byte{7}).Read(b)
	return b
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func createUpload(c *testkit.Client, base string, req dto.CreateUploadRequest) dto.Upload {
	c.T.Helper()
	return testkit.Decode[dto.Upload](c.Post(base, req).RequireStatus(http.StatusCreated))
}

func putChunk(c *testkit.Client, base, id string, offset int64, data []byte) *testkit.Response {
	c.T.Helper()
	return c.DoRaw(http.MethodPut, fmt.Sprintf("%s/%s?offset=%d", base, id, offset), "application/octet-stream", bytes.NewReader(data))
}

// uploadInChunks creates an upload of data and sends it in chunks of chunk bytes.
func uploadInChunks(c *testkit.Client, base, name, mimeType string, data []byte, chunk int) dto.Upload {
	c.T.Helper()
	u := createUpload(c, base, dto.CreateUploadRequest{Name: name, Mime: mimeType, Size: int64(len(data))})
	for off := 0; off < len(data); off += chunk {
		end := min(off+chunk, len(data))
		u = testkit.Decode[dto.Upload](putChunk(c, base, u.ID, int64(off), data[off:end]).RequireStatus(http.StatusOK))
		require.Equal(c.T, int64(end), u.Received)
	}
	return u
}

func uploadFile(env *testkit.Env, id string) string {
	return filepath.Join(env.App.Config.DataDir, app.UploadsDir, id)
}

// LF-03: a file goes up in chunks of a few MB, written to disk as they arrive; the last one completes
// the upload with the file's hash, and a message attaches it by id like a multipart file. Its download
// is the same bytes, and the upload is used up by the message.
func TestUploadSessionAttachesAFileSentInChunks(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	room := createRoom(alice, "Media")
	data := randomBytes(3*(2<<20) + 123)

	u := createUpload(alice, userUploads, dto.CreateUploadRequest{Name: "../clip.mp4", Mime: "video/mp4", Size: int64(len(data))})
	require.Equal(t, "clip.mp4", u.Name, "the name keeps no path")
	require.Equal(t, int64(50<<20), u.ChunkSize)
	require.Equal(t, int64(5120<<20), u.MaxSize)
	require.Zero(t, u.Received)
	require.False(t, u.Complete)
	require.WithinDuration(t, time.Now().Add(time.Hour), u.ExpiresAt, time.Minute, "an untouched upload expires soon")

	chunk := 2 << 20
	for off := 0; off < len(data); off += chunk {
		end := min(off+chunk, len(data))
		u = testkit.Decode[dto.Upload](putChunk(alice, userUploads, u.ID, int64(off), data[off:end]).RequireStatus(http.StatusOK))
		require.Equal(t, int64(end), u.Received)
		require.Equal(t, end == len(data), u.Complete)
		if !u.Complete {
			info, err := os.Stat(uploadFile(env, u.ID))
			require.NoError(t, err)
			require.Equal(t, int64(end), info.Size(), "the chunks are on disk as they arrive")
		}
	}
	require.Equal(t, sha256Hex(data), u.Sha256)
	require.NoFileExists(t, uploadFile(env, u.ID), "the finished file moved into the blob store")
	state := testkit.Decode[dto.Upload](alice.Get(userUploads + "/" + u.ID).RequireStatus(http.StatusOK))
	require.True(t, state.Complete)
	require.Equal(t, int64(len(data)), state.Received)

	msg := testkit.Decode[dto.Message](alice.Post("/api/v1/rooms/"+room.ID+"/messages",
		dto.PostMessageRequest{Text: "the clip", UploadIDs: []string{u.ID}}).RequireStatus(http.StatusCreated))
	require.Equal(t, "the clip", msg.Text)
	require.Len(t, msg.Attachments, 1)
	require.Equal(t, dto.Attachment{ID: msg.Attachments[0].ID, Name: "clip.mp4", Mime: "video/mp4", Size: int64(len(data)),
		Status: dto.AttachmentStatusStored}, msg.Attachments[0])

	got := alice.Get("/api/v1/attachments/" + msg.Attachments[0].ID).RequireStatus(http.StatusOK)
	require.True(t, bytes.Equal(data, got.Body), "the download is the uploaded file byte for byte")
	require.Equal(t, `attachment; filename=clip.mp4`, got.Header.Get("Content-Disposition"))

	alice.Get(userUploads+"/"+u.ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	alice.Post("/api/v1/rooms/"+room.ID+"/messages", dto.PostMessageRequest{UploadIDs: []string{u.ID}}).
		RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)

	// A file alone needs no text, and a multipart message attaches uploads after its own files.
	v := uploadInChunks(alice, userUploads, "voice.ogg", "audio/ogg", []byte("ogg bytes"), 4)
	msg = testkit.Decode[dto.Message](postMultipart(alice, room.ID, dto.PostMessageRequest{UploadIDs: []string{v.ID}},
		upload{"note.txt", "text/plain", "hi"}).RequireStatus(http.StatusCreated))
	require.Len(t, msg.Attachments, 2)
	require.Equal(t, "note.txt", msg.Attachments[0].Name)
	require.Equal(t, "voice.ogg", msg.Attachments[1].Name)
}

// LF-03: a chunk that does not start at what the upload received is refused with the offset to resume
// at; nothing it carried is kept, and the client goes on from there. A message cannot attach an upload
// before its last chunk.
func TestUploadResumesAtTheReceivedOffset(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	room := createRoom(alice, "Media")
	data := randomBytes(3000)
	u := createUpload(alice, userUploads, dto.CreateUploadRequest{Name: "a.bin", Size: int64(len(data))})
	require.Equal(t, "application/octet-stream", u.Mime)

	putChunk(alice, userUploads, u.ID, 0, data[:1000]).RequireStatus(http.StatusOK)
	e := putChunk(alice, userUploads, u.ID, 2000, data[2000:]).RequireError(http.StatusConflict, dto.ErrorCodeUploadOffset)
	require.Equal(t, map[string]string{"received": "1000", "complete": "false"}, e.Details)
	e = putChunk(alice, userUploads, u.ID, 0, data[:1000]).RequireError(http.StatusConflict, dto.ErrorCodeUploadOffset)
	require.Equal(t, "1000", e.Details["received"], "a chunk sent twice is not appended twice")
	alice.DoRaw(http.MethodPut, userUploads+"/"+u.ID+"?offset=x", "application/octet-stream", bytes.NewReader(data)).
		RequireError(http.StatusBadRequest, dto.ErrorCodeInvalid)
	putChunk(alice, userUploads, u.ID, 1000, nil).RequireError(http.StatusBadRequest, dto.ErrorCodeInvalid)

	alice.Post("/api/v1/rooms/"+room.ID+"/messages", dto.PostMessageRequest{Text: "early", UploadIDs: []string{u.ID}}).
		RequireError(http.StatusConflict, dto.ErrorCodeUploadIncomplete)

	state := testkit.Decode[dto.Upload](alice.Get(userUploads + "/" + u.ID).RequireStatus(http.StatusOK))
	require.Equal(t, int64(1000), state.Received)
	u = testkit.Decode[dto.Upload](putChunk(alice, userUploads, u.ID, state.Received, data[1000:]).RequireStatus(http.StatusOK))
	require.True(t, u.Complete)
	require.Equal(t, sha256Hex(data), u.Sha256)

	e = putChunk(alice, userUploads, u.ID, 3000, []byte("x")).RequireError(http.StatusConflict, dto.ErrorCodeUploadOffset)
	require.Equal(t, map[string]string{"received": "3000", "complete": "true"}, e.Details)
}

// LF-03: an upload's size is positive and at most PARTYLINE_MAX_FILE_MB.
func TestUploadRefusesBadSizes(t *testing.T) {
	env := testkit.New(t, testkit.WithEnv(map[string]string{"PARTYLINE_MAX_FILE_MB": "2"}))
	alice := env.NewUser("alice")
	e := alice.Post(userUploads, dto.CreateUploadRequest{Name: "big", Size: 2<<20 + 1}).RequireError(http.StatusRequestEntityTooLarge, dto.ErrorCodeFileTooLarge)
	require.Equal(t, map[string]string{"max_size": fmt.Sprint(2 << 20)}, e.Details)
	require.Contains(t, e.Message, "2 MB")
	for _, size := range []int64{0, -1} {
		alice.Post(userUploads, dto.CreateUploadRequest{Name: "x", Size: size}).RequireError(http.StatusBadRequest, dto.ErrorCodeInvalid)
	}
	u := createUpload(alice, userUploads, dto.CreateUploadRequest{Name: "fits", Size: 2 << 20})
	require.Equal(t, int64(2<<20), u.MaxSize)
}

// LF-07: an upload that would leave less than the reserve free on the data disk is refused, counting
// what the unfinished uploads may still write.
func TestUploadRefusesWhenTheDiskWouldFill(t *testing.T) {
	free := int64(3 << 30)
	env := testkit.New(t, testkit.WithFreeSpace(func(string) (int64, bool) { return free, true }))
	alice := env.NewUser("alice")

	first := createUpload(alice, userUploads, dto.CreateUploadRequest{Name: "a", Size: 900 << 20})
	e := alice.Post(userUploads, dto.CreateUploadRequest{Name: "b", Size: 900 << 20}).RequireError(http.StatusInsufficientStorage, dto.ErrorCodeInsufficientStorage)
	require.Contains(t, e.Message, "disk space")

	free = 1 << 30
	alice.Post(userUploads, dto.CreateUploadRequest{Name: "c", Size: 1}).RequireError(http.StatusInsufficientStorage, dto.ErrorCodeInsufficientStorage)
	free = 4 << 30
	createUpload(alice, userUploads, dto.CreateUploadRequest{Name: "b", Size: 900 << 20})
	alice.Get(userUploads + "/" + first.ID).RequireStatus(http.StatusOK)
}

// LF-03: an upload belongs to whoever created it: another user, the AI of its creator or the creator of
// an AI's upload neither reads it, writes to it nor attaches it, and learns nothing about it (404).
func TestUploadsBelongToTheirCreator(t *testing.T) {
	f := newAgentRoom(t)
	bob := f.env.NewUser("bob")
	addToRoom(t, f.env, f.alice, f.room.ID, bob)
	agent := f.rest()

	mine := uploadInChunks(f.alice, userUploads, "a.txt", "text/plain", []byte("alice's"), 100)
	open := createUpload(f.alice, userUploads, dto.CreateUploadRequest{Name: "b.txt", Size: 10})
	ai := uploadInChunks(agent, agentUploads, "c.txt", "text/plain", []byte("the AI's"), 100)

	for _, id := range []string{mine.ID, open.ID} {
		bob.Get(userUploads+"/"+id).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
		putChunk(bob, userUploads, id, 0, []byte("x")).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
		agent.Get(agentUploads+"/"+id).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
		putChunk(agent, agentUploads, id, 0, []byte("x")).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	}
	bob.Post("/api/v1/rooms/"+f.room.ID+"/messages", dto.PostMessageRequest{UploadIDs: []string{mine.ID}}).
		RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	agent.Post(agentRoomPath(f.room.ID, "/messages"), dto.PostMessageRequest{UploadIDs: []string{mine.ID}}).
		RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	f.alice.Get(userUploads+"/"+ai.ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	f.alice.Post("/api/v1/rooms/"+f.room.ID+"/messages", dto.PostMessageRequest{UploadIDs: []string{ai.ID}}).
		RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	f.env.Anon().Post(userUploads, dto.CreateUploadRequest{Name: "x", Size: 1}).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	// An agent token keeps to the agent routes (TOK-03), and a session to the user's.
	f.agent.Post(userUploads, dto.CreateUploadRequest{Name: "x", Size: 1}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	f.alice.Post(agentUploads, dto.CreateUploadRequest{Name: "x", Size: 1}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)

	// Nothing above used them up.
	f.alice.Get(userUploads + "/" + mine.ID).RequireStatus(http.StatusOK)
	agent.Get(agentUploads + "/" + ai.ID).RequireStatus(http.StatusOK)
}

// unsized hides the length of a body, so the client sends it chunked and the server cannot refuse it
// from its Content-Length.
type unsized struct{ r io.Reader }

func (u unsized) Read(p []byte) (int, error) { return u.r.Read(p) }

// LF-03: a chunk holds at most chunk_size bytes, which follows a lower PARTYLINE_MAX_UPLOAD_MB, and ends
// at the size of the file; a refused chunk leaves nothing behind.
func TestUploadRefusesAChunkTooLarge(t *testing.T) {
	env := testkit.New(t, testkit.WithEnv(map[string]string{"PARTYLINE_MAX_UPLOAD_MB": "1"}))
	alice := env.NewUser("alice")
	u := createUpload(alice, userUploads, dto.CreateUploadRequest{Name: "a.bin", Size: 3 << 20})
	require.Equal(t, int64(1<<20), u.ChunkSize)

	over := randomBytes(1<<20 + 1)
	putChunk(alice, userUploads, u.ID, 0, over).RequireError(http.StatusRequestEntityTooLarge, dto.ErrorCodeTooLarge)
	alice.DoRaw(http.MethodPut, userUploads+"/"+u.ID+"?offset=0", "application/octet-stream", unsized{bytes.NewReader(over)}).
		RequireError(http.StatusRequestEntityTooLarge, dto.ErrorCodeTooLarge)
	require.Zero(t, testkit.Decode[dto.Upload](alice.Get(userUploads+"/"+u.ID).RequireStatus(http.StatusOK)).Received)
	info, err := os.Stat(uploadFile(env, u.ID))
	require.NoError(t, err)
	require.Zero(t, info.Size(), "the refused chunk was cut off the file")

	small := createUpload(alice, userUploads, dto.CreateUploadRequest{Name: "b.bin", Size: 10})
	putChunk(alice, userUploads, small.ID, 0, []byte("eleven byte")).RequireError(http.StatusRequestEntityTooLarge, dto.ErrorCodeTooLarge)
	putChunk(alice, userUploads, small.ID, 0, []byte("ten bytes!")).RequireStatus(http.StatusOK)
}

// LF-06: an upload expires 24 hours after it started, finished or not: it is unusable at once, and the
// sweep removes it with its temporary file. The blob of a finished upload stays while the upload holds
// it, and goes with the blob sweep once the upload is gone.
func TestExpiredUploadsAreSwept(t *testing.T) {
	clock := testkit.NewClock(time.Now())
	env := testkit.New(t, testkit.WithClock(clock.Now))
	alice := env.NewUser("alice")
	ctx := context.Background()

	open := createUpload(alice, userUploads, dto.CreateUploadRequest{Name: "a", Size: 10})
	putChunk(alice, userUploads, open.ID, 0, []byte("half")).RequireStatus(http.StatusOK)
	done := uploadInChunks(alice, userUploads, "b", "text/plain", []byte("finished but never posted"), 100)
	require.FileExists(t, uploadFile(env, open.ID))

	clock.Advance(2 * time.Hour)
	_, err := env.App.Rooms.Cleanup(ctx)
	require.NoError(t, err)
	f, err := env.App.Blobs.Open(done.Sha256)
	require.NoError(t, err, "a finished upload keeps its blob")
	_ = f.Close()

	clock.Advance(22*time.Hour + time.Minute)
	alice.Get(userUploads+"/"+open.ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	putChunk(alice, userUploads, open.ID, 4, []byte("rest!!")).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)

	n, err := env.App.Uploads.Sweep(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(2), n)
	require.NoFileExists(t, uploadFile(env, open.ID))
	ids, err := env.App.Store.R().ListUploadIDs(ctx)
	require.NoError(t, err)
	require.Empty(t, ids)

	_, err = env.App.Rooms.Cleanup(ctx)
	require.NoError(t, err)
	_, err = env.App.Blobs.Open(done.Sha256)
	require.Error(t, err, "the blob no message used went with the blob sweep")
}

// LF-06: at start the server removes the temporary files no upload owns; an upload in progress keeps
// its file and goes on.
func TestStartupRemovesOrphanUploadFiles(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	data := []byte("0123456789")
	u := createUpload(alice, userUploads, dto.CreateUploadRequest{Name: "a", Size: int64(len(data))})
	putChunk(alice, userUploads, u.ID, 0, data[:4]).RequireStatus(http.StatusOK)
	orphan := uploadFile(env, "019a0000-0000-7000-8000-000000000000")
	require.NoError(t, os.WriteFile(orphan, []byte("left by a crash"), 0o600))

	env.Restart()
	require.NoFileExists(t, orphan)
	require.FileExists(t, uploadFile(env, u.ID))
	u = testkit.Decode[dto.Upload](putChunk(alice, userUploads, u.ID, 4, data[4:]).RequireStatus(http.StatusOK))
	require.True(t, u.Complete)
	require.Equal(t, sha256Hex(data), u.Sha256)
}

// LF-08: an attachment streams with ranges, on the web route and on the agent's: a range is 206 with
// those bytes, and If-Range with the file's ETag resumes a broken download while a stale one gets
// the whole file.
func TestAttachmentDownloadsServeRanges(t *testing.T) {
	f := newAgentRoom(t)
	data := randomBytes(64 << 10)
	u := uploadInChunks(f.alice, userUploads, "movie.mp4", "video/mp4", data, 20<<10)
	msg := testkit.Decode[dto.Message](f.alice.Post("/api/v1/rooms/"+f.room.ID+"/messages",
		dto.PostMessageRequest{UploadIDs: []string{u.ID}}).RequireStatus(http.StatusCreated))
	id := msg.Attachments[0].ID

	full := f.alice.Get("/api/v1/attachments/" + id).RequireStatus(http.StatusOK)
	etag := full.Header.Get("ETag")
	require.Equal(t, `"`+u.Sha256+`"`, etag)
	require.Equal(t, "bytes", full.Header.Get("Accept-Ranges"))

	for path, c := range map[string]*testkit.Client{"/api/v1/attachments/": f.alice, "/api/v1/agent/attachments/": f.rest()} {
		part := c.WithHeader("Range", "bytes=1000-1999").Get(path + id).RequireStatus(http.StatusPartialContent)
		require.Equal(t, data[1000:2000], part.Body, path)
		require.Equal(t, fmt.Sprintf("bytes 1000-1999/%d", len(data)), part.Header.Get("Content-Range"))
		require.Equal(t, `attachment; filename=movie.mp4`, part.Header.Get("Content-Disposition"))
		require.Equal(t, "application/octet-stream", part.Header.Get("Content-Type"))

		tail := c.WithHeader("Range", "bytes=60000-").WithHeader("If-Range", etag).Get(path + id).RequireStatus(http.StatusPartialContent)
		require.Equal(t, data[60000:], tail.Body)
		stale := c.WithHeader("Range", "bytes=60000-").WithHeader("If-Range", `"other"`).Get(path + id).RequireStatus(http.StatusOK)
		require.True(t, bytes.Equal(data, stale.Body))
	}
	f.env.NewUser("carol").WithHeader("Range", "bytes=0-9").Get("/api/v1/attachments/"+id).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
}

// LF-03: an AI's token uploads in chunks on the agent routes and posts the file to a room; through a
// connection upload_ids are refused until the sends stream files (LF-04).
func TestAgentUploadsAFileToARoom(t *testing.T) {
	f := newAgentRoom(t)
	agent := f.rest()
	data := randomBytes(5 << 20)
	u := uploadInChunks(agent, agentUploads, "report.pdf", "application/pdf", data, 2<<20)
	require.True(t, u.Complete)
	require.Equal(t, u.ID, testkit.Decode[dto.Upload](agent.Get(agentUploads+"/"+u.ID).RequireStatus(http.StatusOK)).ID)

	msg := testkit.Decode[dto.Message](agent.Post(agentRoomPath(f.room.ID, "/messages"),
		dto.PostMessageRequest{Text: "here it is", UploadIDs: []string{u.ID}}).RequireStatus(http.StatusCreated))
	require.Len(t, msg.Attachments, 1)
	require.Equal(t, "report.pdf", msg.Attachments[0].Name)
	require.Equal(t, int64(len(data)), msg.Attachments[0].Size)
	got := f.alice.Get("/api/v1/attachments/" + msg.Attachments[0].ID).RequireStatus(http.StatusOK)
	require.True(t, bytes.Equal(data, got.Body))

	other := uploadInChunks(agent, agentUploads, "b.txt", "text/plain", []byte("b"), 10)
	e := agent.Post("/api/v1/agent/connections/whatever/chats/someone/messages",
		dto.SendToChatRequest{Text: "file", UploadIDs: []string{other.ID}}).RequireError(http.StatusBadRequest, dto.ErrorCodeInvalid)
	require.Contains(t, e.Message, "upload_ids")
	agent.Get(agentUploads + "/" + other.ID).RequireStatus(http.StatusOK)
}

// LF-03: while a chunk of an upload is being written, another chunk of it is refused, so two writers
// never interleave their bytes; the first one completes the upload.
func TestUploadTakesOneChunkAtATime(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	u := createUpload(alice, userUploads, dto.CreateUploadRequest{Name: "a", Size: 8})
	owner := uploads.Owner{Kind: uploads.OwnerUser, ID: alice.User.ID}

	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		_, err := env.App.Uploads.Write(context.Background(), owner, u.ID, 0, pr)
		done <- err
	}()
	_, err := pw.Write([]byte("abcd")) // returns once the writer read it: the chunk is in progress
	require.NoError(t, err)
	putChunk(alice, userUploads, u.ID, 0, []byte("12345678")).RequireError(http.StatusConflict, dto.ErrorCodeUploadBusy)
	_, err = pw.Write([]byte("efgh"))
	require.NoError(t, err)
	require.NoError(t, pw.Close())
	require.NoError(t, <-done)

	state := testkit.Decode[dto.Upload](alice.Get(userUploads + "/" + u.ID).RequireStatus(http.StatusOK))
	require.True(t, state.Complete)
	require.Equal(t, sha256Hex([]byte("abcdefgh")), state.Sha256)
}

// LF-03, LF-07: the limits of a new upload hold against concurrent creates: of many at once for one
// owner exactly MaxOpen succeed, and creates that together exceed the free space over the reserve
// are refused past what fits.
func TestConcurrentUploadCreatesKeepTheLimits(t *testing.T) {
	const fits = 5
	env := testkit.New(t, testkit.WithFreeSpace(func(string) (int64, bool) {
		return 2048<<20 + fits*(100<<20) + 1<<20, true // the reserve, five files of 100 MB and a little
	}))
	alice, bob := env.NewUser("alice"), env.NewUser("bob")

	statuses := func(c *testkit.Client, n int, size int64) map[int]int {
		out := make(chan int, n)
		var wg sync.WaitGroup
		for range n {
			wg.Go(func() {
				out <- c.Post(userUploads, dto.CreateUploadRequest{Name: "x", Size: size}).Status
			})
		}
		wg.Wait()
		close(out)
		counts := map[int]int{}
		for status := range out {
			counts[status]++
		}
		return counts
	}
	require.Equal(t, map[int]int{http.StatusCreated: uploads.MaxOpen, http.StatusTooManyRequests: 24 - uploads.MaxOpen},
		statuses(alice, 24, 1))
	require.Equal(t, map[int]int{http.StatusCreated: fits, http.StatusInsufficientStorage: 12 - fits},
		statuses(bob, 12, 100<<20))
}

// LF-06: an upload expires an hour after it was created when no chunk came, and each chunk moves its
// expiry 24 hours past that chunk, so a slow upload never expires midway.
func TestUploadExpiryFollowsItsChunks(t *testing.T) {
	clock := testkit.NewClock(time.Now())
	env := testkit.New(t, testkit.WithClock(clock.Now))
	alice := env.NewUser("alice")
	untouched := createUpload(alice, userUploads, dto.CreateUploadRequest{Name: "a", Size: 10})
	slow := createUpload(alice, userUploads, dto.CreateUploadRequest{Name: "b", Size: 10})
	slow = testkit.Decode[dto.Upload](putChunk(alice, userUploads, slow.ID, 0, []byte("12")).RequireStatus(http.StatusOK))
	require.WithinDuration(t, clock.Now().Add(24*time.Hour), slow.ExpiresAt, time.Second)

	clock.Advance(time.Hour + time.Minute)
	alice.Get(userUploads+"/"+untouched.ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	n, err := env.App.Uploads.Sweep(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(1), n, "only the untouched upload went")

	for i := int64(1); i <= 4; i++ { // a chunk every 22 hours, over almost four days
		clock.Advance(22 * time.Hour)
		putChunk(alice, userUploads, slow.ID, 2*i, []byte("34")).RequireStatus(http.StatusOK)
	}
	slow = testkit.Decode[dto.Upload](alice.Get(userUploads + "/" + slow.ID).RequireStatus(http.StatusOK))
	require.True(t, slow.Complete)
	require.WithinDuration(t, clock.Now().Add(24*time.Hour), slow.ExpiresAt, time.Second)
}

// LF-07: one account cannot claim the disk for everyone: the uploads it holds, finished or not, count
// against 16 uploads and against the bytes of the largest file, until a message uses them.
func TestUploadsOfOneOwnerAreBounded(t *testing.T) {
	env := testkit.New(t, testkit.WithEnv(map[string]string{"PARTYLINE_MAX_FILE_MB": "2"}))
	alice, bob := env.NewUser("alice"), env.NewUser("bob")
	room := createRoom(alice, "Media")

	big := createUpload(alice, userUploads, dto.CreateUploadRequest{Name: "big", Size: 3 << 19}) // 1.5 MB
	alice.Post(userUploads, dto.CreateUploadRequest{Name: "more", Size: 1 << 20}).RequireError(http.StatusTooManyRequests, dto.ErrorCodeRateLimited)
	createUpload(bob, userUploads, dto.CreateUploadRequest{Name: "bob's", Size: 1 << 20}) // others are not held back
	data := randomBytes(3 << 19)
	putChunk(alice, userUploads, big.ID, 0, data).RequireStatus(http.StatusOK)
	alice.Post(userUploads, dto.CreateUploadRequest{Name: "more", Size: 1 << 20}).RequireError(http.StatusTooManyRequests, dto.ErrorCodeRateLimited)
	alice.Post("/api/v1/rooms/"+room.ID+"/messages", dto.PostMessageRequest{UploadIDs: []string{big.ID}}).RequireStatus(http.StatusCreated)
	createUpload(alice, userUploads, dto.CreateUploadRequest{Name: "more", Size: 1 << 20})

	// Finished uploads no message used count among the 16.
	var finished []string
	for len(finished) < uploads.MaxOpen-1 {
		finished = append(finished, uploadInChunks(bob, userUploads, "f", "text/plain", []byte("x"), 1).ID)
	}
	bob.Post(userUploads, dto.CreateUploadRequest{Name: "17th", Size: 1}).RequireError(http.StatusTooManyRequests, dto.ErrorCodeRateLimited)
	bob.Post("/api/v1/rooms/"+createRoom(bob, "Mine").ID+"/messages", dto.PostMessageRequest{UploadIDs: finished[:1]}).RequireStatus(http.StatusCreated)
	createUpload(bob, userUploads, dto.CreateUploadRequest{Name: "17th", Size: 1})
}
