package whatsapp

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"testing/iotest"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/attachments"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
)

// readContent reads what mediaContent finds in answer, through r (iotest readers split it).
func readContent(t *testing.T, r io.Reader) (string, error) {
	t.Helper()
	media, err := mediaContent(r, connectors.MaxJSONResponse)
	if err != nil {
		return "", err
	}
	data, err := io.ReadAll(media)
	return string(data), err
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// LF-04: the media of a /message/downloadmedia answer is found where evolution-go puts it, whatever the
// JSON around it, and decoded as it is read: a value split across reads, the escaped slashes JSON may put
// in base64, a data: URL prefix, and members of every kind before it.
func TestMediaContentFindsTheBase64(t *testing.T) {
	content := "synthetic media bytes / with \x00 binary \xff and more to make padding vary"
	enc := b64(content)
	escaped := strings.ReplaceAll(enc, "/", `\/`)
	require.NotEqual(t, enc, escaped, "the content has slashes in its base64")
	for name, answer := range map[string]string{
		"data is the string":        `{"data":"` + enc + `"}`,
		"data.base64":               `{"data":{"base64":"` + enc + `"}}`,
		"escaped slashes":           `{"data":{"base64":"` + escaped + `"}}`,
		"a data URL":                `{"data":"data:image/png;base64,` + enc + `"}`,
		"a data URL, escaped":       `{"data":"data:image\/png;base64,` + escaped + `"}`,
		"unicode escapes":           `{"data":"` + strings.ReplaceAll(enc, "A", `\u0041`) + `"}`,
		"members before it":         ` { "message" : "ok \" } {", "n": -1.5e3, "ok": true, "x": null, "list": [1, {"a": [2, "]"]}], "data" : { "mimetype": "image/png", "size": 12, "base64" : "` + enc + `" } , "after": 1 }`,
		"members after it are not":  `{"data":"` + enc + `", "broken": `,
		"white space":               "\n{\r\n\t\"data\"\t:\n\"" + enc + "\"\n}\n",
		"line breaks in the base64": `{"data":"` + enc[:8] + `\n` + enc[8:16] + `\r\n` + enc[16:] + `"}`,
	} {
		for how, r := range map[string]func(io.Reader) io.Reader{
			"whole": func(r io.Reader) io.Reader { return r }, "one byte at a time": iotest.OneByteReader, "halves": iotest.HalfReader,
		} {
			got, err := readContent(t, r(strings.NewReader(answer)))
			require.NoError(t, err, name+", "+how)
			require.Equal(t, content, got, name+", "+how)
		}
	}
}

// LF-04: an answer without the media, with an empty one, or that is no JSON is refused at once; corrupt
// base64 fails the read; a cut answer is an error, not a shorter file.
func TestMediaContentRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		answer string
		want   error
	}{
		"no data":                   {`{"message":"success"}`, errNoContent},
		"data without media":        {`{"data":{"mimetype":"image/png"}}`, errNoContent},
		"base64 not a string":       {`{"data":{"base64":12}}`, errNoContent},
		"data null":                 {`{"data":null}`, errNoContent},
		"empty data":                {`{"data":""}`, errNoContent},
		"empty base64":              {`{"data":{"base64":""}}`, errNoContent},
		"not an object":             {`["data"]`, errInvalidJSON},
		"empty body":                {``, errInvalidJSON},
		"cut before it":             {`{"message":"succ`, errInvalidJSON},
		"a data URL without base64": {`{"data":"data:image/png,` + b64("x") + `"}`, errInvalidBase64},
	} {
		_, err := readContent(t, strings.NewReader(tc.answer))
		require.ErrorIs(t, err, tc.want, name)
	}

	_, err := readContent(t, strings.NewReader(`{"data":"not base64!"}`))
	require.ErrorIs(t, err, errInvalidBase64, "corrupt base64 fails the read")
	_, err = readContent(t, strings.NewReader(`{"data":"`+b64("abcdef")[:4]))
	require.ErrorIs(t, err, io.ErrUnexpectedEOF, "a string that never ends is a cut answer")
	_, err = readContent(t, strings.NewReader(`{"data":"\u00e9"}`))
	require.ErrorIs(t, err, errInvalidBase64, "no base64 holds anything but ASCII")
}

