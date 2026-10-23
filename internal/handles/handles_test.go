package handles

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// PROF-02, AID-03: a folder base name gives the default handle (slug) and display name
// (first letter upper-cased).
func TestDefaultsFromABaseName(t *testing.T) {
	cases := []struct {
		kind          Kind
		base, handle  string
		display       string
		handleComment string
	}{
		{AI, "discord", "discord", "Discord", "plain folder name"},
		{AI, "2024-notes", "ai-2024-notes", "2024-notes", "must start with a letter"},
		{AI, "/", "ai", "/", "root folder"},
		{AI, "~", "ai", "~", "home shown as ~"},
		{AI, "Ágata Ção", "agata-cao", "Ágata Ção", "accents fold, spaces become -"},
		{AI, "über_ñandú", "uber-nandu", "Über_ñandú", "lower-case accents and _"},
		{AI, "My.Project", "my-project", "My.Project", "upper-case input is lower-cased"},
		{AI, "--notes--", "notes", "--notes--", "trimmed of -"},
		{User, "Bob.Smith", "bob-smith", "Bob.Smith", "username"},
		{User, "42", "user-42", "42", "user prefix"},
		{User, "", "user", "", "empty user base"},
		{Person, "Maria", "maria", "Maria", "person"},
		{Person, "+55 11", "contact-55-11", "+55 11", "person prefix"},
		{Person, "李", "contact", "李", "no Latin letter left"},
	}
	for _, c := range cases {
		require.Equal(t, c.handle, Default(c.kind, c.base), c.handleComment)
		require.Equal(t, c.display, DisplayName(c.base), c.handleComment)
		_, err := Validate(Default(c.kind, c.base))
		require.NoError(t, err, "a default handle is always valid: %q", c.base)
	}
}

// The slug is cut to 36 bytes so a "-NN" suffix still fits the 40-character limit.
func TestSlugIsCutTo36Bytes(t *testing.T) {
	long := strings.Repeat("a", 50)
	require.Equal(t, strings.Repeat("a", 36), Slug(long))
	// A cut that ends on "-" drops it.
	require.Equal(t, strings.Repeat("a", 35), Slug(strings.Repeat("a", 35)+" b"+strings.Repeat("c", 10)))
}

// PROF-02: 1-40 characters of a-z0-9- starting with a letter, case-insensitive (lower-cased),
// reserved words refused.
func TestValidate(t *testing.T) {
	ok := []struct{ in, out string }{
		{"a", "a"},
		{"Notes", "notes"},
		{"ATLAS-2", "atlas-2"},
		{"a" + strings.Repeat("b", 39), "a" + strings.Repeat("b", 39)},
	}
	for _, c := range ok {
		got, err := Validate(c.in)
		require.NoError(t, err, c.in)
		require.Equal(t, c.out, got)
	}
	bad := []string{
		"",
		"a" + strings.Repeat("b", 40), // 41 characters
		"2notes", "-notes", "no tes", "notes_", "notés", "no.tes",
		"partyline", "all", "everyone", "here", "owner", "Partyline", "OWNER",
	}
	for _, in := range bad {
		_, err := Validate(in)
		require.ErrorIs(t, err, ErrInvalid, in)
	}
}

// AID-03: the same base twice gives name, name-2, name-3; taken checks ignore case and a
// reserved base moves on to the next free suffix.
func TestAllocate(t *testing.T) {
	taken := map[string]bool{}
	use := func(h string) bool { return taken[strings.ToLower(h)] }
	for _, want := range []string{"notes", "notes-2", "notes-3"} {
		got := Allocate(use, "notes")
		require.Equal(t, want, got)
		taken[got] = true
	}
	require.Equal(t, "atlas-2", Allocate(func(h string) bool { return strings.EqualFold(h, "ATLAS") }, "atlas"))
	require.Equal(t, "partyline-2", Allocate(func(string) bool { return false }, "partyline"))

	// A 36-byte base with a 4-digit suffix is cut so the handle stays within 40 characters.
	base := strings.Repeat("x", 36)
	busy := func(h string) bool {
		if h == base {
			return true
		}
		n, err := strconv.Atoi(strings.TrimPrefix(h, base+"-"))
		return strings.HasPrefix(h, base+"-") && err == nil && n < 1000
	}
	require.Equal(t, strings.Repeat("x", 35)+"-1000", Allocate(busy, base))
}

