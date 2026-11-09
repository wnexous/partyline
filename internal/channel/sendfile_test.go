package channel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

// sentRequest is a request of the agent API a recorder saw.
type sentRequest struct {
	method, path, contentType string
	length                    int64
}

// fault is what a recorder does to a chunk.
type fault int

const (
	// cutChunk sends half of the chunk's body, then fails the request as a dropped connection does.
	cutChunk fault = iota + 1
	// loseAnswer lets the chunk reach the server, then fails the request as if its answer was lost.
	loseAnswer
)

// recorder is an HTTP transport that records the agent API requests and injects faults into the
// chunks of upload sessions, by their order (1 is the first PUT).
type recorder struct {
	base http.RoundTripper
	// faults maps the number of a chunk request to its fault.
	faults map[int]fault
	// noUploads answers 404 to the creation of an upload session, as a server without them does.
	noUploads bool

	mu   sync.Mutex
	reqs []sentRequest
	puts int
}

func newRecorder() *recorder {
	return &recorder{base: http.DefaultTransport.(*http.Transport).Clone(), faults: map[int]fault{}}
}

func (r *recorder) client() *http.Client { return &http.Client{Transport: r} }

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.reqs = append(r.reqs, sentRequest{method: req.Method, path: req.URL.Path, contentType: req.Header.Get("Content-Type"), length: req.ContentLength})
	var f fault
	if req.Method == http.MethodPut && strings.HasPrefix(req.URL.Path, "/api/v1/agent/uploads/") {
		r.puts++
		f = r.faults[r.puts]
	}
	noUploads := r.noUploads && req.Method == http.MethodPost && req.URL.Path == "/api/v1/agent/uploads"
	r.mu.Unlock()
	switch {
	case noUploads:
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return &http.Response{
			StatusCode: http.StatusNotFound, Header: http.Header{"Content-Type": {"application/json"}}, Request: req,
			Body: io.NopCloser(strings.NewReader(`{"error":{"code":"not_found","message":"not found"}}`)),
		}, nil
	case f == cutChunk:
		req.Body = &cutBody{r: req.Body, left: req.ContentLength / 2}
		req.GetBody = nil // a dropped connection is not replayed by the transport
		resp, err := r.base.RoundTrip(req)
		if err == nil {
			_ = resp.Body.Close()
			return nil, errors.New("connection dropped")
		}
		return nil, err
	case f == loseAnswer:
		resp, err := r.base.RoundTrip(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
		return nil, errors.New("answer lost")
	}
	return r.base.RoundTrip(req)
}

// sent returns the requests of the agent API whose path holds part.
func (r *recorder) sent(part string) []sentRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []sentRequest
	for _, q := range r.reqs {
		if strings.Contains(q.path, part) {
			out = append(out, q)
		}
	}
	return out
}

// cutBody gives left bytes of r, then fails.
type cutBody struct {
	r    io.ReadCloser
	left int64
}

func (c *cutBody) Read(p []byte) (int, error) {
	if c.left <= 0 {
		return 0, errors.New("cut")
	}
	if int64(len(p)) > c.left {
		p = p[:c.left]
	}
	n, err := c.r.Read(p)
	c.left -= int64(n)
	return n, err
}

func (c *cutBody) Close() error { return c.r.Close() }

// randomFile writes size pseudo-random bytes to a new file named name, a block at a time, and returns
// its path and its sha256.
func randomFile(t *testing.T, name string, size int64) (string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	f, err := os.Create(path)
	require.NoError(t, err)
	h := sha256.New()
	rng := rand.New(rand.NewPCG(uint64(size), 7))
	block := make([]byte, 64<<10)
	for left := size; left > 0; {
		n := min(int64(len(block)), left)
		for i := range block[:n] {
			block[i] = byte(rng.Uint32())
		}
		_, err := f.Write(block[:n])
		require.NoError(t, err)
		h.Write(block[:n])
		left -= n
	}
	require.NoError(t, f.Close())
	return path, hex.EncodeToString(h.Sum(nil))
}

// fileSHA256 hashes a file as a stream.
func fileSHA256(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path) // #nosec G304 -- a test file
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	h := sha256.New()
	_, err = io.Copy(h, f)
	require.NoError(t, err)
	return hex.EncodeToString(h.Sum(nil))
}

