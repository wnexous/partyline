package whatsapp

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Bounds of what a shared contact adds to the text of an event (CID-30): a card is data of whoever
// shared it, of any size and shape.
const (
	// maxCard is how much of a vCard is read.
	maxCard = 64 << 10
	// maxContacts is how many contacts of one message are named, maxNumbers how many numbers of
	// one card are kept and maxContactField how many characters a name or a number may have.
	maxContacts     = 50
	maxNumbers      = 10
	maxContactField = 80
)

// vcard is what Partyline takes from a vCard: the name of the contact and its numbers.
type vcard struct {
	Name    string
	Numbers []string
}

// parseVCard reads the name and the telephone numbers of a vCard (CID-30). Lines are unfolded and
// their names are case-insensitive, with or without a group prefix ("item1.TEL"). A number is the
// WhatsApp id of its line when it has one (the waid parameter, international and digits only, with a
// plus) and otherwise the value of the line: only its digits and a leading plus when it is a number
// with separators, as written when it has anything else in it (an extension, a code). Numbers come
// in the order of the card, once each. The name is the formatted name (FN), else the given and the
// family name of N. Whatever the card holds, nothing but this is read, and all of it is one line of
// bounded length.
func parseVCard(card string) vcard {
	if len(card) > maxCard {
		card = card[:maxCard]
	}
	var out vcard
	var formatted, structured string
	for _, line := range unfold(card) {
		name, params, value, ok := property(line)
		if !ok {
			continue
		}
		switch name {
		case "FN":
			if formatted == "" {
				formatted = ellipsize(oneLine(value), maxContactField)
			}
		case "N":
			if structured == "" {
				structured = structuredName(value)
			}
		case "TEL":
			n := telNumber(params, value)
			if n != "" && len(out.Numbers) < maxNumbers && !contains(out.Numbers, n) {
				out.Numbers = append(out.Numbers, n)
			}
		}
	}
	out.Name = formatted
	if out.Name == "" {
		out.Name = structured
	}
	return out
}

// unfold joins the lines of a card that were folded (a line that starts with a space or a tab
// continues the one before) after reading any line end.
func unfold(card string) []string {
	card = strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(card)
	var lines []string
	for _, l := range strings.Split(card, "\n") {
		if l != "" && (l[0] == ' ' || l[0] == '\t') && len(lines) > 0 {
			lines[len(lines)-1] += l[1:]
			continue
		}
		lines = append(lines, l)
	}
	return lines
}

// property splits a line into the upper-case name of the property (without its group), its
// parameters and its value. The first colon outside double quotes ends the name and the parameters;
// a line without one, or without a name, is no property.
func property(line string) (name string, params []string, value string, ok bool) {
	colon := indexOutsideQuotes(line, ':')
	if colon < 0 {
		return "", nil, "", false
	}
	parts := splitOutsideQuotes(line[:colon], ';')
	name = strings.ToUpper(strings.TrimSpace(parts[0]))
	if dot := strings.LastIndexByte(name, '.'); dot >= 0 {
		name = name[dot+1:]
	}
	if name == "" {
		return "", nil, "", false
	}
	return name, parts[1:], line[colon+1:], true
}

func indexOutsideQuotes(s string, sep byte) int {
	quoted := false
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '"':
			quoted = !quoted
		case s[i] == sep && !quoted:
			return i
		}
	}
	return -1
}

func splitOutsideQuotes(s string, sep byte) []string {
	var parts []string
	for {
		i := indexOutsideQuotes(s, sep)
		if i < 0 {
			return append(parts, s)
		}
		parts = append(parts, s[:i])
		s = s[i+1:]
	}
}

// telNumber is the number a TEL line gives: its waid, else its value; empty when it gives none.
func telNumber(params []string, value string) string {
	for _, p := range params {
		if k, v, ok := strings.Cut(p, "="); ok && strings.EqualFold(strings.TrimSpace(k), "waid") {
			if id := strings.Trim(v, `" `); id != "" && strings.Trim(id, "0123456789") == "" {
				return ellipsize("+"+id, maxContactField)
			}
		}
	}
	return number(value)
}

// number cleans the value of a TEL line. A value made of digits and the separators people write
// ("+55 (11) 99999-0000") keeps its digits and a leading plus; one with anything else in it (an
// extension, a code, a word) is kept as written, because merging its digits would make another number.
func number(value string) string {
	value = oneLine(value)
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '+' && b.Len() == 0:
			b.WriteRune(r)
		case strings.ContainsRune(" -.()/ ", r):
		default:
			return ellipsize(value, maxContactField)
		}
	}
	if strings.Trim(b.String(), "+") == "" {
		return ellipsize(value, maxContactField)
	}
	return ellipsize(b.String(), maxContactField)
}

// structuredName is the given and the family name of the value of an N line
// ("family;given;additional;prefix;suffix").
func structuredName(value string) string {
	parts := strings.Split(value, ";")
	given, family := "", parts[0]
	if len(parts) > 1 {
		given = parts[1]
	}
	return ellipsize(oneLine(given+" "+family), maxContactField)
}

// oneLine turns every run of white space, control and formatting characters (a newline, an escape
// sequence, a zero-width space, a right-to-left override) into one space, and trims it: text of a
// stranger that goes into a line of an event or of a status.
func oneLine(s string) string {
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == utf8.RuneError
	}), " ")
}

// ellipsize cuts s to at most n characters, the last one an ellipsis when it was cut.
func ellipsize(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return strings.TrimRight(string([]rune(s)[:n-1]), " ") + "…"
}

func contains(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}
