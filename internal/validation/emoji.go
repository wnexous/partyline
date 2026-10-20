package validation

import (
	"sort"
	"unicode/utf8"
)

// MaxEmojiLength bounds one emoji in code points: enough for flags, keycaps, skin tones and
// ZWJ sequences such as a family with skin tones.
const MaxEmojiLength = 16

const (
	zwj         = '‍'
	vs16        = '️'
	keycapMark  = '⃣'
	riFirst     = '\U0001F1E6' // regional indicator A
	riLast      = '\U0001F1FF' // regional indicator Z
	toneFirst   = '\U0001F3FB' // skin-tone modifiers
	toneLast    = '\U0001F3FF'
	pictoLowCut = '©' // no Extended_Pictographic code point is below ©
)

// pictographic lists the Extended_Pictographic ranges of Unicode emoji-data (15.1), sorted.
var pictographic = [][2]rune{
	{0x00A9, 0x00A9}, {0x00AE, 0x00AE}, {0x203C, 0x203C}, {0x2049, 0x2049}, {0x2122, 0x2122},
	{0x2139, 0x2139}, {0x2194, 0x2199}, {0x21A9, 0x21AA}, {0x231A, 0x231B}, {0x2328, 0x2328},
	{0x2388, 0x2388}, {0x23CF, 0x23CF}, {0x23E9, 0x23F3}, {0x23F8, 0x23FA}, {0x24C2, 0x24C2},
	{0x25AA, 0x25AB}, {0x25B6, 0x25B6}, {0x25C0, 0x25C0}, {0x25FB, 0x25FE}, {0x2600, 0x2605},
	{0x2607, 0x2612}, {0x2614, 0x2685}, {0x2690, 0x2705}, {0x2708, 0x2712}, {0x2714, 0x2714},
	{0x2716, 0x2716}, {0x271D, 0x271D}, {0x2721, 0x2721}, {0x2728, 0x2728}, {0x2733, 0x2734},
	{0x2744, 0x2744}, {0x2747, 0x2747}, {0x274C, 0x274C}, {0x274E, 0x274E}, {0x2753, 0x2755},
	{0x2757, 0x2757}, {0x2763, 0x2767}, {0x2795, 0x2797}, {0x27A1, 0x27A1}, {0x27B0, 0x27B0},
	{0x27BF, 0x27BF}, {0x2934, 0x2935}, {0x2B05, 0x2B07}, {0x2B1B, 0x2B1C}, {0x2B50, 0x2B50},
	{0x2B55, 0x2B55}, {0x3030, 0x3030}, {0x303D, 0x303D}, {0x3297, 0x3297}, {0x3299, 0x3299},
	{0x1F000, 0x1F0FF}, {0x1F10D, 0x1F10F}, {0x1F12F, 0x1F12F}, {0x1F16C, 0x1F171},
	{0x1F17E, 0x1F17F}, {0x1F18E, 0x1F18E}, {0x1F191, 0x1F19A}, {0x1F1AD, 0x1F1E5},
	{0x1F201, 0x1F20F}, {0x1F21A, 0x1F21A}, {0x1F22F, 0x1F22F}, {0x1F232, 0x1F23A},
	{0x1F23C, 0x1F23F}, {0x1F249, 0x1F3FA}, {0x1F400, 0x1F53D}, {0x1F546, 0x1F64F},
	{0x1F680, 0x1F6FF}, {0x1F774, 0x1F77F}, {0x1F7D5, 0x1F7FF}, {0x1F80C, 0x1F80F},
	{0x1F848, 0x1F84F}, {0x1F85A, 0x1F85F}, {0x1F888, 0x1F88F}, {0x1F8AE, 0x1F8FF},
	{0x1F90C, 0x1F93A}, {0x1F93C, 0x1F945}, {0x1F947, 0x1FAFF}, {0x1FC00, 0x1FFFD},
}

func isPictographic(r rune) bool {
	if r < pictoLowCut {
		return false
	}
	i := sort.Search(len(pictographic), func(i int) bool { return pictographic[i][1] >= r })
	return i < len(pictographic) && pictographic[i][0] <= r
}

func isRegionalIndicator(r rune) bool { return r >= riFirst && r <= riLast }

func isSkinTone(r rune) bool { return r >= toneFirst && r <= toneLast }

func isKeycapBase(r rune) bool { return (r >= '0' && r <= '9') || r == '#' || r == '*' }

// OneEmoji reports whether s is exactly one emoji grapheme (REACT-05): a keycap ([0-9#*],
// optional VS16, U+20E3), a flag (two regional indicators), or a ZWJ sequence of
// Extended_Pictographic characters, each optionally followed by VS16 or a skin-tone
// modifier. Anything else, including letters, digits alone and several emoji, is refused.
func OneEmoji(s string) bool {
	rs := []rune(s)
	if len(rs) == 0 || len(rs) > MaxEmojiLength || !utf8.ValidString(s) {
		return false
	}
	switch {
	case isKeycapBase(rs[0]):
		return (len(rs) == 2 && rs[1] == keycapMark) || (len(rs) == 3 && rs[1] == vs16 && rs[2] == keycapMark)
	case isRegionalIndicator(rs[0]):
		return len(rs) == 2 && isRegionalIndicator(rs[1])
	}
	for i := 0; ; {
		if !isPictographic(rs[i]) {
			return false
		}
		i++
		if i < len(rs) && (rs[i] == vs16 || isSkinTone(rs[i])) {
			i++
		}
		if i == len(rs) {
			return true
		}
		if rs[i] != zwj || i+1 == len(rs) {
			return false
		}
		i++
	}
}