// requireDelivered checks that the newest message of the room is the agent's with the caption and
// the one attachment named name, and that downloading it gives back the bytes of sha.
func requireDelivered(t *testing.T, e *chanEnv, s *session, text, name string, size int64, sha string) dto.Message {
	t.Helper()
	stored := e.messages(e.room.ID)[0]
	require.Equal(t, "sent (message_id "+stored.ID+")", text)
	require.Equal(t, e.agentID, stored.AuthorID)
	require.Len(t, stored.Attachments, 1)
	require.Equal(t, []any{name, size}, []any{stored.Attachments[0].Name, stored.Attachments[0].Size})
	path, isErr := s.call("download_attachment", toolArgs("attachment_id", stored.Attachments[0].ID))
	require.False(t, isErr, path)
	require.Equal(t, sha, fileSHA256(t, path), "the attachment is the file, byte for byte")
	return stored
}

// LF-02: a file over the size of one multipart post goes to the room through an upload session, in
// chunks of the size the server gives (a lowered PARTYLINE_MAX_UPLOAD_MB), streamed from the file:
// the room gets one attachment, byte-identical, with the caption and reply_to; no multipart post is
// tried, and the channel holds no more than a few chunks' worth of memory. The tools-only server (pi,
// OpenCode) runs the same tool.
func TestSendFileGoesUpInChunks(t *testing.T) {
	for _, toolsOnly := range []bool{false, true} {
		t.Run("tools-only="+strconv.FormatBool(toolsOnly), func(t *testing.T) {
			e := newChanEnv(t, testkit.WithEnv(map[string]string{"PARTYLINE_MAX_UPLOAD_MB": "1"}))
			rec := newRecorder()
			o := e.options()
			o.HTTPClient = rec.client()
			var s *session
			if toolsOnly {
				o.ToolsOnly = true
				s = startSession(t, o)
				s.handshake()
			} else {
				s = e.connected(o)
			}
			question := e.post("send the recording")
			size := int64(multipartMaxBytes) + 1<<20 + 4321
			path, sha := randomFile(t, "recording.bin", size)

			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			text, isErr := s.call("send_file", toolArgs("room", "Support", "path", path, "caption", "the recording", "reply_to", question.ID))
			runtime.ReadMemStats(&after)
			require.False(t, isErr, text)
			// The server and the channel run in this process: together they allocate a fraction of the
			// file, where a body built in memory would allocate it whole at least once.
			require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(size/2), "allocated while sending")

			stored := requireDelivered(t, e, s, text, "recording.bin", size, sha)
			require.Equal(t, []string{"the recording", question.ID}, []string{stored.Text, stored.ReplyTo})

			require.Len(t, rec.sent("/api/v1/agent/uploads"), 1+10, "one session and ten chunks of 1 MB")
			for _, put := range rec.sent("/api/v1/agent/uploads/") {
				require.Equal(t, http.MethodPut, put.method)
				require.LessOrEqual(t, put.length, int64(1<<20))
				require.Positive(t, put.length, "every chunk has its exact length")
			}
			posts := rec.sent("/messages")
			require.Len(t, posts, 1)
			require.Equal(t, "application/json", posts[0].contentType, "the post attaches the upload, it carries no file")
		})
	}
}

// LF-02: a chunk whose connection drops half way is sent again whole, and a chunk whose answer was lost
// after it arrived is not sent twice: the channel asks the server where the upload stands and goes on
// from there. The room gets the file byte for byte.
func TestSendFileResumesAnInterruptedChunk(t *testing.T) {
	e := newChanEnv(t, testkit.WithEnv(map[string]string{"PARTYLINE_MAX_UPLOAD_MB": "1"}))
	rec := newRecorder()
	rec.faults[2] = cutChunk   // the second chunk: dropped half way
	rec.faults[3] = loseAnswer // its retry arrives, but its answer is lost
	rec.faults[6] = loseAnswer // a later chunk, too
	o := e.options()
	o.HTTPClient = rec.client()
	s := e.connected(o)
	size := int64(multipartMaxBytes) + 3<<19
	path, sha := randomFile(t, "video.mp4", size)

	text, isErr := s.call("send_file", toolArgs("room", "Support", "path", path))
	require.False(t, isErr, text)
	requireDelivered(t, e, s, text, "video.mp4", size, sha)

	gets := 0
	for _, q := range rec.sent("/api/v1/agent/uploads/") {
		if q.method == http.MethodGet {
			gets++
		}
	}
	require.Equal(t, 3, gets, "each failure asks where the upload stands")
	// Ten chunks, and one more for the chunk that was cut: the chunks whose answers were lost were
	// not sent again.
	require.Len(t, rec.sent("/api/v1/agent/uploads/"), 10+1+gets)
}

