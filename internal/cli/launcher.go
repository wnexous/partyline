package cli

import (
	"fmt"
	"os"
	"os/exec"
)

// execLookPath finds programs on PATH; tests use stdio.lookPath instead.
var execLookPath = exec.LookPath

// claudeChannelArgs are the arguments `partyline claude` puts before the user's (HAR-03).
var claudeChannelArgs = []string{"--dangerously-load-development-channels", "server:partyline"}

// launchClaude runs `partyline claude [args…]`: it replaces this process with
// `claude --dangerously-load-development-channels server:partyline [args…]` (HAR-03).
func launchClaude(args []string, io stdio) int {
	lookPath := io.lookPath
	if lookPath == nil {
		lookPath = execLookPath
	}
	path, err := lookPath("claude")
	if err != nil {
		fmt.Fprintln(io.stderr, "partyline claude: Claude Code (claude) is not on PATH; install it from https://claude.com/claude-code")
		return 1
	}
	argv := append([]string{"claude"}, claudeChannelArgs...)
	argv = append(argv, args...)
	run := io.exec
	if run == nil {
		run = execProgram
	}
	code, err := run(path, argv, os.Environ())
	if err != nil {
		fmt.Fprintln(io.stderr, "partyline claude:", err)
		return 1
	}
	return code
}
