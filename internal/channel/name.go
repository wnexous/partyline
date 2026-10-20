package channel

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/wnexous/partyline/internal/agents"
)

// gitTimeout bounds the git call of DeriveName.
const gitTimeout = 5 * time.Second

// DeriveName returns the agent name for a session started in dir when none is set (NAME-01):
// the basename of the git repository root that contains dir, else dir's basename,
// slugified like the server does.
func DeriveName(dir string) string {
	base := filepath.Base(dir)
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	if out, err := cmd.Output(); err == nil {
		if root := strings.TrimSpace(string(out)); root != "" {
			base = filepath.Base(root)
		}
	}
	return agents.Slug(base)
}