// LF-02: a chunk that keeps failing stops the upload after three retries, and the tool says how far it
// went; nothing is posted.
func TestSendFileGivesUpAfterThreeRetries(t *testing.T) {
	e := newChanEnv(t, testkit.WithEnv(map[string]string{"PARTYLINE_MAX_UPLOAD_MB": "1"}))
	rec := newRecorder()
	for i := 2; i <= 5; i++ {
		rec.faults[i] = cutChunk
	}
	o := e.options()
	o.HTTPClient = rec.client()
	s := e.connected(o)
	before := len(e.messages(e.room.ID))
	path, _ := randomFile(t, "archive.zip", int64(multipartMaxBytes)+1)

	text, isErr := s.call("send_file", toolArgs("room", "Support", "path", path))
	require.True(t, isErr)
	require.Contains(t, text, "archive.zip was not sent: its upload stopped at 1 MB of 8 MB: a chunk failed 4 times")
	require.Equal(t, 5, rec.puts, "the first chunk, then one chunk sent four times")
	require.Len(t, e.messages(e.room.ID), before)
	requireUploadsDeleted(t, e, rec, 1)
}

// requireUploadsDeleted checks that the channel deleted the upload sessions, sending at least n
// deletes, and that the server holds none any more.
func requireUploadsDeleted(t *testing.T, e *chanEnv, rec *recorder, n int) {
	t.Helper()
	deletes := 0
	for _, q := range rec.sent("/api/v1/agent/uploads/") {
		if q.method == http.MethodDelete {
			deletes++
		}
	}
	require.GreaterOrEqual(t, deletes, n, "a delete is sent again while the failed chunk is still being written")
	ids, err := e.env.App.Store.R().ListUploadIDs(context.Background())
	require.NoError(t, err)
	require.Empty(t, ids, "the upload that failed holds no place on the server")
}

// LF-02: an upload whose post fails (a room the AI is not in) is deleted; the tool says why it failed.
func TestSendFileDeletesTheUploadWhenThePostFails(t *testing.T) {
	e := newChanEnv(t)
	rec := newRecorder()
	o := e.options()
	o.HTTPClient = rec.client()
	s := e.connected(o)
	path, _ := randomFile(t, "notes.bin", int64(multipartMaxBytes)+1)

	text, isErr := s.call("send_file", toolArgs("room", "Elsewhere", "path", path))
	require.True(t, isErr)
	require.Contains(t, text, "Elsewhere")
	require.Len(t, rec.sent("/messages"), 1)
	requireUploadsDeleted(t, e, rec, 1)
}

// LF-02: a small file stays one multipart post, streamed with its exact length; no upload session.
func TestSendFileSmallFileIsOneMultipartPost(t *testing.T) {
	e := newChanEnv(t)
	rec := newRecorder()
	o := e.options()
	o.HTTPClient = rec.client()
	s := e.connected(o)
	size := int64(300 << 10)
	path, sha := randomFile(t, "photo.jpg", size)

	text, isErr := s.call("send_file", toolArgs("room", "Support", "path", path, "caption", "a photo"))
	require.False(t, isErr, text)
	requireDelivered(t, e, s, text, "photo.jpg", size, sha)
	require.Empty(t, rec.sent("/api/v1/agent/uploads"))
	posts := rec.sent("/messages")
	require.Len(t, posts, 1)
	require.True(t, strings.HasPrefix(posts[0].contentType, "multipart/form-data; boundary="), posts[0].contentType)
	require.Greater(t, posts[0].length, size, "the body has its length: the file and its framing")
}

// LF-02: a small file over a server's single-request cap is refused as one post before it is sent, and
// goes up in chunks instead.
func TestSendFileOverTheRequestCapGoesUpInChunks(t *testing.T) {
	e := newChanEnv(t, testkit.WithEnv(map[string]string{"PARTYLINE_MAX_UPLOAD_MB": "1"}))
	rec := newRecorder()
	o := e.options()
	o.HTTPClient = rec.client()
	s := e.connected(o)
	size := int64(3<<20 + 99)
	path, sha := randomFile(t, "scan.pdf", size)

	text, isErr := s.call("send_file", toolArgs("room", "Support", "path", path))
	require.False(t, isErr, text)
	requireDelivered(t, e, s, text, "scan.pdf", size, sha)
	posts := rec.sent("/messages")
	require.Len(t, posts, 2, "the refused post, then the post of the upload")
	require.True(t, strings.HasPrefix(posts[0].contentType, "multipart/form-data"))
	require.Equal(t, "application/json", posts[1].contentType)
	require.Len(t, rec.sent("/api/v1/agent/uploads/"), 4)
}

