package cli

import (
	"bufio"
	"fmt"
	"io"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/term"
)

// MenuItem is one row of a Menu.
type MenuItem struct {
	// Title is the item's display name.
	Title string
	// Installed reports whether the item exists on this machine.
	Installed bool
	// Registered reports whether Partyline is already registered for the item.
	Registered bool
	// Selected is the initial selection.
	Selected bool
}

// rawTerminal is the terminal operations the menu needs. The real implementation wraps
// golang.org/x/term; tests inject a fake so no terminal is needed.
type rawTerminal interface {
	isTerminal(fd int) bool
	makeRaw(fd int) (*term.State, error)
	restore(fd int, state *term.State) error
	size(fd int) (width, height int, err error)
}

// osRawTerm is the rawTerminal backed by golang.org/x/term.
type osRawTerm struct{}

func (osRawTerm) isTerminal(fd int) bool { return term.IsTerminal(fd) }

func (osRawTerm) makeRaw(fd int) (*term.State, error) { return term.MakeRaw(fd) }

func (osRawTerm) restore(fd int, state *term.State) error { return term.Restore(fd, state) }

func (osRawTerm) size(fd int) (int, int, error) { return term.GetSize(fd) }

// Menu is a terminal checklist. It reads raw keys from In, draws to Out and asks Width
// how many columns it may use.
type Menu struct {
	Items []MenuItem
	In    io.Reader
	Out   io.Writer
	// Width returns the terminal width; nil means 80 columns.
	Width func() int

	// raw, when set, puts the terminal in raw mode while the menu is open.
	raw  rawTerminal
	inFD int
}

const (
	escapeDelay = 50 * time.Millisecond
	minWidth    = 20
	cursorHide  = "\x1b[?25l"
	cursorShow  = "\x1b[?25h"
)

// Run shows the menu until the user applies or cancels. It returns the selection and
// whether the user applied (ok). A cancel leaves every item as it was and returns ok
// false.
func (m Menu) Run() ([]bool, bool, error) {
	restore, err := m.enterRaw()
	if err != nil {
		return nil, false, err
	}
	if restore != nil {
		defer restore()
	}
	fmt.Fprint(m.Out, cursorHide)
	defer fmt.Fprint(m.Out, cursorShow)

	selected := make([]bool, len(m.Items))
	for i, item := range m.Items {
		selected[i] = item.Selected
	}
	active := 0
	drawn := m.draw(selected, active, 0)
	in := newKeyInput(m.In)
	defer in.stop()
	for {
		k := readMenuKey(in)
		if k == keyEOF {
			return nil, false, nil
		}
		switch k {
		case keyUp:
			if active > 0 {
				active--
			}
		case keyDown:
			if active < len(m.Items)-1 {
				active++
			}
		case keyToggle:
			if active < len(selected) {
				selected[active] = !selected[active]
			}
		case keyAll:
			// Only installed items: one that is not installed cannot be registered, and one that is
			// registered without being installed keeps its state.
			all, installed := true, false
			for i, item := range m.Items {
				if item.Installed {
					installed = true
					all = all && selected[i]
				}
			}
			if !installed {
				continue
			}
			for i, item := range m.Items {
				if item.Installed {
					selected[i] = !all
				}
			}
		case keyApply:
			return selected, true, nil
		case keyCancel:
			return nil, false, nil
		case keyUnknown:
			continue
		}
		drawn = m.draw(selected, active, drawn)
	}
}

// enterRaw puts the input terminal in raw mode and returns the function that restores it.
func (m Menu) enterRaw() (func(), error) {
	if m.raw == nil || !m.raw.isTerminal(m.inFD) {
		return nil, nil
	}
	state, err := m.raw.makeRaw(m.inFD)
	if err != nil {
		return nil, err
	}
	return func() { _ = m.raw.restore(m.inFD, state) }, nil
}

