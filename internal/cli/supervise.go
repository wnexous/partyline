package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/wnexous/partyline/internal/channel"
)

// The bounds of a handover (AU-04, AU-09): how long the old child may take to end once its input closed,
// how long the new one may take to answer the replayed initialize and the tools list, and how long after
// that a new child that ends by itself still counts as one that did not work.
const (
	drainTimeout     = 10 * time.Second
	handshakeTimeout = 30 * time.Second
	probation        = time.Minute
)

// selfExe is the program this process runs even after its file was replaced: Linux keeps it (AU-09).
const selfExe = "/proc/self/exe"

// listChangedLine tells the harness to list the tools again (AU-05).
var listChangedLine = []byte(`{"jsonrpc":"2.0","method":"notifications/tools/list_changed"}` + "\n")

// child is one channel process the supervisor runs: its stdin, its stdout and its exit code.
type child struct {
	in   io.WriteCloser
	out  io.Reader
	wait func() int
	kill func()
}

// startChild starts a channel process from exe; resumed says it takes over the session of the one before it.
type startChild func(exe string, resumed bool) (*child, error)

// supervisor holds the harness's MCP connection and runs the channel in a child process that it can hand
// over to a new executable (AU-04..AU-06, AU-09).
type supervisor struct {
	in     io.Reader
	out    io.Writer
	stderr io.Writer
	start  startChild
	// first is the program the first child runs: the one this process runs. exes are the programs a new
	// child starts from, in order: the updated executable and, where the system has it, the old one (AU-09).
	first string
	exes  []string
	// drain, handshake and probation are drainTimeout, handshakeTimeout and probation; tests shorten them.
	drain, handshake, probation time.Duration

	cur *child
	gen int
	// initLine and initID are the harness's initialize request, initialized its notifications/initialized.
	initLine, initialized []byte
	initID                string
	initAnswered          bool
	// inflight are the ids of harness requests the child has not answered; toolsLists those that are tools/list.
	inflight   map[string]bool
	toolsLists map[string]bool
	tools      []byte // the tools of the last tools/list answer, compacted
	held       [][]byte
	// phase is where a handover is: idle, draining (the old child's stdin is closed) or handshake (the new
	// child is answering the replayed initialize and tools/list).
	phase     phase
	want      string    // the version to hand over to, "" when none is due
	tried     int       // how many of exes the current handover has tried
	harnessIn bool      // the harness's input is still open
	resumedAt time.Time // when the last handover ended; zero before the first
}

type phase int

const (
	phaseIdle phase = iota
	phaseDraining
	phaseHandshake
)

type childLine struct {
	gen  int
	line []byte
}

type childExit struct {
	gen, code int
}

// superviseChannel runs `partyline channel` with args under a supervisor that follows au (AU-04).
func superviseChannel(ctx context.Context, args []string, io stdio, au *autoUpdate) int {
	env := os.Environ()
	harness := strconv.Itoa(os.Getppid())
	// Every child connects with the same live id, so the server sees one process that replaced its socket
	// and keeps the session's label, start time and role (AU-06, SESS-14).
	live := channel.NewLiveID()
	start := func(exe string, resumed bool) (*child, error) {
		cmd := exec.Command(exe, append([]string{"channel"}, args...)...) // #nosec G204 -- this program, updated
		cmd.Env = append(append([]string{}, env...), supervisedVar+"="+harness, linkLiveVar+"="+live)
		if resumed {
			cmd.Env = append(cmd.Env, resumedVar+"=1", "PARTYLINE_TAKEOVER=0", principalEnvVar+"=0")
		}
		cmd.Stderr = io.stderr
		in, err := cmd.StdinPipe()
		if err != nil {
			return nil, err
		}
		out, err := cmd.StdoutPipe()
		if err != nil {
			return nil, err
		}
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		return &child{
			in: in, out: out,
			wait: func() int {
				_ = cmd.Wait()
				if code := cmd.ProcessState.ExitCode(); code >= 0 {
					return code
				}
				return 1 // ended by a signal
			},
			kill: func() { _ = cmd.Process.Kill() },
		}, nil
	}
	// The first child runs this very program, whatever the file on disk holds now; a handover starts the
	// updated file and falls back to this program where the system keeps it (AU-09).
	first, exes := au.exe, []string{au.exe}
	if _, err := os.Stat(selfExe); err == nil {
		first, exes = selfExe, append(exes, selfExe)
	}
	updates := make(chan string, 1)
	go func() {
		for {
			v := au.run(ctx)
			if v == "" {
				return
			}
			au.current = v
			select {
			case updates <- v:
			case <-ctx.Done():
				return
			}
		}
	}()
	s := &supervisor{in: io.stdin, out: io.stdout, stderr: io.stderr, start: start, first: first, exes: exes,
		drain: drainTimeout, handshake: handshakeTimeout, probation: probation}
	return s.run(ctx, updates)
}

