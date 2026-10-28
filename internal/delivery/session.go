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
// first valid address wins. An address inside code - an inline code span between backticks or a fenced
// code block - is an example of an address, not one: that part of the text is not read (blankCode, SESS-48).
func SessionLabel(text, handle string) string {
	if handle == "" {
		return ""
	}
	text = blankCode(text)
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

// blankCode returns text with every code span replaced by spaces of the same length: the inline code
// spans (a run of n backticks up to the next run of exactly n, which stay literal when none follows) and the
// fenced code blocks (a line of three or more backticks or tildes, indented by at most three spaces, up to
// the line that closes it with the same character and at least as many of them, or the end of the text), the
// way Markdown reads them. A backslash before a backtick makes it a plain character. The text without any
// backtick or tilde is returned as it is.
func blankCode(text string) string {
	if !strings.ContainsAny(text, "`~") {
		return text
	}
	b := []byte(text)
	blank := func(from, to int) {
		for i := from; i < to; i++ {
			if b[i] != '\n' {
				b[i] = ' '
			}
		}
	}
	for i := 0; i < len(b); {
		if i == 0 || b[i-1] == '\n' {
			if fence, n, ok := openingFence(b, i); ok {
				end := closingFenceEnd(b, lineEnd(b, i), fence, n)
				blank(i, end)
				i = end
				continue
			}
		}
		switch {
		case b[i] == '\\' && i+1 < len(b) && b[i+1] == '`':
			i += 2
		case b[i] == '`':
			n := backtickRun(b, i)
			if end, ok := closingSpan(b, i+n, n); ok {
				blank(i, end)
				i = end
			} else {
				i += n
			}
		default:
			i++
		}
	}
	return string(b)
}

// backtickRun is the length of the run of backticks that starts at b[i].
func backtickRun(b []byte, i int) int {
	n := 0
	for i+n < len(b) && b[i+n] == '`' {
		n++
	}
	return n
}

// closingSpan finds, from b[from], the run of exactly n backticks that closes an inline code span, and
// returns the index after it.
func closingSpan(b []byte, from, n int) (int, bool) {
	for j := from; j < len(b); {
		if b[j] != '`' {
			j++
			continue
		}
		m := backtickRun(b, j)
		if m == n {
			return j + m, true
		}
		j += m
	}
	return 0, false
}

// lineEnd is the index of the newline that ends the line starting at b[i], or len(b).
func lineEnd(b []byte, i int) int {
	for i < len(b) && b[i] != '\n' {
		i++
	}
	return i
}

// openingFence reports whether the line starting at b[i] opens a fenced code block: at most three spaces,
// then three or more backticks (with no backtick in the rest of the line, which would make it an inline
// span) or tildes. It returns the fence character and its length.
func openingFence(b []byte, i int) (fence byte, n int, ok bool) {
	for k := 0; k < 3 && i < len(b) && b[i] == ' '; k++ {
		i++
	}
	if i >= len(b) || (b[i] != '`' && b[i] != '~') {
		return 0, 0, false
	}
	fence = b[i]
	for i+n < len(b) && b[i+n] == fence {
		n++
	}
	if n < 3 {
		return 0, 0, false
	}
	if fence == '`' && strings.IndexByte(string(b[i+n:lineEnd(b, i)]), '`') >= 0 {
		return 0, 0, false
	}
	return fence, n, true
}

// closingFenceEnd returns the index after the line that closes a fence of n characters, searching from the
// newline at b[from]; the end of the text when none does.
func closingFenceEnd(b []byte, from int, fence byte, n int) int {
	for i := from; i < len(b); {
		start := i + 1 // the line after the newline at b[i]
		if start >= len(b) {
			return len(b)
		}
		end := lineEnd(b, start)
		line := b[start:end]
		k := 0
		for k < 3 && k < len(line) && line[k] == ' ' {
			k++
		}
		run := 0
		for k+run < len(line) && line[k+run] == fence {
			run++
		}
		if run >= n && strings.TrimRight(string(line[k+run:]), " \t") == "" {
			return end
		}
		i = end
	}
	return len(b)
}
