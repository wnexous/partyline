package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/channel/mcp"
)

// fakeChannels starts channel processes that are real MCP servers on pipes, one per start, with the tools
// each executable has.
type fakeChannels struct {
	mu    sync.Mutex
	tools map[string][]string // exe → its tools
	fail  map[string]bool     // exe → cannot start
	// stubborn exes keep running after their input ends; mute ones read but never answer.
	stubborn, mute map[string]bool
	starts         []fakeStart
	block          chan struct{} // the "slow" tool waits for it
	running        []*fakeChannel
}

type fakeStart struct {
	exe     string
	resumed bool
}

type fakeChannel struct {
	exe  string
	got  *lineLog
	code int
	stop func()
	// crash ends the process at once: its output closes and nothing it was doing answers.
	crash func()
}

// lineLog keeps the lines a fake channel read.
type lineLog struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	lines []string
}

func (l *lineLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf.Write(p)
	for {
		i := bytes.IndexByte(l.buf.Bytes(), '\n')
		if i < 0 {
			return len(p), nil
		}
		l.lines = append(l.lines, string(l.buf.Next(i+1)))
	}
}

func (l *lineLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.lines...)
}

func newFakeChannels(tools map[string][]string) *fakeChannels {
	return &fakeChannels{tools: tools, fail: map[string]bool{}, stubborn: map[string]bool{}, mute: map[string]bool{}, block: make(chan struct{})}
}

func (f *fakeChannels) start(exe string, resumed bool) (*child, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts = append(f.starts, fakeStart{exe: exe, resumed: resumed})
	if f.fail[exe] {
		return nil, errors.New("cannot start " + exe)
	}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	got := &lineLog{}
	ctx, cancel := context.WithCancel(context.Background())
	input := io.TeeReader(inR, got)
	if f.stubborn[exe] {
		input = &endless{r: input, ctx: ctx}
	}
	if f.mute[exe] {
		go func() { _, _ = io.Copy(got, inR) }()
		input = &endless{r: strings.NewReader(""), ctx: ctx}
	}
	srv := mcp.NewServer(input, outW, mcp.ServerInfo{Name: "partyline", Version: exe})
	for _, name := range f.tools[exe] {
		srv.HandleTool(mcp.ToolDef{Name: name, InputSchema: json.RawMessage(`{"type":"object"}`)},
			func(ctx context.Context, _ json.RawMessage) (mcp.ToolResult, error) {
				if name == "slow" {
					select {
					case <-f.block:
					case <-ctx.Done():
					}
				}
				return mcp.TextResult(name + " from " + exe), nil
			})
	}
	fc := &fakeChannel{exe: exe, got: got}
	fc.stop = func() { cancel(); _ = inR.Close() }
	done := make(chan struct{})
	var once sync.Once
	end := func() { once.Do(func() { _ = outW.Close(); close(done) }) }
	fc.crash = func() { _ = outW.Close(); end(); cancel(); _ = inR.Close() }
	go func() {
		_ = srv.Serve(ctx)
		end()
	}()
	f.running = append(f.running, fc)
	return &child{
		in: inW, out: outR,
		wait: func() int {
			<-done
			f.mu.Lock()
			defer f.mu.Unlock()
			return fc.code
		},
		kill: fc.stop,
	}, nil
}

// endless reads r and, at its end, waits until ctx is done instead of ending: a child that does not end
// when its input closes.
type endless struct {
	r   io.Reader
	ctx context.Context
}

func (e *endless) Read(p []byte) (int, error) {
	n, err := e.r.Read(p)
	if errors.Is(err, io.EOF) && n == 0 {
		<-e.ctx.Done()
		return 0, io.EOF
	}
	return n, err
}

func (f *fakeChannels) startsSoFar() []fakeStart {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeStart(nil), f.starts...)
}

func (f *fakeChannels) channel(i int) *fakeChannel {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running[i]
}

// fakeHarness is the harness side of a supervisor: it writes requests and reads every line it gets.
type fakeHarness struct {
	t     *testing.T
	in    *io.PipeWriter
	lines chan map[string]any
	code  chan int
}

