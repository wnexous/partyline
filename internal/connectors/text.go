package connectors

import (
	"strings"
	"unicode/utf8"
)

// AuthorLine returns the text prefixed with "<prefix><author><suffix>" when the author is
// known (e.g. "*Alice*\n" on WhatsApp); adapters use it to show who wrote a room message.
func AuthorLine(author, prefix, suffix, text string) string {
	if text == "" || author == "" {
		return text
	}
	return prefix + author + suffix + text
}

// SplitText splits text into ordered parts of at most limit characters (runes), breaking
// after the last newline of a part when there is one, else after the last space, else at
// the limit. Joining the parts gives back the text. An empty text has no parts.
func SplitText(text string, limit int) []string {
	var parts []string
	for text != "" {
		if utf8.RuneCountInString(text) <= limit {
			parts = append(parts, text)
			break
		}
		cut := byteIndexOfRune(text, limit)
		head := text[:cut]
		if i := strings.LastIndexByte(head, '\n'); i > 0 {
			cut = i + 1
		} else if i := strings.LastIndexByte(head, ' '); i > 0 {
			cut = i + 1
		}
		parts = append(parts, text[:cut])
		text = text[cut:]
	}
	return parts
}

// byteIndexOfRune returns the byte offset of the n-th rune of s.
func byteIndexOfRune(s string, n int) int {
	i := 0
	for pos := range s {
		if i == n {
			return pos
		}
		i++
	}
	return len(s)
}