// LF-02: a server without upload sessions still gets a file of one request as a multipart post.
func TestSendFileWithoutUploadSessionsPostsTheFile(t *testing.T) {
	e := newChanEnv(t)
	rec := newRecorder()
	rec.noUploads = true
	o := e.options()
	o.HTTPClient = rec.client()
	s := e.connected(o)
	size := int64(multipartMaxBytes) + 1
	path, sha := randomFile(t, "slides.pdf", size)

	text, isErr := s.call("send_file", toolArgs("room", "Support", "path", path))
	require.False(t, isErr, text)
	requireDelivered(t, e, s, text, "slides.pdf", size, sha)
	posts := rec.sent("/messages")
	require.Len(t, posts, 1)
	require.True(t, strings.HasPrefix(posts[0].contentType, "multipart/form-data"))
}

// LF-02, LF-03, LF-07: the refusals of an upload session read as what they are: a file over the
// largest one names the limit, and a full disk says that nothing was sent.
func TestSendFileRefusals(t *testing.T) {
	t.Run("file too large", func(t *testing.T) {
		e := newChanEnv(t, testkit.WithEnv(map[string]string{"PARTYLINE_MAX_UPLOAD_MB": "1", "PARTYLINE_MAX_FILE_MB": "2"}))
		s := e.connected(e.options())
		before := len(e.messages(e.room.ID))
		path, _ := randomFile(t, "movie.mkv", 3<<20)
		text, isErr := s.call("send_file", toolArgs("room", "Support", "path", path))
		require.True(t, isErr)
		require.Equal(t, "file_too_large: movie.mkv is 3 MB, and this Partyline server takes files up to 2 MB; nothing was sent: send a smaller file, or split it", text)
		require.Len(t, e.messages(e.room.ID), before)
	})
	t.Run("insufficient storage", func(t *testing.T) {
		e := newChanEnv(t, testkit.WithFreeSpace(func(string) (int64, bool) { return 1 << 20, true }))
		s := e.connected(e.options())
		path, _ := randomFile(t, "backup.tar", int64(multipartMaxBytes)+1)
		text, isErr := s.call("send_file", toolArgs("room", "Support", "path", path))
		require.True(t, isErr)
		require.Equal(t, "insufficient_storage: the Partyline server does not have enough free disk space for backup.tar (8 MB); nothing was sent: send a smaller file, or ask whoever runs the server to free space", text)
	})
}

// LF-02: a rate limit says when to try again, from Retry-After.
func TestFileErrorRateLimited(t *testing.T) {
	err := fileError(&statusError{status: http.StatusTooManyRequests, code: dto.ErrorCodeRateLimited, retryAfter: 90 * time.Second,
		msg: "rate_limited: too many uploads: post or wait for the ones you have before starting another"}, localFile{name: "a.bin"})
	require.EqualError(t, err, "rate_limited: too many uploads: post or wait for the ones you have before starting another; try again in 90 seconds")
	require.Equal(t, "3 hours", humanWait(3*time.Hour))
	require.Equal(t, "5 minutes", humanWait(5*time.Minute))
}

// LF-02, CID-32: a file over one request is refused through a connection before anything is read or
// sent, saying that larger files through a connection come later; a server whose cap is lower refuses
// it from its length, and the tool says the same.
func TestSendFileThroughAConnectionOverTheLimitFailsFast(t *testing.T) {
	t.Run("over the connection limit", func(t *testing.T) {
		c := newConnectionChan(t)
		rec := newRecorder()
		o := c.options()
		o.HTTPClient = rec.client()
		s := c.connected(o)
		path := filepath.Join(t.TempDir(), "huge.mov")
		f, err := os.Create(path)
		require.NoError(t, err)
		require.NoError(t, f.Truncate(120<<20)) // sparse: nothing written, nothing read
		require.NoError(t, f.Close())

		text, isErr := s.call("send_file", toolArgs("connection", c.conn, "chat", c.group.ID, "path", path))
		require.True(t, isErr)
		require.Equal(t, "file too large: huge.mov is 120 MB, over the 95 MB a file sent through a connection can have for now; sending larger files through a connection comes in a later version of Partyline. Nothing was sent: share it in a room with send_file and room instead, or send a smaller file", text)
		require.Empty(t, rec.sent("/connections/"))
		require.Empty(t, rec.sent("/api/v1/agent/uploads"))
	})
	t.Run("over the server's request cap", func(t *testing.T) {
		c := newConnectionChan(t, testkit.WithEnv(map[string]string{"PARTYLINE_MAX_UPLOAD_MB": "1"}))
		s := c.connected(c.options())
		path, _ := randomFile(t, "clip.mp4", 3<<20)
		text, isErr := s.call("send_file", toolArgs("connection", c.conn, "chat", c.group.ID, "path", path))
		require.True(t, isErr)
		require.Contains(t, text, "file too large: clip.mp4 is 3 MB, more than this Partyline server takes in one request; sending larger files through a connection comes in a later version")
		require.Empty(t, c.inst.Sent())
	})
}