// LF-04: the JSON around the media is bounded: a long answer before it is refused without reading on,
// while the media itself is bounded by the client's cap and the blob store's limit, not by this one.
func TestMediaContentBoundsTheJSONAroundTheMedia(t *testing.T) {
	head := `{"pad":"` + strings.Repeat("p", 100) + `","data":"`
	media, err := mediaContent(strings.NewReader(head+b64(strings.Repeat("m", 1000))+`"}`), int64(len(head)))
	require.NoError(t, err)
	data, err := io.ReadAll(media)
	require.NoError(t, err)
	require.Len(t, data, 1000, "the media is not counted as JSON")

	_, err = mediaContent(strings.NewReader(head+b64("m")+`"}`), int64(len(head)-1))
	require.ErrorIs(t, err, errEnvelope)
}

// LF-04: base64Reader is base64.StdEncoding of what it reads, padding included, for every length and
// however its source splits the bytes.
func TestBase64ReaderEncodes(t *testing.T) {
	data := make([]byte, 3<<15+5)
	_, _ = rand.Read(data)
	for _, n := range []int{0, 1, 2, 3, 4, 47, 3 << 14, 3<<14 + 1, 3<<14 + 2, len(data)} {
		for how, r := range map[string]func(io.Reader) io.Reader{
			"whole": func(r io.Reader) io.Reader { return r }, "one byte at a time": iotest.OneByteReader,
		} {
			got, err := io.ReadAll(newBase64Reader(r(bytes.NewReader(data[:n]))))
			require.NoError(t, err)
			require.Equal(t, base64.StdEncoding.EncodeToString(data[:n]), string(got), "%d bytes, %s", n, how)
			require.Equal(t, base64Len(int64(n)), int64(len(got)))
		}
	}
	_, err := io.ReadAll(newBase64Reader(iotest.ErrReader(errors.New("disk failed"))))
	require.EqualError(t, err, "disk failed")
}

// randomFile writes size random bytes to a file of the test and returns its path and SHA-256.
func randomFile(t *testing.T, size int) (string, [32]byte) {
	t.Helper()
	data := make([]byte, size)
	_, _ = rand.Read(data)
	path := filepath.Join(t.TempDir(), "media.bin")
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path, sha256.Sum256(data)
}

