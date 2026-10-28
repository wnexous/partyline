package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/channel"
)

// folderEnvVar is the canonical folder the launcher runs in; the launcher always sets it, so a
// process whose environment has it was launched by `partyline <harness>`.
const folderEnvVar = "PARTYLINE_FOLDER"

// mainFolderVar is the canonical folder of the main worktree when the launch runs in a linked git
// worktree: the session reads it and sends it beside its own folder, so the server resolves the AI of
// the repository (SESS-01, SESS-09). Empty outside a linked worktree and for a --parent launch.
const mainFolderVar = "PARTYLINE_MAIN_FOLDER"

// What a session asked for when it was launched (--org, --position, --parent; ORG-04, POS-07, POS-08). The
// launcher reads PARTYLINE_ORG, PARTYLINE_POSITION and PARTYLINE_PARENT when the flag is absent, and hands the
// values to the session's own processes as PARTYLINE_LAUNCH_ORG, PARTYLINE_LAUNCH_POSITION and
// PARTYLINE_LAUNCH_PARENT. It consumes the first three and never reads the second three as input: a launch from
// inside a launched session is another AI and does not inherit its placement (AD-020).
const (
	orgEnvVar      = "PARTYLINE_ORG"
	positionEnvVar = "PARTYLINE_POSITION"
	parentEnvVar   = "PARTYLINE_PARENT"

	launchOrgVar      = "PARTYLINE_LAUNCH_ORG"
	launchPositionVar = "PARTYLINE_LAUNCH_POSITION"
	launchParentVar   = "PARTYLINE_LAUNCH_PARENT"
)

// liveEnvVar carries, to the processes of a pi or OpenCode session, the live id of that session as
// "<pid>:<id>": the pid of the harness process that owns the id and the id itself (22 characters of
// [A-Za-z0-9_-], channel.NewLiveID). `partyline pi` and `partyline opencode` mint it for the process they
// start (exec keeps the pid), and the adapter mints it when the launcher did not or the pid is not its own.
// `partyline listen` connects with that id, and the tools-only process of the same harness sends it with
// every call, so the server knows the tools act as the session that listen holds (SESS-45). The pid ties
// the id to one harness process: a harness started by a tool of this one inherits the variable, and a value
// whose pid is not the parent of the reading process (or, from an ancestor's environment, that ancestor) is
// ignored, so two sessions never share an id.
const liveEnvVar = "PARTYLINE_LIVE"

// liveIDPattern is the shape of a live id the server accepts (dto.HeaderLive).
var liveIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)

// parseLive splits a PARTYLINE_LIVE value into its owner pid and the live id.
func parseLive(v string) (pid int, id string, ok bool) {
	p, id, found := strings.Cut(strings.TrimSpace(v), ":")
	pid, err := strconv.Atoi(p)
	if !found || err != nil || pid <= 0 || !liveIDPattern.MatchString(id) {
		return 0, "", false
	}
	return pid, id, true
}

// sessionLiveID is the live id of the pi or OpenCode session this process belongs to: the id of
// PARTYLINE_LIVE, as returned by withLauncherEnv, which keeps the variable only when its owner is the
// parent of this process or the launched ancestor it came from. Empty when there is none.
func sessionLiveID(io stdio) string {
	_, id, _ := parseLive(io.getenv(liveEnvVar))
	return id
}

// principalEnvVar is PARTYLINE_PRINCIPAL=1, which `partyline <harness> --principal` sets: this session asks to
// be the AI's principal one and pins its harness session id (SESS-57).
const principalEnvVar = "PARTYLINE_PRINCIPAL"

// harnessSessionVar carries the harness's id of the conversation to the session's partyline processes. The
// pi adapter sets it on the listen process it starts; Claude Code needs none, its channel process has
// claudeSessionVar (SESS-50). Any other harness whose host can set an environment variable may use it.
const harnessSessionVar = "PARTYLINE_HARNESS_SESSION"

// claudeSessionVar is the variable Claude Code puts in the environment of the processes it starts, among them
// `partyline channel`: its session id, which stays the same when the conversation is resumed with --resume.
const claudeSessionVar = "CLAUDE_CODE_SESSION_ID"

// harnessSessionPattern is the shape of a harness session id the server accepts (dto.HarnessSessionPattern).
var harnessSessionPattern = regexp.MustCompile(dto.HarnessSessionPattern)

