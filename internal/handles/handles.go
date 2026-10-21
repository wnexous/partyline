// Package handles holds the rules for @handles and default display names shared by the
// migrations and the services (PROF-02, AID-03): slugs, defaults per kind, validation and
// allocation of a free handle.
package handles

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Kind is the kind of intelligence a default handle is made for.
type Kind int

// Kinds of default handles: the prefix used when a slug is empty or starts with a digit.
const (
	AI Kind = iota
	User
	Person
)

// MaxLen is the maximum handle length; SlugLen leaves room for a "-NNN" suffix.
const (
	MaxLen         = 40
	SlugLen        = 36
	MaxDisplayName = 64
)

// ErrInvalid is returned by Validate for a handle that breaks the rules.
var ErrInvalid = errors.New("invalid handle")

var (
	pattern  = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)
	reserved = map[string]bool{"partyline": true, "all": true, "everyone": true, "here": true, "owner": true}
	prefixes = map[Kind]string{AI: "ai", User: "user", Person: "contact"}
)

// fold maps common lower-case Latin letters with diacritics to ASCII.
var fold = map[rune]string{
	'à': "a", 'á': "a", 'â': "a", 'ã': "a", 'ä': "a", 'å': "a", 'ā': "a", 'ă': "a", 'ą': "a",
	'æ': "ae", 'ç': "c", 'ć': "c", 'č': "c", 'ď': "d", 'đ': "d",
	'è': "e", 'é': "e", 'ê': "e", 'ë': "e", 'ē': "e", 'ė': "e", 'ę': "e", 'ě': "e",
	'ğ': "g", 'ì': "i", 'í': "i", 'î': "i", 'ï': "i", 'ī': "i", 'į': "i", 'ı': "i",
	'ł': "l", 'ñ': "n", 'ń': "n", 'ň': "n",
	'ò': "o", 'ó': "o", 'ô': "o", 'õ': "o", 'ö': "o", 'ø': "o", 'ō': "o", 'ő': "o", 'œ': "oe",
	'ř': "r", 'ś': "s", 'š': "s", 'ş': "s", 'ß': "ss", 'ť': "t", 'ţ': "t",
	'ù': "u", 'ú': "u", 'û': "u", 'ü': "u", 'ū': "u", 'ů': "u", 'ű': "u", 'ų': "u",
	'ý': "y", 'ÿ': "y", 'ź': "z", 'ż': "z", 'ž': "z",
}

// Slug lower-cases s, folds common Latin accents, turns every run of other characters into
// "-", trims "-" from both ends and cuts the result to SlugLen bytes.
func Slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		var part string
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			part = string(r)
		default:
			part = fold[r]
		}
		if part == "" {
			dash = true
			continue
		}
		if dash && b.Len() > 0 {
			b.WriteByte('-')
		}
		dash = false
		b.WriteString(part)
	}
	out := b.String()
	if len(out) > SlugLen {
		out = out[:SlugLen]
	}
	return strings.Trim(out, "-")
}

// Default returns the default handle for base: its slug, prefixed with "ai-", "user-" or
// "contact-" when it does not start with a letter ("ai", "user", "contact" when empty).
func Default(kind Kind, base string) string {
	slug := Slug(base)
	prefix := prefixes[kind]
	switch {
	case slug == "":
		return prefix
	case slug[0] < 'a' || slug[0] > 'z':
		return prefix + "-" + slug
	}
	return slug
}

// Validate lower-cases h and checks it: 1-40 characters of a-z, 0-9 and "-", starting with a
// letter, not a reserved word. It returns the lower-cased handle.
func Validate(h string) (string, error) {
	h = strings.ToLower(h)
	if !pattern.MatchString(h) {
		return "", fmt.Errorf("%w: use 1-%d characters a-z, 0-9 and -, starting with a letter", ErrInvalid, MaxLen)
	}
	if reserved[h] {
		return "", fmt.Errorf("%w: %q is reserved", ErrInvalid, h)
	}
	return h, nil
}

// Allocate returns the first free handle among base, base-2, base-3, … taken reports whether a
// (lower-case) handle is in use; reserved words are skipped. A suffix that would pass MaxLen
// cuts the base. A base that is not shaped like a handle is replaced by Default(AI, base).
func Allocate(taken func(string) bool, base string) string {
	base = strings.ToLower(base)
	if !pattern.MatchString(base) {
		base = Default(AI, base)
	}
	if !reserved[base] && !taken(base) {
		return base
	}
	// base starts with a letter, so every base-N is a valid handle and never a reserved word.
	for n := 2; ; n++ {
		suffix := "-" + strconv.Itoa(n)
		stem := base
		if len(stem)+len(suffix) > MaxLen {
			stem = strings.TrimRight(stem[:MaxLen-len(suffix)], "-")
		}
		if candidate := stem + suffix; !taken(candidate) {
			return candidate
		}
	}
}

// DisplayName returns the default display name for base: control characters dropped, the
// first rune upper-cased, at most MaxDisplayName code points.
func DisplayName(base string) string {
	var b strings.Builder
	n := 0
	for _, r := range base {
		if r == utf8.RuneError || unicode.IsControl(r) {
			continue
		}
		if n == 0 {
			r = unicode.ToUpper(r)
		}
		if n == MaxDisplayName {
			break
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}
