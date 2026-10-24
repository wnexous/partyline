package whatsapp

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// CID-30: a vCard gives the name of the contact and the numbers of its TEL lines. The number is the
// WhatsApp id when the line has one (waid, digits only, international) and otherwise the number as
// written with only its digits and a leading plus left, in the order of the card, once each.
func TestParseVCard(t *testing.T) {
	for name, tc := range map[string]struct {
		card        string
		wantName    string
		wantNumbers []string
	}{
		"a labelled number":     {cardAna, "Ana Test", []string{"+5511999990000"}},
		"two, CRLF, empty waid": {cardBruno, "Bruno Test", []string{"+5521988880000", "2125550000"}},
		"no number": {
			"BEGIN:VCARD\nVERSION:3.0\nFN:No Phone\nEND:VCARD", "No Phone", nil,
		},
		"nothing": {"", "", nil},
		"name from N when there is no FN": {
			"BEGIN:VCARD\nN:Test;Carla;;Dr.;\nTEL:+5531977770000\nEND:VCARD", "Carla Test", []string{"+5531977770000"},
		},
		"lower case, parameters of vCard 2.1": {
			"begin:vcard\nfn:Dani Test\ntel;cell;voice:+55 41 97777-0000\nend:vcard", "Dani Test", []string{"+5541977770000"},
		},
		"a folded line": {
			"BEGIN:VCARD\nFN:Edu\n Test\nTEL;waid=55519\n 99990000:+55 51\n 99999-0000\nEND:VCARD", "EduTest", []string{"+5551999990000"},
		},
		"the same number twice": {
			"BEGIN:VCARD\nFN:Fabi\nTEL;waid=5561999990000:+55 61 99999-0000\nTEL:+55 61 99999-0000\nTEL;type=CELL:+556199999-0000\nEND:VCARD", "Fabi", []string{"+5561999990000"},
		},
		"a number with a word in it is kept as written": {
			"BEGIN:VCARD\nFN:Gus\nTEL:+55 71 3333-0000 ext. 12\nTEL:*123\nEND:VCARD", "Gus", []string{"+55 71 3333-0000 ext. 12", "*123"},
		},
		"a quoted parameter with a colon": {
			"BEGIN:VCARD\nFN:Hel\nTEL;X-LABEL=\"work: main\";waid=5581999990000:+55 81 99999-0000\nEND:VCARD", "Hel", []string{"+5581999990000"},
		},
		"other properties are not numbers": {
			"BEGIN:VCARD\nFN:Iris\nEMAIL:iris@example.test\nX-TELEGRAM:5551234\nURL:https://example.test/tel:+5511111111\nEND:VCARD", "Iris", nil,
		},
	} {
		t.Run(name, func(t *testing.T) {
			card := parseVCard(tc.card)
			require.Equal(t, tc.wantName, card.Name)
			require.Equal(t, tc.wantNumbers, card.Numbers)
		})
	}
}

// CID-30: a card from a stranger is data of any size and shape: the numbers kept are bounded, a long
// line is cut, and a line that is no property or a card made of garbage gives nothing.
func TestParseVCardIsBounded(t *testing.T) {
	var card strings.Builder
	card.WriteString("BEGIN:VCARD\nFN:Many\n")
	for i := range 500 {
		card.WriteString("TEL:+55119" + strings.Repeat("9", 4) + string(rune('0'+i%10)) + strings.Repeat("0", i%7) + "\n")
	}
	got := parseVCard(card.String())
	require.LessOrEqual(t, len(got.Numbers), maxNumbers)
	require.Len(t, got.Numbers, maxNumbers, "the first ones are kept")

	long := parseVCard("FN:" + strings.Repeat("n", 5000) + "\nTEL:" + strings.Repeat("9", 5000))
	require.LessOrEqual(t, len([]rune(long.Name)), maxContactField)
	require.LessOrEqual(t, len([]rune(long.Numbers[0])), maxContactField)

	for _, garbage := range []string{"\x00\x01\x02", ":::;;;", "TEL", "TEL;", "TEL:", ";TEL:+5511999990000", "FN\nTEL\n:"} {
		got := parseVCard(garbage)
		require.Empty(t, got.Numbers, "%q", garbage)
	}
}

// CID-30: names and numbers are text of a stranger on one line of the event: white space and control
// characters become single spaces, whatever they are.
func TestParseVCardCleansWhatItKeeps(t *testing.T) {
	got := parseVCard("BEGIN:VCARD\nFN:Jo\x00 \u202eTest\u2028  Name\nTEL:*1\u200b23\x1b[1m\nEND:VCARD")
	require.Equal(t, "Jo Test Name", got.Name)
	require.Equal(t, []string{"*1 23 [1m"}, got.Numbers)
}