// draw clears the rows of the previous frame, prints the current one and returns how
// many rows it printed.
func (m Menu) draw(selected []bool, active, previous int) int {
	if previous > 0 {
		fmt.Fprintf(m.Out, "\x1b[%dA", previous)
	}
	width := m.width()
	for i, item := range m.Items {
		fmt.Fprintf(m.Out, "\x1b[2K%s\r\n", menuRow(item, i == active, i < len(selected) && selected[i], width))
	}
	return len(m.Items)
}

func (m Menu) width() int {
	w := 80
	if m.Width != nil {
		w = m.Width()
	}
	if w < minWidth {
		w = minWidth
	}
	return w
}

// menuRow renders one row, truncating the title (and, when the terminal is very narrow,
// the rest) so the row fits width columns.
func menuRow(item MenuItem, active, selected bool, width int) string {
	marker := "  "
	if active {
		marker = "> "
	}
	check := "[ ]"
	if selected {
		check = "[x]"
	}
	prefix := marker + check + " "
	state := "not installed · not registered"
	switch {
	case item.Installed && item.Registered:
		state = "installed · registered"
	case item.Installed:
		state = "installed · not registered"
	case item.Registered:
		state = "not installed · registered"
	}
	suffix := "  " + state
	avail := width - utf8.RuneCountInString(prefix) - utf8.RuneCountInString(suffix)
	if avail >= 1 {
		return prefix + truncate(item.Title, avail) + suffix
	}
	return prefix + truncate(item.Title+suffix, width-utf8.RuneCountInString(prefix))
}

// truncate cuts s to at most w runes, ending with … when it was longer.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	runes := []rune(s)
	return string(runes[:w-1]) + "…"
}

type menuKey int

const (
	keyUnknown menuKey = iota
	keyUp
	keyDown
	keyToggle
	keyAll
	keyApply
	keyCancel
	keyEOF
)

// keyInput reads bytes from one goroutine so an escape sequence can wait for more
// bytes without racing the caller for the reader.
type keyInput struct {
	ch   chan byte
	done chan struct{}
	once sync.Once
}

func newKeyInput(in io.Reader) *keyInput {
	k := &keyInput{ch: make(chan byte), done: make(chan struct{})}
	go func() {
		defer close(k.ch)
		r := bufio.NewReader(in)
		for {
			b, err := r.ReadByte()
			if err != nil {
				return
			}
			select {
			case k.ch <- b:
			case <-k.done:
				return
			}
		}
	}()
	return k
}

func (k *keyInput) stop() { k.once.Do(func() { close(k.done) }) }

// next reads one byte, reporting false at the end of the stream.
func (k *keyInput) next() (byte, bool) {
	b, ok := <-k.ch
	return b, ok
}

// nextWithin reads one byte, reporting false when none arrives within d.
func (k *keyInput) nextWithin(d time.Duration) (byte, bool) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case b, ok := <-k.ch:
		return b, ok
	case <-timer.C:
		return 0, false
	}
}

// readMenuKey decodes one key from the raw byte stream.
func readMenuKey(in *keyInput) menuKey {
	b, ok := in.next()
	if !ok {
		return keyEOF
	}
	switch b {
	case 'k':
		return keyUp
	case 'j':
		return keyDown
	case ' ':
		return keyToggle
	case 'a':
		return keyAll
	case '\r', '\n':
		return keyApply
	case 'q', 0x03:
		// 0x03 is Ctrl+C: raw mode delivers it as a byte, so it must cancel like q and
		// restore the terminal.
		return keyCancel
	case 0x1b:
		second, ok := in.nextWithin(escapeDelay)
		if !ok {
			return keyCancel
		}
		if second != '[' {
			return keyUnknown
		}
		third, ok := in.nextWithin(escapeDelay)
		if !ok {
			return keyUnknown
		}
		switch third {
		case 'A':
			return keyUp
		case 'B':
			return keyDown
		}
		return keyUnknown
	}
	return keyUnknown
}