// A base that is not shaped like a handle is allocated from its default AI handle.
func TestAllocateNormalisesTheBase(t *testing.T) {
	free := func(string) bool { return false }
	require.Equal(t, "notes", Allocate(free, "Notes"))
	require.Equal(t, "ai-2024-notes", Allocate(free, "2024 notes"))
	require.Equal(t, "ai", Allocate(free, ""))
}

// DisplayName: first rune upper-cased, control characters dropped, 64 code points at most.
func TestDisplayNameBounds(t *testing.T) {
	require.Equal(t, "Notes", DisplayName("notes"))
	require.Equal(t, "Ab", DisplayName("a\x00\tb"))
	require.Equal(t, "Ab", DisplayName("a\xffb"), "invalid UTF-8 is dropped")
	long := strings.Repeat("é", 70)
	got := DisplayName(long)
	require.Equal(t, 64, len([]rune(got)))
	require.Equal(t, "É", string([]rune(got)[0]))
}

// ORG-01: the address of an organization (its slug) derives from a name or a username with the
// handle slug rules; it may start with a digit, and "org" stands in when no Latin letter or digit is
// left.
func TestOrgSlug(t *testing.T) {
	cases := []struct{ in, want, why string }{
		{"Acme", "acme", "lower-cased"},
		{"Acme Inc.", "acme-inc", "runs of other characters become one hyphen"},
		{"Ágata Ção", "agata-cao", "common accents fold"},
		{"42 Labs", "42-labs", "unlike a handle it may start with a digit"},
		{"Bob_", "bob", "a trailing separator is trimmed"},
		{"--  --", "org", "nothing left"},
		{"李雷", "org", "no Latin letter left"},
		{"", "org", "empty"},
	}
	for _, c := range cases {
		require.Equal(t, c.want, OrgSlug(c.in), c.why)
	}
	require.Equal(t, strings.Repeat("a", 36), OrgSlug(strings.Repeat("a", 50)), "cut to 36 bytes so a -NNN suffix fits")
}

// ORG-01, OUPG-01: two organizations named alike get acme, acme-2, acme-3; taken checks ignore case,
// and a suffix that would pass 40 characters cuts the stem. Unlike a handle, "all" and "partyline"
// are not reserved.
func TestAllocateSlug(t *testing.T) {
	taken := map[string]bool{}
	use := func(s string) bool { return taken[strings.ToLower(s)] }
	for _, want := range []string{"acme", "acme-2", "acme-3"} {
		got := AllocateSlug(use, "Acme")
		require.Equal(t, want, got)
		taken[got] = true
	}
	require.Equal(t, "acme-2", AllocateSlug(func(s string) bool { return strings.EqualFold(s, "ACME") }, "acme"))
	require.Equal(t, "all", AllocateSlug(func(string) bool { return false }, "all"))
	require.Equal(t, "partyline", AllocateSlug(func(string) bool { return false }, "partyline"))
	require.Equal(t, "42-labs", AllocateSlug(func(string) bool { return false }, "42 Labs"), "the base is normalised")
	require.Equal(t, "org", AllocateSlug(func(string) bool { return false }, "李雷"))

	base := strings.Repeat("x", 36)
	busy := func(s string) bool {
		if s == base {
			return true
		}
		n, err := strconv.Atoi(strings.TrimPrefix(s, base+"-"))
		return strings.HasPrefix(s, base+"-") && err == nil && n < 1000
	}
	got := AllocateSlug(busy, base)
	require.Equal(t, strings.Repeat("x", 35)+"-1000", got)
	require.LessOrEqual(t, len(got), 40)
}
