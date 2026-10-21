package channel

import (
	"context"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/wnexous/partyline/internal/api/dto"
)

// gitTimeout bounds the git call of DeriveProject.
const gitTimeout = 5 * time.Second

// HomeProject is the project of a session started in the home folder or in / without
// --project or --name (ID-03).
const HomeProject = "home"

// DeriveProject returns the project of a session started in dir (ID-01): the basename of the
// git repository root that contains dir, else dir's basename. The server turns it into the
// project's slug and name.
func DeriveProject(dir string) string {
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
	return base
}

// IsHomeDir reports whether dir is the home folder home or the file system root (ID-03).
func IsHomeDir(dir, home string) bool {
	dir = filepath.Clean(dir)
	return dir == string(filepath.Separator) || (home != "" && dir == filepath.Clean(home))
}

// Identity is what a session tells the server about itself on connect (ID-01, ID-02): an
// explicit agent name (a fixed agent) or none (the server names a temporary agent after the
// project and harness), the project, the harness and the machine.
type Identity struct {
	// Name is the explicit agent name (--name); empty asks for a temporary agent.
	Name    string
	Project string
	// Harness is the harness id (claude, opencode, pi, omp, codex, gemini); empty is "agent".
	Harness string
	Machine string
}

// setHeaders sets the project, harness and machine headers that are not empty.
func (id Identity) setHeaders(h http.Header) {
	for k, v := range map[string]string{dto.HeaderProject: id.Project, dto.HeaderHarness: id.Harness, dto.HeaderMachine: id.Machine} {
		if v != "" {
			h.Set(k, v)
		}
	}
}
