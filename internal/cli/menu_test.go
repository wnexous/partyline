package cli

import (
	"bytes"
	"io"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/term"
)

var (
	ansi     = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)
	cursorUp = regexp.MustCompile(`\x1b\[[0-9]+A`)
)

func testItems() []MenuItem {
	return []MenuItem{
		{Title: "Claude Code", Installed: true, Registered: false, Selected: true},
		{Title: "OpenCode", Installed: true, Registered: true},
		{Title: "pi", Installed: false},
	}
}

func runMenu(t *testing.T, items []MenuItem, keys string, width int) ([]bool, bool, string) {
	t.Helper()
	var out bytes.Buffer
	m := Menu{Items: items, In: strings.NewReader(keys), Out: &out, Width: func() int { return width }}
	selected, ok, err := m.Run()
	require.NoError(t, err)
	return selected, ok, out.String()
}

func TestMenuKeys(t *testing.T) {
	tests := []struct {
		name string
		keys string
		want []bool
	}{
		{"space toggles the first item", " \r", []bool{false, false, false}},
		{"newline applies", "\n", []bool{true, false, false}},
		{"j moves down", "j \r", []bool{true, true, false}},
		{"k moves up and clamps at the top", "k \r", []bool{false, false, false}},
		{"j clamps at the bottom", "jjj \r", []bool{true, false, true}},
		{"down arrow moves down", "\x1b[B \r", []bool{true, true, false}},
		{"up arrow moves up", "\x1b[B\x1b[A \r", []bool{false, false, false}},
		{"j and k move", "jj\r", []bool{true, false, false}},
		{"a selects every installed item and no other", "a\r", []bool{true, true, false}},
		{"a again clears them", "aa\r", []bool{false, false, false}},
		{"unknown keys are ignored", "xz\x1b[C \r", []bool{false, false, false}},
		{"unknown escape sequences are ignored", "\x1b[1~\r", []bool{true, false, false}},
		{"escape followed by a non-bracket byte is ignored", "\x1bX \r", []bool{false, false, false}},
		{"escape with an unknown final byte is ignored", "\x1b[Z \r", []bool{false, false, false}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selected, ok, _ := runMenu(t, testItems(), tt.keys, 80)
			require.True(t, ok)
			require.Equal(t, tt.want, selected)
		})
	}
}

// CLI-03 (L-045): `a` only touches the installed items: an item that is not installed would fail
// to register, and one that is registered without being installed keeps its state, so a is never what
// removes Partyline from it.
func TestMenuAllLeavesItemsThatAreNotInstalledAsTheyWere(t *testing.T) {
	items := []MenuItem{
		{Title: "Claude Code", Installed: true},
		{Title: "OpenCode"},
		{Title: "pi", Registered: true, Selected: true},
	}
	selected, ok, _ := runMenu(t, items, "a\r", 80)
	require.True(t, ok)
	require.Equal(t, []bool{true, false, true}, selected, "the installed item is checked; the others stay as they were")

	selected, _, _ = runMenu(t, items, "aa\r", 80)
	require.Equal(t, []bool{false, false, true}, selected, "a again clears the installed items only")

	selected, _, _ = runMenu(t, []MenuItem{{Title: "OpenCode"}, {Title: "pi", Registered: true, Selected: true}}, "a\r", 80)
	require.Equal(t, []bool{false, true}, selected, "nothing is installed: a changes nothing")
}

func TestMenuCancelKeepsSelection(t *testing.T) {
	// q and Ctrl+C are followed by enter: a menu that ignored them would apply instead. A lone ESC
	// stays last, because ESC followed by another byte is read as an escape sequence.
	for _, keys := range []string{"q\r", "\x1b", "\x03\r"} {
		selected, ok, out := runMenu(t, testItems(), keys, 80)
		require.False(t, ok)
		require.Nil(t, selected)
		require.True(t, strings.HasPrefix(out, cursorHide))
		require.True(t, strings.HasSuffix(out, cursorShow))
	}
}

// Ctrl+C in raw mode arrives as byte 0x03 and cancels like q, with the terminal restored.
func TestMenuCtrlCCancelsAndRestoresTheTerminal(t *testing.T) {
	fake := &fakeRawTerminal{terminal: true}
	var out bytes.Buffer
	m := Menu{Items: testItems(), In: strings.NewReader(" \x03\r"), Out: &out, Width: func() int { return 80 }, raw: fake}
	selected, ok, err := m.Run()
	require.NoError(t, err)
	require.False(t, ok)
	require.Nil(t, selected)
	require.True(t, fake.madeRaw)
	require.True(t, fake.restored)
	require.True(t, strings.HasSuffix(out.String(), cursorShow))
}

func TestMenuCancelAfterChangesKeepsOriginal(t *testing.T) {
	selected, ok, _ := runMenu(t, testItems(), " a q", 80)
	require.False(t, ok)
	require.Nil(t, selected)
}

func TestMenuEndOfInputCancels(t *testing.T) {
	selected, ok, out := runMenu(t, testItems(), "", 80)
	require.False(t, ok)
	require.Nil(t, selected)
	require.True(t, strings.HasPrefix(out, cursorHide))
	require.True(t, strings.HasSuffix(out, cursorShow))
}

