package intelligences

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TREE-02 (labels): the home folder reads "~", folders under it "~/…", others stay absolute.
func TestFolderLabel(t *testing.T) {
	for _, c := range []struct{ name, home, folder, want string }{
		{"no folder", "/home/u", "", ""},
		{"home itself", "/home/u", "/home/u", "~"},
		{"under home", "/home/u", "/home/u/atlas", "~/atlas"},
		{"deeper under home", "/home/u", "/home/u/c/d/lab", "~/c/d/lab"},
		{"outside home", "/home/u", "/srv/api", "/srv/api"},
		{"sibling with the home as a prefix", "/home/u", "/home/user/x", "/home/user/x"},
		{"the root", "/home/u", "/", "/"},
		{"unknown home", "", "/home/u/atlas", "/home/u/atlas"},
		{"root as home", "/", "/", "~"},
		{"root as home, a folder", "/", "/srv", "~/srv"},
		{"trailing slashes are cleaned", "/home/u/", "/home/u/atlas/", "~/atlas"},
	} {
		require.Equal(t, c.want, FolderLabel(c.home, c.folder), c.name)
	}
}