// run serves until the harness's input ends, the child ends by itself or ctx is done, and returns the
// exit code of the process.
func (s *supervisor) run(ctx context.Context, updates <-chan string) int {
	s.inflight, s.toolsLists = map[string]bool{}, map[string]bool{}
	s.harnessIn = true
	done := make(chan struct{})
	defer close(done)

	harnessLines := make(chan []byte)
	harnessEnd := make(chan struct{})
	go func() {
		r := bufio.NewReader(s.in)
		for {
			line, err := r.ReadBytes('\n')
			if len(line) > 0 {
				select {
				case harnessLines <- line:
				case <-done:
					return
				}
			}
			if err != nil {
				close(harnessEnd)
				return
			}
		}
	}()

	lines := make(chan childLine)
	exits := make(chan childExit, 4)
	spawn := func(exe string, resumed bool) error {
		c, err := s.start(exe, resumed)
		if err != nil {
			return err
		}
		s.gen++
		s.cur = c
		gen := s.gen
		go func() {
			r := bufio.NewReader(c.out)
			for {
				line, err := r.ReadBytes('\n')
				// A line cut off by the end of the child (killed mid-write) is dropped: glued to the next
				// child's first line it would break both.
				if len(line) > 0 && line[len(line)-1] == '\n' {
					select {
					case lines <- childLine{gen: gen, line: line}:
					case <-done:
						return
					}
				}
				if err != nil {
					break
				}
			}
			exits <- childExit{gen: gen, code: c.wait()}
		}()
		return nil
	}
	if err := spawn(s.first, false); err != nil {
		fmt.Fprintln(s.stderr, "partyline channel:", err)
		return 1
	}

	// deadline bounds the phase of a handover: the old child's end while draining, the new child's answers
	// during the handshake. Past it the child is killed, and its exit moves the handover on.
	var deadline <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return s.stop(exits, lines)
		case <-harnessEnd:
			harnessEnd = nil
			s.harnessIn = false
			if s.phase != phaseIdle {
				s.cur.kill() // nothing is left to hand over to
				return 0
			}
			_ = s.cur.in.Close()
		case line := <-harnessLines:
			s.fromHarness(line)
		case l := <-lines:
			if l.gen != s.gen {
				continue
			}
			if err := s.fromChild(l.line); err != nil {
				return 1 // the harness is gone
			}
			if s.phase == phaseIdle {
				deadline = nil
			}
		case e := <-exits:
			if e.gen != s.gen {
				continue
			}
			switch {
			case s.phase == phaseDraining:
				s.tried = 0
			case s.phase == phaseHandshake:
				// The new child died before it was ready: the next candidate replaces it.
			case s.onProbation():
				// A new program that ends by itself right after the handover does not work here: the
				// session goes back to the one it started with (AU-09). Calls it had not answered are
				// failed, so the harness never waits for them.
				fmt.Fprintln(s.stderr, "partyline: the updated channel ended right after it started")
				if err := s.failInflight(); err != nil {
					return 1
				}
			default:
				return e.code // the channel ended as it does without a supervisor
			}
			if !s.startNext(spawn) {
				return 1
			}
			deadline = time.After(s.handshake)
		case <-deadline:
			if s.phase == phaseHandshake {
				fmt.Fprintln(s.stderr, "partyline: the updated channel did not answer in time")
			}
			s.cur.kill() // its exit moves the handover on
			deadline = nil
		case v := <-updates:
			s.want = v
		}
		if s.maybeHandOver() {
			deadline = time.After(s.drain)
		}
	}
}

