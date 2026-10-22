package intelligences

import (
	"path"
	"strings"
)

// FolderLabel is how a folder reads in the UI: "~" for the machine's home folder, "~/x/y"
// under it and the absolute path elsewhere; empty for no folder (an unlinked AI). home is the
// canonical home folder the machine reported, or empty when unknown.
func FolderLabel(home, folder string) string {
	if folder == "" {
		return ""
	}
	folder = path.Clean(folder)
	if home != "" {
		home = path.Clean(home)
	}
	segments, absolute := segmentsUnder(folder, home)
	switch {
	case absolute:
		return folder
	case len(segments) == 0:
		return "~"
	}
	return "~/" + strings.Join(segments, "/")
}
