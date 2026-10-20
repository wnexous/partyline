package validation

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// REACT-05: a reaction is exactly one emoji grapheme: an Extended_Pictographic character
// (optionally with VS16 or a skin-tone modifier; text-style symbols such as ©, ™, ‼, arrows
// and ❤ need one of them), ZWJ sequences of at most 4 of those (at most 11 code points),
// keycaps and regional-indicator flags. Letters, digits or punctuation alone, symbols that are
// not emoji, text-style symbols without VS16, longer chains and several graphemes are refused.
func TestOneEmoji(t *testing.T) {
	accept := map[string]string{
		"pictographic":              "👍",
		"text-default with VS16":    "❤️",
		"copyright with VS16":       "©️",
		"skin tone":                 "👍🏽",
		"ZWJ family":                "👩‍👩‍👧‍👦",
		"ZWJ with skin tones":       "👩🏻‍💻",
		"ZWJ with VS16":             "🏳️‍🌈",
		"keycap digit":              "1️⃣",
		"keycap without VS16":       "1⃣",
		"keycap hash":               "#️⃣",
		"flag":                      "🇧🇷",
		"newer pictographic":        "🫠",
		"emoji in misc symbols":     "☕",
		"emoji in dingbats":         "✅",
		"emoji in arrows":           "↩️",
		"emoji in supplemental":     "🥳",
		"emoji presentation clock":  "⌚",
		"text-style with skin tone": "☝🏽",
		"registered with VS16":      "®️",
		"trade mark with VS16":      "™️",
		"double exclamation VS16":   "‼️",
		"arrow with VS16":           "↔️",
		"exactly 11 code points":    "👩🏻‍❤️‍💋️‍👨🏼",
	}
	require.Len(t, []rune(accept["exactly 11 code points"]), 11)
	for name, s := range accept {
		require.True(t, OneEmoji(s), "%s %q", name, s)
	}
	reject := map[string]string{
		"empty":                    "",
		"letter":                   "a",
		"word":                     "ok",
		"accented letter":          "é",
		"digit alone":              "1",
		"digit with VS16 only":     "1\ufe0f",
		"hash alone":               "#",
		"punctuation":              "!",
		"angle brackets":           "<>",
		"math symbol":              "∑",
		"space":                    " ",
		"two emoji":                "👍👍",
		"emoji and letter":         "👍a",
		"emoji space emoji":        "👍 👍",
		"one regional indicator":   "🇧",
		"three regional indicator": "🇧🇷🇦",
		"lone skin tone":           "🏽",
		"lone VS16":                "\ufe0f",
		"lone ZWJ":                 "\u200d",
		"trailing ZWJ":             "👩‍",
		"leading ZWJ":              "\u200d👩",
		"double ZWJ":               "👩\u200d\u200d💻",
		"ZWJ to a letter":          "👩‍a",
		"keycap on a letter":       "a⃣",
		"text presentation":        "❤\ufe0e",
		"too long":                 strings.Repeat("👩‍", 8) + "👩",
		"heart without VS16":       "❤",
		"copyright without VS16":   "©",
		"registered without VS16":  "®",
		"trade mark without VS16":  "™",
		"double exclamation":       "‼",
		"arrow without VS16":       "↔",
		"return arrow without VS":  "↩",
		"text-style in a ZWJ":      "❤\u200d🔥",
		"five-person chain":        "👨‍👩‍👧‍👦‍👦",
		"eight-person chain":       "❤️" + strings.Repeat("\u200d👩", 7),
	}
	require.Len(t, []rune(reject["eight-person chain"]), 16)
	for name, s := range reject {
		require.False(t, OneEmoji(s), "%s %q", name, s)
	}
}