func TestMenuLoneEscapeCancelsWithoutEndOfInput(t *testing.T) {
	reader, writer := io.Pipe()
	defer writer.Close()
	var out bytes.Buffer
	m := Menu{Items: testItems(), In: reader, Out: &out, Width: func() int { return 80 }}

	type result struct {
		selected []bool
		ok       bool
		err      error
	}
	done := make(chan result, 1)
	go func() {
		selected, ok, err := m.Run()
		done <- result{selected, ok, err}
	}()
	_, err := writer.Write([]byte{0x1b})
	require.NoError(t, err)

	select {
	case res := <-done:
		require.NoError(t, res.err)
		require.False(t, res.ok)
		require.Nil(t, res.selected)
	case <-time.After(10 * escapeDelay):
		t.Fatal("the menu did not cancel a lone escape without further input")
	}
	require.True(t, strings.HasSuffix(out.String(), cursorShow))
}

func TestMenuRendersRowsAndPreSelection(t *testing.T) {
	_, ok, out := runMenu(t, testItems(), "\r", 80)
	require.True(t, ok)
	rows := menuRows(out)
	require.Len(t, rows, 3)
	require.Equal(t, "> [x] Claude Code  installed · not registered", rows[0])
	require.Equal(t, "  [ ] OpenCode  installed · registered", rows[1])
	require.Equal(t, "  [ ] pi  not installed · not registered", rows[2])
}

func TestMenuRendersEveryInstallationState(t *testing.T) {
	items := []MenuItem{
		{Title: "Both", Installed: true, Registered: true, Selected: true},
		{Title: "Installed only", Installed: true},
		{Title: "Registered only", Registered: true},
		{Title: "Neither"},
	}
	_, ok, out := runMenu(t, items, "\r", 80)
	require.True(t, ok)
	require.Equal(t, []string{
		"> [x] Both  installed · registered",
		"  [ ] Installed only  installed · not registered",
		"  [ ] Registered only  not installed · registered",
		"  [ ] Neither  not installed · not registered",
	}, menuRows(out))
}

func TestMenuRedrawsWithANSISequences(t *testing.T) {
	_, ok, out := runMenu(t, testItems(), "j\r", 80)
	require.True(t, ok)
	require.Contains(t, out, "\x1b[3A")
	require.Contains(t, out, "\x1b[2K")
	rows := menuRows(out)
	require.Len(t, rows, 3)
	require.True(t, strings.HasPrefix(rows[1], "> "))
}

func TestMenuNarrowWidthOneRowPerItem(t *testing.T) {
	items := []MenuItem{
		{Title: "Claude Code", Installed: true, Registered: true, Selected: true},
		{Title: "OpenCode", Installed: true, Registered: false},
		{Title: "OpenCode", Installed: false, Registered: false},
	}
	_, ok, out := runMenu(t, items, "\r", 30)
	require.True(t, ok)
	rows := menuRows(out)
	require.Len(t, rows, len(items))
	require.True(t, strings.HasPrefix(rows[0], "> [x] Claude Code"))
	require.True(t, strings.HasPrefix(rows[1], "  [ ] OpenCode"))
	require.True(t, strings.HasPrefix(rows[2], "  [ ] OpenCode"))
	for _, row := range rows {
		require.LessOrEqual(t, len([]rune(row)), 30, "row %q is wider than the terminal", row)
		require.Contains(t, row, "…", "row %q should be truncated", row)
	}
}

func TestMenuCursorHiddenAndRestored(t *testing.T) {
	for _, keys := range []string{"\r", "q", "\x1b"} {
		_, _, out := runMenu(t, testItems(), keys, 80)
		require.True(t, strings.HasPrefix(out, cursorHide))
		require.True(t, strings.HasSuffix(out, cursorShow))
		require.Equal(t, 1, strings.Count(out, cursorHide))
		require.Equal(t, 1, strings.Count(out, cursorShow))
	}
}

func TestMenuRawModeRestored(t *testing.T) {
	for _, keys := range []string{"\r", "q"} {
		fake := &fakeRawTerminal{terminal: true}
		var out bytes.Buffer
		m := Menu{Items: testItems(), In: strings.NewReader(keys), Out: &out, Width: func() int { return 80 }, raw: fake}
		_, ok, err := m.Run()
		require.NoError(t, err)
		require.Equal(t, keys == "\r", ok)
		require.True(t, fake.madeRaw)
		require.True(t, fake.restored)
		require.True(t, strings.HasSuffix(out.String(), cursorShow))
	}
}

func TestMenuRawModeSkippedWithoutTerminal(t *testing.T) {
	fake := &fakeRawTerminal{}
	var out bytes.Buffer
	m := Menu{Items: testItems(), In: strings.NewReader("\r"), Out: &out, Width: func() int { return 80 }, raw: fake}
	_, ok, err := m.Run()
	require.NoError(t, err)
	require.True(t, ok)
	require.False(t, fake.madeRaw)
	require.False(t, fake.restored)
}

type fakeRawTerminal struct {
	terminal          bool
	madeRaw, restored bool
}

func (f *fakeRawTerminal) isTerminal(int) bool { return f.terminal }

func (f *fakeRawTerminal) makeRaw(int) (*term.State, error) {
	f.madeRaw = true
	return &term.State{}, nil
}

func (f *fakeRawTerminal) restore(int, *term.State) error {
	f.restored = true
	return nil
}

func (*fakeRawTerminal) size(int) (int, int, error) { return 80, 24, nil }

// menuRows returns the last frame's rows with the ANSI escape sequences removed.
func menuRows(out string) []string {
	if loc := cursorUp.FindAllStringIndex(out, -1); loc != nil {
		out = out[loc[len(loc)-1][1]:]
	}
	plain := ansi.ReplaceAllString(out, "")
	plain = strings.ReplaceAll(plain, "\r", "")
	lines := strings.Split(plain, "\n")
	var rows []string
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			rows = append(rows, line)
		}
	}
	return rows
}