// allocated returns the bytes the process allocated while fn ran.
func allocated(fn func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	fn()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// LF-04: a large media of a message is downloaded from evolution-go and stored as it arrives: the base64
// of the JSON answer is decoded into the blob store as a stream, so the memory allocated meanwhile (the
// fake server's included) is a fraction of the file, where decoding the answer would allocate it whole
// several times over. The blob is the file, byte for byte.
func TestALargeMediaIsDownloadedAsAStream(t *testing.T) {
	const size = 6 << 20
	path, sha := randomFile(t, size)
	data, err := os.ReadFile(path) // #nosec G304 -- the test's own file
	require.NoError(t, err)
	answer := []byte(`{"data":{"mimetype":"video/mp4","base64":"` + strings.ReplaceAll(base64.StdEncoding.EncodeToString(data), "/", `\/`) + `"},"message":"success"}`)
	f := newFakeEvolution(t)
	mux := http.NewServeMux()
	mux.Handle("/", f.srv.Config.Handler)
	mux.HandleFunc("POST /message/downloadmedia", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(answer)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c, err := New(connectors.Config{"base_url": srv.URL, "api_key": globalKey, "instances": []string{testInstance}},
		connectors.Deps{AllowPrivateNetworks: true, UserAgent: userAgent, MaxMediaBytes: 1 << 20, MaxFileBytes: 64 << 20})
	require.NoError(t, err)
	blobs, err := attachments.Open(t.TempDir())
	require.NoError(t, err)
	media := c.media(context.Background(), testInstance, MediaRef{Name: "clip.mp4", Mime: "video/mp4", Size: size, Message: json.RawMessage(`{"videoMessage":{}}`)})

	var stored string
	var n int64
	used := allocated(func() {
		r, err := media.Open(context.Background())
		require.NoError(t, err)
		stored, n, err = blobs.Put(r, 64<<20)
		require.NoError(t, r.Close())
		require.NoError(t, err)
	})
	require.Equal(t, int64(size), n)
	require.Equal(t, hex.EncodeToString(sha[:]), stored, "the blob is the file")
	t.Logf("downloading %d bytes allocated %d bytes", size, used)
	require.Less(t, used, uint64(size/4), "allocated while downloading")

	// Over the largest file the store refuses it, still without holding it.
	used = allocated(func() {
		r, err := media.Open(context.Background())
		require.NoError(t, err)
		_, _, err = blobs.Put(r, size-1)
		require.NoError(t, r.Close())
		require.ErrorIs(t, err, attachments.ErrTooLarge)
	})
	require.Less(t, used, uint64(size/4), "allocated while refusing")
}

// LF-04: a file goes to /send/media as valid JSON whose "url" is the base64 of the file, encoded as the
// request is written, with the exact length of the body; the other fields are the usual ones.
func TestSendMediaStreamsValidJSON(t *testing.T) {
	path, sha := randomFile(t, 1<<20+1)
	var got struct {
		Number, Type, Filename, Caption, URL string
	}
	var length, declared int64
	f := newFakeEvolution(t)
	mux := http.NewServeMux()
	mux.Handle("/", f.srv.Config.Handler)
	mux.HandleFunc("POST /send/media", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		length, declared = int64(len(body)), r.ContentLength
		if json.Unmarshal(body, &got) != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"data":{"Info":{"ID":"SENT-MEDIA"}}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c, err := New(connectors.Config{"base_url": srv.URL, "api_key": globalKey, "instances": []string{testInstance}},
		connectors.Deps{AllowPrivateNetworks: true, UserAgent: userAgent})
	require.NoError(t, err)

	id, err := c.Send(context.Background(), connectors.Outbound{ChatID: ChatID(testInstance, groupJID), Files: []connectors.OutFile{diskFile(t, path, "clip.mp4", "video/mp4")}})
	require.NoError(t, err)
	require.Equal(t, "SENT-MEDIA", id)
	require.Equal(t, []string{groupJID, "video", "clip.mp4", ""}, []string{got.Number, got.Type, got.Filename, got.Caption})
	content, err := base64.StdEncoding.DecodeString(got.URL)
	require.NoError(t, err)
	require.Equal(t, sha, sha256.Sum256(content), "the base64 is the file")
	require.Equal(t, length, declared, "the body has its exact length")
}

// diskFile is an outbound file read from path, as a connection's send reads a blob.
func diskFile(t *testing.T, path, name, mime string) connectors.OutFile {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	return connectors.OutFile{Name: name, Mime: mime, Size: info.Size(), Open: func() (io.ReadCloser, error) { return os.Open(path) }} // #nosec G304 -- the test's own file
}

// LF-04: sending a large file allocates a fraction of it, the fake server's hashing of the body included,
// where building the JSON would allocate its base64 at least twice.
func TestSendMediaHoldsNoFileInMemory(t *testing.T) {
	const size = 8 << 20
	path, _ := randomFile(t, size)
	var received atomic.Int64
	f := newFakeEvolution(t)
	mux := http.NewServeMux()
	mux.Handle("/", f.srv.Config.Handler)
	mux.HandleFunc("POST /send/media", func(w http.ResponseWriter, r *http.Request) {
		n, _ := io.Copy(sha256.New(), r.Body)
		received.Store(n)
		_, _ = w.Write([]byte(`{"data":{"Info":{"ID":"SENT-MEDIA"}}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c, err := New(connectors.Config{"base_url": srv.URL, "api_key": globalKey, "instances": []string{testInstance}},
		connectors.Deps{AllowPrivateNetworks: true, UserAgent: userAgent})
	require.NoError(t, err)
	_, err = c.token(context.Background(), testInstance) // the instance list is fetched before measuring
	require.NoError(t, err)
	file := diskFile(t, path, "clip.mp4", "video/mp4")

	used := allocated(func() {
		_, err = c.Send(context.Background(), connectors.Outbound{ChatID: ChatID(testInstance, groupJID), Files: []connectors.OutFile{file}})
	})
	require.NoError(t, err)
	require.Greater(t, received.Load(), base64Len(size), "the whole file went")
	t.Logf("sending %d bytes allocated %d bytes", size, used)
	require.Less(t, used, uint64(size/4), "allocated while sending")
}

// LF-04: a request the transport sends again (a connection that was closed under it) reads the file again
// from its start, through GetBody; every reader opened is closed.
func TestSendMediaReopensTheFileForARetry(t *testing.T) {
	f := newFakeEvolution(t)
	c := newConnector(t, f)
	var opened, closed atomic.Int32
	file := connectors.OutFile{Name: "a.pdf", Mime: "application/pdf", Size: 9, Open: func() (io.ReadCloser, error) {
		opened.Add(1)
		return &closeCounter{Reader: strings.NewReader("pdf-bytes"), closed: &closed}, nil
	}}
	_, err := c.token(context.Background(), testInstance)
	require.NoError(t, err)
	c.uploads = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		// The first attempt dies after a few bytes; the transport takes a new body and sends it whole.
		_, _ = io.CopyN(io.Discard, r.Body, 10)
		_ = r.Body.Close()
		again, err := r.GetBody()
		if err != nil {
			return nil, err
		}
		defer func() { _ = again.Close() }()
		var body map[string]string
		if err := json.NewDecoder(again).Decode(&body); err != nil {
			return nil, err
		}
		if body["url"] != b64("pdf-bytes") {
			return nil, errors.New("the second body is not the file")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"data":{"Info":{"ID":"SENT-MEDIA"}}}`))}, nil
	})}
	id, err := c.Send(context.Background(), connectors.Outbound{ChatID: ChatID(testInstance, groupJID), Files: []connectors.OutFile{file}})
	require.NoError(t, err)
	require.Equal(t, "SENT-MEDIA", id)
	require.Equal(t, []int32{2, 2}, []int32{opened.Load(), closed.Load()})
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type closeCounter struct {
	io.Reader
	closed *atomic.Int32
}

func (c *closeCounter) Close() error { c.closed.Add(1); return nil }

// slowHandler holds the handling of the first message until release is closed, as the inbound
// pipeline does while it downloads a large media; the rest goes to the recorder.
type slowHandler struct {
	*connectortest.Recorder
	release chan struct{}
	held    chan struct{}
	first   atomic.Bool
}

func (h *slowHandler) Inbound(ctx context.Context, m connectors.Inbound) {
	if h.first.CompareAndSwap(false, true) {
		close(h.held)
		select {
		case <-h.release:
		case <-ctx.Done():
		}
	}
	h.Recorder.Inbound(ctx, m)
}

// LF-04: while a message is being handled (its media downloading for minutes), the socket goes on being
// read: a ping of evolution-go is answered, so the connection does not drop; the messages that came
// meanwhile follow it in order once it is done.
func TestRunReadsTheSocketWhileAMessageIsHandled(t *testing.T) {
	f := newFakeEvolution(t)
	c := newConnector(t, f)
	rec := connectortest.NewRecorder()
	h := &slowHandler{Recorder: rec, release: make(chan struct{}), held: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx, h) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			require.ErrorIs(t, err, context.Canceled)
		case <-time.After(wait):
			t.Error("Run did not return after cancel")
		}
	})
	t.Cleanup(func() {
		select {
		case <-h.release:
		default:
			close(h.release)
		}
	})
	sock := f.socket()
	require.Equal(t, connectors.StatusConnected, rec.NextStatus(t))

	push(t, sock, frame(t, testInstance, groupInfo("FIRST"), `{"conversation":"the video"}`))
	select {
	case <-h.held:
	case <-time.After(wait):
		t.Fatal("the first message was never handled")
	}
	push(t, sock, frame(t, testInstance, groupInfo("SECOND"), `{"conversation":"did it arrive?"}`))
	pctx, pcancel := context.WithTimeout(context.Background(), wait)
	defer pcancel()
	for range 3 { // pongs keep coming for as long as the message is handled
		require.NoError(t, sock.Ping(pctx), "the socket is read while the first message is handled")
		time.Sleep(50 * time.Millisecond)
	}
	require.True(t, rec.NoInbound(), "nothing overtakes the message being handled")

	close(h.release)
	require.Equal(t, "FIRST", rec.NextInbound(t).MessageID)
	require.Equal(t, "SECOND", rec.NextInbound(t).MessageID)
}

