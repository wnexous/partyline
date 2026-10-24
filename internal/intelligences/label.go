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

// segmentsUnder splits the folder into the elements under home, and reports whether it is outside it (an
// absolute path). A folder that is home itself has no element.
func segmentsUnder(folder, home string) ([]string, bool) {
	if home != "" {
		if home == "/" {
			if folder == "/" {
				return nil, false
			}
			return splitPath(folder), false
		}
		if folder == home {
			return nil, false
		}
		if strings.HasPrefix(folder, home+"/") {
			return splitPath(strings.TrimPrefix(folder, home+"/")), false
		}
	}
	if folder == "/" {
		return nil, true
	}
	return splitPath(folder), true
}

func splitPath(folder string) []string {
	folder = strings.Trim(folder, "/")
	if folder == "" {
		return nil
	}
	return strings.Split(folder, "/")
}
