package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// folderEnvVar is the canonical folder the launcher runs in; the launcher always sets it, so a
// process whose environment has it was launched by `partyline <harness>`.
const folderEnvVar = "PARTYLINE_FOLDER"

// launcherVars are the variables `partyline <harness>` sets for its session (see launch).
// PARTYLINE_TAKEOVER is the one a launcher sets when the user agreed to take the AI's live
// session over.
var launcherVars = []string{folderEnvVar, "PARTYLINE_HARNESS", "PARTYLINE_TAKEOVER", "PARTYLINE_AGENT"}

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