// LF-04: when the socket fails while a message is being handled, the frames it had read are still
// handled, in order, before Run returns; none of them is lost to the reconnection.
func TestRunHandlesWhatItReadBeforeTheSocketFailed(t *testing.T) {
	f := newFakeEvolution(t)
	c := newConnector(t, f)
	rec := connectortest.NewRecorder()
	h := &slowHandler{Recorder: rec, release: make(chan struct{}), held: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel) // whatever the test found, Run ends
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx, h) }()
	sock := f.socket()
	t.Cleanup(func() { _ = sock.CloseNow() })
	require.Equal(t, connectors.StatusConnected, rec.NextStatus(t))

	push(t, sock, frame(t, testInstance, groupInfo("FIRST"), `{"conversation":"one"}`))
	select {
	case <-h.held:
	case <-time.After(wait):
		t.Fatal("the first message was never handled")
	}
	push(t, sock, frame(t, testInstance, groupInfo("SECOND"), `{"conversation":"two"}`))
	pctx, pcancel := context.WithTimeout(context.Background(), wait)
	defer pcancel()
	require.NoError(t, sock.Ping(pctx), "the second frame was read") // frames are read in order: SECOND came before the pong
	require.NoError(t, sock.CloseNow())
	select {
	case err := <-done:
		t.Fatalf("Run returned before handling what it read: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(h.release)
	require.Equal(t, "FIRST", rec.NextInbound(t).MessageID)
	require.Equal(t, "SECOND", rec.NextInbound(t).MessageID)
	select {
	case err := <-done:
		require.Error(t, err, "the socket's failure is Run's")
	case <-time.After(wait):
		t.Fatal("Run did not return")
	}
}

// downloadingHandler downloads the media of every message it is given, as the inbound pipeline does,
// and records what the download returned before it passes the message on.
type downloadingHandler struct {
	*connectortest.Recorder
	results chan error
}

func (h *downloadingHandler) Inbound(ctx context.Context, m connectors.Inbound) {
	for _, md := range m.Media {
		r, err := md.Open(ctx)
		if err == nil {
			_, err = io.Copy(io.Discard, r)
			_ = r.Close()
		}
		h.results <- err
	}
	h.Recorder.Inbound(ctx, m)
}

// LF-04: when the socket fails while a media of minutes is downloading, the download is aborted (the
// message still goes on, its media failed) and Run returns at once, so the connection is not left
// without a socket for the rest of the download; the next Run reads what comes next.
func TestASocketThatFailsAbortsTheDownloadAndRunReturnsPromptly(t *testing.T) {
	f := newFakeEvolution(t)
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	mux := http.NewServeMux()
	mux.Handle("/", f.srv.Config.Handler)
	mux.HandleFunc("POST /message/downloadmedia", func(w http.ResponseWriter, r *http.Request) {
		// evolution-go sends the start of a large media, then takes its time.
		_, _ = w.Write([]byte(`{"data":"` + b64("the start of a long video")))
		w.(http.Flusher).Flush()
		started <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c, err := New(connectors.Config{"base_url": srv.URL, "api_key": globalKey, "instances": []string{testInstance}},
		connectors.Deps{AllowPrivateNetworks: true, UserAgent: userAgent})
	require.NoError(t, err)
	rec := connectortest.NewRecorder()
	h := &downloadingHandler{Recorder: rec, results: make(chan error, 4)}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	run := func() (<-chan error, *websocket.Conn) {
		done := make(chan error, 1)
		go func() { done <- c.Run(ctx, h) }()
		sock := f.socket()
		t.Cleanup(func() { _ = sock.CloseNow() })
		require.Equal(t, connectors.StatusConnected, rec.NextStatus(t))
		return done, sock
	}

	done, sock := run()
	push(t, sock, frame(t, testInstance, groupInfo("VIDEO"), `{"videoMessage":{"mimetype":"video/mp4","fileLength":"900000000"}}`))
	select {
	case <-started:
	case <-time.After(wait):
		t.Fatal("the download never started")
	}
	push(t, sock, frame(t, testInstance, groupInfo("AFTER"), `{"conversation":"read before the socket failed"}`))
	pctx, pcancel := context.WithTimeout(context.Background(), wait)
	defer pcancel()
	require.NoError(t, sock.Ping(pctx), "the second frame was read")
	require.NoError(t, sock.CloseNow())
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run waited for the download after its socket failed")
	}
	require.ErrorIs(t, <-h.results, errSocketClosed, "the download was aborted")
	require.Equal(t, "VIDEO", rec.NextInbound(t).MessageID, "the message still went on")
	require.Equal(t, "AFTER", rec.NextInbound(t).MessageID, "and what was read after it, in order")

	_, sock = run()
	push(t, sock, frame(t, testInstance, groupInfo("NEXT"), `{"conversation":"on the new socket"}`))
	require.Equal(t, "NEXT", rec.NextInbound(t).MessageID)
}