// startNext starts the next candidate of a handover and replays the harness's initialize to it; false when
// none is left (AU-09).
func (s *supervisor) startNext(spawn func(string, bool) error) bool {
	for s.tried < len(s.exes) {
		exe := s.exes[s.tried]
		s.tried++
		if s.tried > 1 {
			fmt.Fprintf(s.stderr, "partyline: going back to the program this session started with\n")
		}
		if err := spawn(exe, true); err != nil {
			fmt.Fprintln(s.stderr, "partyline channel:", err)
			continue
		}
		s.phase = phaseHandshake
		if _, err := s.cur.in.Write(withID(s.initLine, supervisorID("init", s.gen))); err != nil {
			s.cur.kill()
			continue
		}
		return true
	}
	fmt.Fprintln(s.stderr, "partyline channel: no program left to run the channel")
	return false
}

// maybeHandOver starts a handover once one is due and no harness request waits for an answer (AU-04); it
// reports whether it started one.
func (s *supervisor) maybeHandOver() bool {
	if s.want == "" || s.phase != phaseIdle || len(s.inflight) > 0 || !s.harnessIn || !s.initAnswered {
		return false
	}
	fmt.Fprintf(s.stderr, "partyline: restarting the channel on %s\n", s.want)
	s.want = ""
	s.phase = phaseDraining
	_ = s.cur.in.Close()
	return true
}

// onProbation reports whether the current child came from a handover that ended a moment ago and another
// program is left to try (AU-09).
func (s *supervisor) onProbation() bool {
	return !s.resumedAt.IsZero() && s.harnessIn && time.Since(s.resumedAt) < s.probation && s.tried < len(s.exes)
}

// failInflight answers every harness request the ended child left unanswered with an error, so the harness
// never waits for an answer that cannot come.
func (s *supervisor) failInflight() error {
	// A held request never reached the ended child: the next one answers it.
	held := map[string]bool{}
	for _, line := range s.held {
		var m struct {
			ID json.RawMessage `json:"id"`
		}
		if json.Unmarshal(line, &m) == nil && len(m.ID) > 0 {
			held[string(m.ID)] = true
		}
	}
	for id := range s.inflight {
		if held[id] {
			continue
		}
		line := []byte(`{"jsonrpc":"2.0","id":` + id + `,"error":{"code":-32603,"message":"Partyline restarted while this call ran; call it again"}}` + "\n")
		if err := s.write(line); err != nil {
			return err
		}
	}
	for id := range s.inflight {
		if !held[id] {
			delete(s.inflight, id)
			delete(s.toolsLists, id)
		}
	}
	return nil
}

// fromHarness passes a harness line to the child, or holds it while a handover runs.
func (s *supervisor) fromHarness(line []byte) {
	line = bytes.Clone(line)
	var m struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if json.Unmarshal(line, &m) == nil && m.Method != "" {
		switch m.Method {
		case "initialize":
			if s.initLine == nil {
				s.initLine, s.initID = line, string(m.ID)
			}
		case "notifications/initialized":
			s.initialized = line
		}
		if len(m.ID) > 0 {
			s.inflight[string(m.ID)] = true
			if m.Method == "tools/list" {
				s.toolsLists[string(m.ID)] = true
			}
		}
	}
	if s.phase != phaseIdle {
		s.held = append(s.held, line)
		return
	}
	if _, err := s.cur.in.Write(line); err != nil {
		s.held = append(s.held, line) // the child is ending: its exit decides what comes next
	}
}

