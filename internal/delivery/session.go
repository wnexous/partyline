package delivery

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/wnexous/partyline/internal/api/dto"
)

// sessionLabelPattern is the shape of a session label (SESS-11).
var sessionLabelPattern = regexp.MustCompile(dto.SessionLabelPattern)

// SessionLabel returns the session label a message's text addresses to the AI with handle,
// or "" when it addresses the AI without a label (SESS-21). An address is "@handle:" followed
// by a label; the handle matches case-insensitively with no letter, digit or underscore right
// before the "@" and no handle character right after it (the rules of containsBounded and
// continuesHandle), and the label is lowercase a-z, 0-9 and "-", 1 to dto.MaxSessionLabel
// characters; it is read case-insensitively (handles are) and returned lowercase, and it must end at a
// boundary: the end of the text or a character that is none of a-z, A-Z, 0-9, "-", ".", "/" and ":", so
// "@atlas:wt.example.com" and "@atlas:wt/x" are not addresses, and neither is "/@atlas:wt". An invalid or over-long label is not an address, so the scan continues, and the
// first valid address wins.
func SessionLabel(text, handle string) string {
	if handle == "" {
		return ""
	}
	for i := 0; i < len(text); i++ {
		if text[i] != '@' {
			continue
		}
		if i > 0 {
			before, _ := utf8.DecodeLastRuneInString(text[:i])
			if isWordRune(before) || before == '/' { // not inside a path or a URL
				continue
			}
		}
		rest, ok := cutPrefixFold(text[i+1:], handle)
		if !ok {
			continue
		}
		if rest != "" {
			after, _ := utf8.DecodeRuneInString(rest)
			if continuesHandle(after) {
				continue
			}
		}
		if !strings.HasPrefix(rest, ":") {
			continue
		}
		if label := leadingLabel(rest[1:]); label != "" {
			return label
		}
	}
	return ""
}

// cutPrefixFold cuts prefix off s ignoring case and reports whether it was there.
func cutPrefixFold(s, prefix string) (string, bool) {
	i := 0
	for _, want := range prefix {
		got, size := utf8.DecodeRuneInString(s[i:])
		if size == 0 || !strings.EqualFold(string(got), string(want)) {
			return "", false
		}
		i += size
	}
	return s[i:], true
}

// leadingLabel returns the maximal run of label characters (any case) at the start of s, lowercased, when it
// ends at a boundary (endsLabel) and matches dto.SessionLabelPattern, else "".
func leadingLabel(s string) string {
	end := 0
	for end < len(s) {
		c := s[end]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' {
			end++
			continue
		}
		break
	}
	if end < len(s) && !endsLabel(s[end:]) {
		return ""
	}
	label := strings.ToLower(s[:end])
	if label == "" || !sessionLabelPattern.MatchString(label) {
		return ""
	}
	return label
}

// endsLabel reports whether rest, what follows a label, is a boundary: nothing, or a character that is none of
// "/", ":" and the label characters. A "." is a boundary only as a sentence's end - last, or followed by a
// space - so a host name ("wt.example.com") or a path never routes.
func endsLabel(rest string) bool {
	switch rest[0] {
	case '/', ':':
		return false
	case '.':
		if len(rest) == 1 {
			return true
		}
		next, _ := utf8.DecodeRuneInString(rest[1:])
		return unicode.IsSpace(next)
	}
	return true
}
