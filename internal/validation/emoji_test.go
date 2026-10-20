package validation

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// REACT-05: a reaction is exactly one emoji grapheme: an Extended_Pictographic character
// (optionally with VS16 or a skin-tone modifier), ZWJ sequences of those, keycaps and
// regional-indicator flags. Letters, digits or punctuation alone, symbols that are not emoji
// and several graphemes are refused.
func TestOneEmoji(t *testing.T) {
	accept := map[string]string{
		"pictographic":             "👍",
		"text-default with VS16":   "❤️",
		"text-default without VS":  "❤",
		"copyright with VS16":      "©️",
		"skin tone":                "👍🏽",
		"ZWJ family":               "👩‍👩‍👧‍👦",
		"ZWJ with skin tones":      "👩🏻‍💻",
		"ZWJ with VS16":            "🏳️‍🌈",
		"keycap digit":             "1️⃣",
		"keycap without VS16":      "1⃣",
		"keycap hash":              "#️⃣",
		"flag":                     "🇧🇷",
		"newer pictographic":       "🫠",
		"emoji in misc symbols":    "☕",
		"emoji in dingbats":        "✅",
		"emoji in arrows":          "↩️",
		"emoji in supplemental":    "🥳",
		"emoji presentation clock": "⌚",
	}
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
	}
	for name, s := range reject {
		require.False(t, OneEmoji(s), "%s %q", name, s)
	}
}