func superviseFake(t *testing.T, f *fakeChannels, exes []string) (*fakeHarness, chan<- string, *lineLog) {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	stderr := &lineLog{}
	s := &supervisor{in: inR, out: outW, stderr: stderr, start: f.start, first: exes[0], exes: exes,
		drain: 300 * time.Millisecond, handshake: 300 * time.Millisecond, probation: 5 * time.Second}
	updates := make(chan string, 1)
	h := &fakeHarness{t: t, in: inW, lines: make(chan map[string]any, 64), code: make(chan int, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		h.code <- s.run(ctx, updates)
		_ = outW.Close()
	}()
	go func() {
		r := bufio.NewReader(outR)
		for {
			line, err := r.ReadBytes('\n')
			if len(line) > 0 {
				var m map[string]any
				require.NoError(t, json.Unmarshal(line, &m), string(line))
				h.lines <- m
			}
			if err != nil {
				close(h.lines)
				return
			}
		}
	}()
	return h, updates, stderr
}

func (h *fakeHarness) send(line string) {
	h.t.Helper()
	_, err := h.in.Write([]byte(line + "\n"))
	require.NoError(h.t, err)
}

func (h *fakeHarness) next() map[string]any {
	h.t.Helper()
	select {
	case m, ok := <-h.lines:
		require.True(h.t, ok, "the supervisor's output ended")
		return m
	case <-time.After(5 * time.Second):
		h.t.Fatal("no line from the supervisor")
		return nil
	}
}

// handshake does what a harness does first and returns the tools it was given.
func (h *fakeHarness) handshake() []string {
	h.t.Helper()
	h.send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	init := h.next()
	require.EqualValues(h.t, 1, init["id"])
	tools := init["result"].(map[string]any)["capabilities"].(map[string]any)["tools"].(map[string]any)
	require.Equal(h.t, true, tools["listChanged"], "the harness must accept tools/list_changed (AU-05)")
	h.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	return h.listTools(2)
}

func (h *fakeHarness) listTools(id int) []string {
	h.t.Helper()
	h.send(`{"jsonrpc":"2.0","id":` + itoa(id) + `,"method":"tools/list"}`)
	res := h.next()
	require.EqualValues(h.t, id, res["id"])
	var names []string
	for _, tool := range res["result"].(map[string]any)["tools"].([]any) {
		names = append(names, tool.(map[string]any)["name"].(string))
	}
	return names
}

func (h *fakeHarness) call(id int, tool string) {
	h.t.Helper()
	h.send(`{"jsonrpc":"2.0","id":` + itoa(id) + `,"method":"tools/call","params":{"name":"` + tool + `"}}`)
}

// answer is the next line that is not a notification.
func (h *fakeHarness) answer() map[string]any {
	h.t.Helper()
	for {
		if m := h.next(); m["method"] == nil {
			return m
		}
	}
}

func resultText(m map[string]any) string {
	return m["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
}

func itoa(i int) string { return strconv.Itoa(i) }

// AU-04, AU-05, AU-06: a handover waits for the call in flight, replays the harness's handshake to the new
// child under the supervisor's own ids, holds what arrives meanwhile for the new child and tells the harness
// that the tools changed. The harness sees one connection throughout.
func TestSupervisorHandsTheHarnessOverToTheUpdatedChannel(t *testing.T) {
	f := newFakeChannels(map[string][]string{"partyline": {"echo", "slow"}})
	h, updates, stderr := superviseFake(t, f, []string{"partyline"})
	require.Equal(t, []string{"echo", "slow"}, h.handshake())

	h.call(3, "slow")
	// The call is waiting in the child before the update comes.
	require.Eventually(t, func() bool {
		return strings.Contains(strings.Join(f.channel(0).got.all(), ""), `"id":3`)
	}, 5*time.Second, 5*time.Millisecond)
	updates <- "v2.0.0"
	time.Sleep(100 * time.Millisecond)
	require.Len(t, f.startsSoFar(), 1, "no handover while a call waits for its answer")

	f.mu.Lock()
	f.tools["partyline"] = []string{"echo", "slow", "extra"} // the updated program has one more tool
	f.mu.Unlock()
	close(f.block)
	answer := h.next()
	require.EqualValues(t, 3, answer["id"])
	require.Equal(t, "slow from partyline", resultText(answer))

	h.call(4, "extra") // held while the handover runs, then answered by the new child
	changed := h.next()
	require.Equal(t, "notifications/tools/list_changed", changed["method"])
	got := h.next()
	require.EqualValues(t, 4, got["id"])
	require.Equal(t, "extra from partyline", resultText(got))
	require.Equal(t, []string{"echo", "slow", "extra"}, h.listTools(5))

	require.Equal(t, []fakeStart{{exe: "partyline"}, {exe: "partyline", resumed: true}}, f.startsSoFar())
	lines := f.channel(1).got.all()
	require.GreaterOrEqual(t, len(lines), 4)
	require.Contains(t, lines[0], `"id":"partyline-supervisor-init-2"`)
	require.Contains(t, lines[0], `"method":"initialize"`)
	require.Contains(t, lines[0], `"protocolVersion":"2025-06-18"`)
	require.Contains(t, lines[1], `notifications/initialized`)
	require.Contains(t, lines[2], `"id":"partyline-supervisor-tools-2"`)
	require.Contains(t, lines[3], `"id":4`)
	require.Contains(t, strings.Join(stderr.all(), ""), "restarting the channel on v2.0.0")

	require.NoError(t, h.in.Close())
	require.Equal(t, 0, <-h.code)
}

// AU-05: a new child with the same tools says nothing about them.
func TestSupervisorSaysNothingWhenTheToolsAreTheSame(t *testing.T) {
	f := newFakeChannels(map[string][]string{"partyline": {"echo"}})
	close(f.block)
	h, updates, _ := superviseFake(t, f, []string{"partyline"})
	h.handshake()

	updates <- "v2.0.0"
	require.Eventually(t, func() bool { return len(f.startsSoFar()) == 2 }, 5*time.Second, 10*time.Millisecond)
	h.call(3, "echo")
	got := h.next()
	require.EqualValues(t, 3, got["id"], "the first line after the handover is the answer, no list_changed")

	require.NoError(t, h.in.Close())
	require.Equal(t, 0, <-h.code)
}

// AU-09: when the updated program cannot start, the supervisor goes back to the one the session started
// with.
func TestSupervisorFallsBackToTheOldProgram(t *testing.T) {
	f := newFakeChannels(map[string][]string{"new": {"echo"}, "old": {"echo"}})
	close(f.block)
	h, updates, stderr := superviseFake(t, f, []string{"new", "old"})
	h.handshake()

	f.mu.Lock()
	f.fail["new"] = true
	f.mu.Unlock()
	updates <- "v2.0.0"
	require.Eventually(t, func() bool { return len(f.startsSoFar()) == 3 }, 5*time.Second, 5*time.Millisecond)
	h.call(3, "echo")
	got := h.next()
	require.EqualValues(t, 3, got["id"])
	require.Equal(t, "echo from old", resultText(got))
	require.Equal(t, []fakeStart{{exe: "new"}, {exe: "new", resumed: true}, {exe: "old", resumed: true}}, f.startsSoFar())
	require.Contains(t, strings.Join(stderr.all(), ""), "going back to the program this session started with")

	require.NoError(t, h.in.Close())
	require.Equal(t, 0, <-h.code)
}

// AU-09: with no program left to run, the supervisor ends as the channel ends without one.
func TestSupervisorEndsWhenNoProgramStarts(t *testing.T) {
	f := newFakeChannels(map[string][]string{"new": {"echo"}})
	close(f.block)
	h, updates, _ := superviseFake(t, f, []string{"new"})
	h.handshake()

	f.mu.Lock()
	f.fail["new"] = true
	f.mu.Unlock()
	updates <- "v2.0.0"
	select {
	case code := <-h.code:
		require.Equal(t, 1, code)
	case <-time.After(5 * time.Second):
		t.Fatal("the supervisor did not end")
	}
}

// AU-04: a channel that ends by itself ends the supervisor with its exit code, as it would end the session
// without one.
func TestSupervisorEndsWithAChannelThatEnds(t *testing.T) {
	f := newFakeChannels(map[string][]string{"partyline": {"echo"}})
	h, _, _ := superviseFake(t, f, []string{"partyline"})
	h.handshake()

	c := f.channel(0)
	f.mu.Lock()
	c.code = 7
	f.mu.Unlock()
	c.stop()
	select {
	case code := <-h.code:
		require.Equal(t, 7, code)
	case <-time.After(5 * time.Second):
		t.Fatal("the supervisor did not end")
	}
}

// AU-04: the end of the harness's input reaches the channel, which ends, and the supervisor with it.
func TestSupervisorEndsWithTheHarness(t *testing.T) {
	f := newFakeChannels(map[string][]string{"partyline": {"echo"}})
	h, _, _ := superviseFake(t, f, []string{"partyline"})
	h.handshake()
	require.NoError(t, h.in.Close())
	select {
	case code := <-h.code:
		require.Equal(t, 0, code)
	case <-time.After(5 * time.Second):
		t.Fatal("the supervisor did not end")
	}
}

func TestDeclareListChangedKeepsEverythingElse(t *testing.T) {
	in := []byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","capabilities":{"tools":{},"experimental":{"claude/channel":{}}},"instructions":"hi"}}` + "\n")
	var m map[string]any
	require.NoError(t, json.Unmarshal(declareListChanged(in), &m))
	res := m["result"].(map[string]any)
	require.Equal(t, "hi", res["instructions"])
	caps := res["capabilities"].(map[string]any)
	require.Equal(t, map[string]any{"listChanged": true}, caps["tools"])
	require.Contains(t, caps, "experimental")

	junk := []byte("not json\n")
	require.Equal(t, junk, declareListChanged(junk))
}

// AU-09: a new program that ends by itself right after the handover is replaced by the one the session
// started with, and the call it left unanswered gets an error instead of no answer.
func TestSupervisorGoesBackWhenTheUpdatedChannelEndsRightAway(t *testing.T) {
	f := newFakeChannels(map[string][]string{"new": {"echo", "slow"}, "old": {"echo"}})
	h, updates, stderr := superviseFake(t, f, []string{"new", "old"})
	h.handshake()

	updates <- "v2.0.0"
	require.Eventually(t, func() bool { return len(f.startsSoFar()) == 2 }, 5*time.Second, 5*time.Millisecond)
	h.call(3, "echo")
	require.Equal(t, "echo from new", resultText(h.next()))
	h.call(4, "slow")
	require.Eventually(t, func() bool {
		return strings.Contains(strings.Join(f.channel(1).got.all(), ""), `"id":4`)
	}, 5*time.Second, 5*time.Millisecond)
	f.channel(1).crash() // the updated program dies while the call runs

	failed := h.next()
	require.EqualValues(t, 4, failed["id"])
	require.Contains(t, failed["error"].(map[string]any)["message"], "call it again")
	h.call(5, "echo")
	got := h.answer() // after the tools/list_changed of the old program's tools
	require.EqualValues(t, 5, got["id"])
	require.Equal(t, "echo from old", resultText(got))
	require.Equal(t, []fakeStart{{exe: "new"}, {exe: "new", resumed: true}, {exe: "old", resumed: true}}, f.startsSoFar())
	require.Contains(t, strings.Join(stderr.all(), ""), "ended right after it started")

	require.NoError(t, h.in.Close())
	require.Equal(t, 0, <-h.code)
}

// AU-04: an old child that does not end once its input closed is killed after the drain bound, and the
// handover goes on.
func TestSupervisorKillsAnOldChannelThatDoesNotEnd(t *testing.T) {
	f := newFakeChannels(map[string][]string{"partyline": {"echo"}})
	close(f.block)
	f.stubborn["partyline"] = true
	h, updates, _ := superviseFake(t, f, []string{"partyline"})
	h.handshake()

	f.mu.Lock()
	f.stubborn["partyline"] = false // the updated program ends as it should
	f.mu.Unlock()
	updates <- "v2.0.0"
	require.Eventually(t, func() bool { return len(f.startsSoFar()) == 2 }, 5*time.Second, 5*time.Millisecond)
	h.call(3, "echo")
	got := h.next()
	require.EqualValues(t, 3, got["id"])
	require.Equal(t, "echo from partyline", resultText(got))

	require.NoError(t, h.in.Close())
	require.Equal(t, 0, <-h.code)
}

// AU-09: a new child that never answers the replayed initialize is killed after the handshake bound and
// the next program takes its place.
func TestSupervisorReplacesAnUpdatedChannelThatDoesNotAnswer(t *testing.T) {
	f := newFakeChannels(map[string][]string{"new": {"echo"}, "old": {"echo"}})
	close(f.block)
	h, updates, stderr := superviseFake(t, f, []string{"new", "old"})
	h.handshake()

	f.mu.Lock()
	f.mute["new"] = true
	f.mu.Unlock()
	updates <- "v2.0.0"
	require.Eventually(t, func() bool { return len(f.startsSoFar()) == 3 }, 5*time.Second, 5*time.Millisecond)
	h.call(3, "echo")
	require.Equal(t, "echo from old", resultText(h.next()))
	require.Contains(t, strings.Join(stderr.all(), ""), "did not answer in time")

	require.NoError(t, h.in.Close())
	require.Equal(t, 0, <-h.code)
}

// AU-04: a harness that uses string ids gets its answers too, across a handover.
func TestSupervisorKeepsStringIDs(t *testing.T) {
	f := newFakeChannels(map[string][]string{"partyline": {"echo"}})
	close(f.block)
	h, updates, _ := superviseFake(t, f, []string{"partyline"})
	h.send(`{"jsonrpc":"2.0","id":"a-1","method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	require.Equal(t, "a-1", h.next()["id"])
	h.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	updates <- "v2.0.0"
	require.Eventually(t, func() bool { return len(f.startsSoFar()) == 2 }, 5*time.Second, 5*time.Millisecond)
	h.send(`{"jsonrpc":"2.0","id":"a-2","method":"tools/call","params":{"name":"echo"}}`)
	got := h.next()
	require.Equal(t, "a-2", got["id"])
	require.Equal(t, "echo from partyline", resultText(got))

	require.NoError(t, h.in.Close())
	require.Equal(t, 0, <-h.code)
}
