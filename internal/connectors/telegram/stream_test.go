package telegram

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/connectors"
)

// LF-05: a bot sends files of up to 50 MB through the public Bot API, and of up to 2000 MB through a
// self-hosted Bot API server.
func TestTheFileLimitFollowsTheBotAPIServer(t *testing.T) {
	k := Kind()
	require.Equal(t, int64(50<<20), k.MaxFile(connectors.Config{"api_base": DefaultAPIBase}))
	require.Equal(t, int64(50<<20), k.MaxFile(connectors.Config{"api_base": DefaultAPIBase + "/"}))
	require.Equal(t, int64(50<<20), k.MaxFile(connectors.Config{}))
	require.Equal(t, int64(2000<<20), k.MaxFile(connectors.Config{"api_base": "https://bots.example.com"}))
}

// streamServer is a Bot API stand-in whose sendDocument reads the file part as a stream and records its
// hash, its length and the request's declared length.
type streamServer struct {
	srv              *httptest.Server
	sha              atomic.Value
	length, declared atomic.Int64
	fileName, chatID atomic.Value
}

func newStreamServer(t *testing.T) *streamServer {
	s := &streamServer{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/sendDocument") {
			http.NotFound(w, r)
			return
		}
		s.declared.Store(r.ContentLength)
		counted := &countReader{r: r.Body}
		_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		mr := multipart.NewReader(counted, params["boundary"])
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			switch part.FormName() {
			case "document":
				h := sha256.New()
				_, _ = io.Copy(h, part)
				s.sha.Store(string(h.Sum(nil)))
				s.fileName.Store(part.FileName())
			case "chat_id":
				data, _ := io.ReadAll(part)
				s.chatID.Store(string(data))
			}
		}
		_, _ = io.Copy(io.Discard, counted)
		s.length.Store(counted.n)
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":7}}`))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

type countReader struct {
	r io.Reader
	n int64
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func streamConnector(t *testing.T, base string) *Connector {
	t.Helper()
	cfg, errs := Kind().Validate(connectors.Config{"bot_token": botToken, "api_base": base})
	require.Empty(t, errs)
	c, err := New(cfg, connectors.Deps{AllowPrivateNetworks: true, UserAgent: userAgent})
	require.NoError(t, err)
	return c
}

// LF-04: a file goes to sendDocument as a multipart body streamed from disk, with its exact length: the
// memory allocated meanwhile, the fake server's included, is a fraction of the file, and the server gets
// the file byte for byte.
func TestSendFileStreamsTheMultipartBody(t *testing.T) {
	const size = 8 << 20
	data := make([]byte, size)
	_, _ = rand.Read(data)
	want := sha256.Sum256(data)
	path := filepath.Join(t.TempDir(), "clip.bin")
	require.NoError(t, os.WriteFile(path, data, 0o600))
	s := newStreamServer(t)
	c := streamConnector(t, s.srv.URL)
	file := connectors.OutFile{Name: "clip.bin", Mime: "application/octet-stream", Size: size, Open: func() (io.ReadCloser, error) {
		return os.Open(path) // #nosec G304 -- the test's own file
	}}

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	id, err := c.Send(context.Background(), connectors.Outbound{ChatID: "5550001", Files: []connectors.OutFile{file}})
	runtime.ReadMemStats(&after)
	require.NoError(t, err)
	require.Equal(t, "7", id)
	used := after.TotalAlloc - before.TotalAlloc
	t.Logf("sending %d bytes allocated %d bytes", size, used)
	require.Less(t, used, uint64(size/4), "allocated while sending")
	require.Equal(t, string(want[:]), s.sha.Load(), "the server got the file")
	require.Equal(t, []any{"clip.bin", "5550001"}, []any{s.fileName.Load(), s.chatID.Load()})
	require.Equal(t, s.length.Load(), s.declared.Load(), "the body has its exact length")
}

// LF-04: a request the transport sends again reads the file again from its start (GetBody), and every
// reader opened is closed.
func TestSendFileReopensTheFileForARetry(t *testing.T) {
	var opened, closed atomic.Int32
	file := connectors.OutFile{Name: "a.pdf", Mime: "application/pdf", Size: 9, Open: func() (io.ReadCloser, error) {
		opened.Add(1)
		return &closeCounter{Reader: strings.NewReader("pdf-bytes"), closed: &closed}, nil
	}}
	c := streamConnector(t, "https://bots.example.com")
	c.client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		_, _ = io.CopyN(io.Discard, r.Body, 10)
		_ = r.Body.Close()
		again, err := r.GetBody()
		if err != nil {
			return nil, err
		}
		defer func() { _ = again.Close() }()
		body, err := io.ReadAll(again)
		if err != nil || int64(len(body)) != r.ContentLength || !strings.Contains(string(body), "\r\n\r\npdf-bytes\r\n") {
			return nil, errors.New("the second body is not the whole request")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{"message_id":8}}`))}, nil
	})}
	id, err := c.Send(context.Background(), connectors.Outbound{ChatID: "5550001", Files: []connectors.OutFile{file}})
	require.NoError(t, err)
	require.Equal(t, "8", id)
	require.Equal(t, []int32{2, 2}, []int32{opened.Load(), closed.Load()})
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type closeCounter struct {
	io.Reader
	closed *atomic.Int32
}

func (c *closeCounter) Close() error { c.closed.Add(1); return nil }
