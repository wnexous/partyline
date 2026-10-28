package cli

import (
	"context"
	"fmt"
	"slices"

	"github.com/wnexous/partyline/internal/harness"
)

// hookCmd runs `partyline hook <name>` for the turn-end hooks that Partyline wrote into Codex CLI, Gemini
// CLI and Antigravity before they stopped being supported (AD-035). It does nothing and lets the
// harness stop as usual ({}), so a hook that `partyline setup` has not removed yet does not fail at
// the end of every turn. Any other name is a usage error.
func hookCmd(_ context.Context, args []string, io stdio) int {
	if len(args) < 1 || !slices.Contains(harness.LegacyHooks, args[0]) {
		fmt.Fprintln(io.stderr, "partyline hook: the turn-end hooks are gone with the harnesses they served; run partyline setup to remove the old ones")
		return 2
	}
	fmt.Fprintln(io.stdout, "{}")
	return 0
}