// harnessSessionID is the harness's id of the conversation this process belongs to (SESS-50): harnessSessionVar
// first, then, for Claude Code only (an empty harness is the full channel, which is Claude Code), claudeSessionVar (another harness started from a Claude Code tool inherits
// it and must not send it). Empty when there is none or it is not an id the server accepts: such a session
// then has no id, which only costs it the fixed principal role, never the connection.
func harnessSessionID(io stdio, harness string) string {
	id := strings.TrimSpace(io.getenv(harnessSessionVar))
	if id == "" && (harness == channel.HarnessClaude || harness == "") {
		id = strings.TrimSpace(io.getenv(claudeSessionVar))
	}
	if !harnessSessionPattern.MatchString(id) {
		return ""
	}
	return id
}

// launcherVars are the variables `partyline <harness>` sets for its session (see launch).
// PARTYLINE_TAKEOVER is the one a launcher sets when the user agreed to take the AI's live
// session over; the PARTYLINE_LAUNCH_* ones carry what the session asked for.
var launcherVars = []string{
	folderEnvVar, mainFolderVar, "PARTYLINE_HARNESS", "PARTYLINE_TAKEOVER", principalEnvVar, "PARTYLINE_AGENT",
	launchOrgVar, launchPositionVar, launchParentVar, liveEnvVar,
}

// maxAncestors is how many processes, starting at the parent, the lookup reads.
const maxAncestors = 4

// launcherEnviron returns the non-empty launcher variables of a NUL-separated environment
// block (/proc/<pid>/environ). Nothing else of the block is kept.
func launcherEnviron(block []byte) map[string]string {
	out := map[string]string{}
	for _, kv := range bytes.Split(block, []byte{0}) {
		k, v, ok := strings.Cut(string(kv), "=")
		if !ok || strings.TrimSpace(v) == "" {
			continue
		}
		for _, name := range launcherVars {
			if k == name {
				out[k] = v
			}
		}
	}
	return out
}

// ancestorLauncherEnv returns the launcher variables of the nearest process, from pid up
// through at most maxAncestors processes, whose environment has PARTYLINE_FOLDER, read under
// procRoot (/proc on Linux). Harnesses such as Codex CLI start MCP servers with a filtered
// environment; the harness process itself (or the process that started it) still has the
// launcher's variables. Processes whose environment cannot be read are passed over; without
// procRoot (macOS, Windows) or such a process it returns nil. The environment is never logged.
func ancestorLauncherEnv(procRoot string, pid int) map[string]string {
	for i := 0; i < maxAncestors && pid > 1; i++ {
		if block, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "environ")); err == nil {
			if vars := launcherEnviron(block); vars[folderEnvVar] != "" {
				// The id belongs to the process that owns it: this ancestor's environment is only its own.
				if owner, _, ok := parseLive(vars[liveEnvVar]); !ok || owner != pid {
					delete(vars, liveEnvVar)
				}
				return vars
			}
		}
		pid = parentPID(procRoot, pid)
	}
	return nil
}

// parentPID returns the parent of pid from <procRoot>/<pid>/stat (the field after the state,
// which follows the parenthesised command name), or 0.
func parentPID(procRoot string, pid int) int {
	data, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0
	}
	i := bytes.LastIndexByte(data, ')')
	if i < 0 {
		return 0
	}
	fields := strings.Fields(string(data[i+1:]))
	if len(fields) < 2 {
		return 0
	}
	ppid, _ := strconv.Atoi(fields[1])
	return ppid
}

// procLauncherEnv is stdio.launcherEnv for real runs: the launcher variables of the nearest
// launched ancestor of this process.
func procLauncherEnv() map[string]string { return ancestorLauncherEnv("/proc", os.Getppid()) }

// withLauncherEnv returns io with the launcher variables of the nearest launched ancestor
// (io.launcherEnv) filled in when this process has no PARTYLINE_FOLDER; its own variables
// win. A session started with `partyline <harness>` thus keeps its folder and harness in its
// tools-only channel even when the harness strips the server's environment.
func withLauncherEnv(io stdio) stdio {
	io = ownLiveOnly(io)
	if io.launcherEnv == nil || strings.TrimSpace(io.getenv(folderEnvVar)) != "" {
		return io
	}
	vars := io.launcherEnv()
	if len(vars) == 0 {
		return io
	}
	own := io.getenv
	io.getenv = func(k string) string {
		if v := own(k); v != "" {
			return v
		}
		return vars[k]
	}
	return io
}

// ownLiveOnly returns io with PARTYLINE_LIVE read only when its owner is the parent of this process: the
// harness process that started it. A value inherited from a harness further up belongs to another session.
func ownLiveOnly(io stdio) stdio {
	own := io.getenv
	ppid := io.ppid
	if ppid == nil {
		ppid = os.Getppid
	}
	io.getenv = func(k string) string {
		v := own(k)
		if k != liveEnvVar || v == "" {
			return v
		}
		if owner, _, ok := parseLive(v); ok && owner == ppid() {
			return v
		}
		return ""
	}
	return io
}