// fromChild handles a line of the current child: the answers to the supervisor's own requests during a
// handshake, else a line for the harness. It fails only when the harness's output fails.
func (s *supervisor) fromChild(line []byte) error {
	var m struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Result struct {
			Tools json.RawMessage `json:"tools"`
		} `json:"result"`
	}
	if json.Unmarshal(line, &m) != nil || m.Method != "" || len(m.ID) == 0 {
		return s.write(line) // a notification (or junk) goes to the harness as it is
	}
	id := string(m.ID)
	if s.phase == phaseHandshake {
		switch id {
		case supervisorID("init", s.gen):
			if s.initialized != nil {
				if _, err := s.cur.in.Write(s.initialized); err != nil {
					return nil // its exit tries the next candidate
				}
			}
			_, _ = s.cur.in.Write([]byte(`{"jsonrpc":"2.0","id":` + supervisorID("tools", s.gen) + `,"method":"tools/list"}` + "\n"))
			return nil
		case supervisorID("tools", s.gen):
			tools := compact(m.Result.Tools)
			changed := s.tools != nil && !bytes.Equal(tools, s.tools) // a harness that never listed them needs no news
			s.tools = tools
			s.phase = phaseIdle
			s.resumedAt = time.Now()
			if changed {
				if err := s.write(listChangedLine); err != nil {
					return err
				}
			}
			held := s.held
			s.held = nil
			for _, l := range held {
				if _, err := s.cur.in.Write(l); err != nil {
					s.held = append(s.held, l)
				}
			}
			return nil
		}
	}
	delete(s.inflight, id)
	if s.toolsLists[id] {
		delete(s.toolsLists, id)
		s.tools = compact(m.Result.Tools)
	}
	if id == s.initID && !s.initAnswered {
		s.initAnswered = true
		line = declareListChanged(line)
	}
	return s.write(line)
}

// stop ends the child when the process is asked to stop: its input closes and it gets a moment to close its
// connection before it is killed.
func (s *supervisor) stop(exits <-chan childExit, lines <-chan childLine) int {
	_ = s.cur.in.Close()
	timer := time.NewTimer(closeTimeoutSupervised)
	defer timer.Stop()
	for {
		select {
		case l := <-lines:
			if l.gen == s.gen {
				_ = s.write(l.line) // what it still says on its way out
			}
		case e := <-exits:
			if e.gen == s.gen {
				return 0
			}
		case <-timer.C:
			s.cur.kill()
			return 0
		}
	}
}

// closeTimeoutSupervised is how long a stopping supervisor waits for its child, a little over the
// channel's own close timeout.
const closeTimeoutSupervised = 3 * time.Second

func (s *supervisor) write(line []byte) error {
	_, err := s.out.Write(line)
	return err
}

// supervisorID is the JSON id of the supervisor's own request kind for child gen; it never looks like the
// numbers harnesses use.
func supervisorID(kind string, gen int) string {
	return strconv.Quote(fmt.Sprintf("partyline-supervisor-%s-%d", kind, gen))
}

// withID is the request line with its id replaced by id (a JSON value), ending in a newline.
func withID(line []byte, id string) []byte {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(line, &m); err != nil {
		return line
	}
	m["id"] = json.RawMessage(id)
	out, err := json.Marshal(m)
	if err != nil {
		return line
	}
	return append(out, '\n')
}

// declareListChanged adds tools.listChanged to an initialize answer, so the harness takes
// notifications/tools/list_changed after a handover (AU-05). Anything it cannot read passes unchanged.
func declareListChanged(line []byte) []byte {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(line, &m); err != nil {
		return line
	}
	var result map[string]any
	if err := json.Unmarshal(m["result"], &result); err != nil || result == nil {
		return line
	}
	capabilities, _ := result["capabilities"].(map[string]any)
	if capabilities == nil {
		return line
	}
	tools, _ := capabilities["tools"].(map[string]any)
	if tools == nil {
		tools = map[string]any{}
	}
	tools["listChanged"] = true
	capabilities["tools"] = tools
	raw, err := json.Marshal(result)
	if err != nil {
		return line
	}
	m["result"] = raw
	out, err := json.Marshal(m)
	if err != nil {
		return line
	}
	return append(out, '\n')
}

// compact is v without insignificant space, nil for nothing.
func compact(v json.RawMessage) []byte {
	if len(v) == 0 {
		return nil
	}
	var b bytes.Buffer
	if err := json.Compact(&b, v); err != nil {
		return bytes.Clone(v)
	}
	return b.Bytes()
}
