package agents

import (
	"fmt"
	"strings"

	"github.com/wnexous/partyline/internal/api/dto"
)

// defaultSessionLabel is the label of a session whose folder yields no slug at all.
const defaultSessionLabel = "session"

// LabelForFolder is the label of a worktree session: the base name of its folder, lowercased,
// with every run of characters outside [a-z0-9] collapsed to one hyphen, at most
// dto.MaxSessionLabel characters and never starting or ending with a hyphen; a folder with no
// such character is "session". The principal session of an AI is "" instead (SESS-11).
func LabelForFolder(folder string) string {
	base := strings.ToLower(folderBase(folder))
	var b strings.Builder
	separated := false
	for _, r := range base {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			separated = false
			continue
		}
		if b.Len() > 0 && !separated {
			b.WriteByte('-')
			separated = true
		}
	}
	label := strings.Trim(b.String(), "-")
	if len(label) > dto.MaxSessionLabel {
		label = strings.TrimRight(label[:dto.MaxSessionLabel], "-")
	}
	if label == "" {
		return defaultSessionLabel
	}
	return label
}

// AllocateLabel is the label a session attaches with: the remembered one when it is not empty
// and free (a reconnect), the preferred one when it is free (the principal session's "" is a
// preferred label like any other), else the first free "<harness>-<n>" with n >= 2, harness
// cleaned by cleanHarness and "agent" when it is empty. taken answers whether a label is
// already live for the AI; the caller holds the lock that makes the answer stable (SESS-11).
func AllocateLabel(preferred, harness string, taken func(string) bool, remembered string) string {
	if remembered != "" && !taken(remembered) {
		return remembered
	}
	if !taken(preferred) {
		return preferred
	}
	base := cleanHarness(harness)
	if base == "" {
		base = DefaultHarness
	}
	for n := 2; ; n++ {
		suffix := fmt.Sprintf("-%d", n)
		label := fitLabel(base, suffix) + suffix
		if !taken(label) {
			return label
		}
	}
}

// fitLabel trims base so that base plus suffix stays within dto.MaxSessionLabel characters and
// does not end with a hyphen.
func fitLabel(base, suffix string) string {
	if room := dto.MaxSessionLabel - len(suffix); room < len(base) {
		if room <= 0 {
			return base
		}
		base = base[:room]
	}
	return strings.TrimRight(base, "-")
}
