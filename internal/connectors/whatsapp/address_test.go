package whatsapp

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/connectors"
)

// oneNumber is the config of a connection of the instance acme.
var oneNumber = connectors.Config{"base_url": "https://evolution.example.test", "instances": []string{"acme"}}

// CID-31: the operator may write to a WhatsApp address no chat has been seen at: a phone number with
// its country code (digits only, a leading plus allowed), a personal JID (the device suffix is not
// part of it), a LID or a group JID. Each names the chat the way messages from it do, on the
// connection's own instance, so the person's first answer lands in the same chat; a number is titled
// with its last four digits, as a DM without a push name is (CTX-06).
func TestAddressAcceptsWhatsAppAddresses(t *testing.T) {
	for raw, want := range map[string]connectors.ChatInfo{
		"5541999990000":                   {ID: "acme|5541999990000@s.whatsapp.net", Title: "+•••• 0000", Kind: "dm"},
		"+5541999990000":                  {ID: "acme|5541999990000@s.whatsapp.net", Title: "+•••• 0000", Kind: "dm"},
		"  5541999990000\n":               {ID: "acme|5541999990000@s.whatsapp.net", Title: "+•••• 0000", Kind: "dm"},
		"5541999990000@s.whatsapp.net":    {ID: "acme|5541999990000@s.whatsapp.net", Title: "+•••• 0000", Kind: "dm"},
		"5541999990000:12@s.whatsapp.net": {ID: "acme|5541999990000@s.whatsapp.net", Title: "+•••• 0000", Kind: "dm"},
		"1234567":                         {ID: "acme|1234567@s.whatsapp.net", Title: "+•••• 4567", Kind: "dm"},
		"100000000000001@lid":             {ID: "acme|100000000000001@lid", Kind: "dm"},
		"120363000000000001@g.us":         {ID: "acme|120363000000000001@g.us", Kind: "group"},
		"5511999990000-1600000000@g.us":   {ID: "acme|5511999990000-1600000000@g.us", Kind: "group"},
		"120363000000000001@g.us ":        {ID: "acme|120363000000000001@g.us", Kind: "group"},
	} {
		got, ok := address(oneNumber, raw)
		require.True(t, ok, "%q", raw)
		require.Equal(t, want, got, "%q", raw)
	}
}

// CID-31: what is no WhatsApp address is refused, so the operator is told the chat is unknown instead
// of a stranger being written to: no digits, too few or too many, separators inside a number, a
// server that is not a chat (status, broadcast, newsletter), a JID with more than one @, a chat id of
// Partyline and the id of a chat as the connector keeps it.
func TestAddressRefusesWhatWhatsAppCannotAddress(t *testing.T) {
	for _, raw := range []string{
		"", " ", "hello", "+", "++5541999990000", "55+41999990000", "123456", "5541999990000123456", "551199999000012345@s.whatsapp.net",
		"5541 99999 0000", "(54) 1999-9900", "55-41999990000", "5541.99999.0000", "٥٥٤١٩٩٩٩٩٠٠٠٠",
		"abc@s.whatsapp.net", "@s.whatsapp.net", "5541999990000@", "5541999990000@s.whatsapp.net@x",
		"status@broadcast", "5541999990000@broadcast", "5541999990000@newsletter", "5541999990000@call", "5541999990000@hosted",
		"120363000000000001@G.US", "5541999990000@S.WHATSAPP.NET", "acme|5541999990000@s.whatsapp.net",
		"0198c2f4-7a3b-7c3e-9b1d-2f4a5b6c7d8e", "g.us", "@g.us", "-1@g.us", "12-@g.us", "12-34-56@g.us",
		"5541999990000\x00", "5541999990000\n5541999990001",
	} {
		_, ok := address(oneNumber, raw)
		require.False(t, ok, "%q", raw)
	}
}

// CID-04, CID-31: one connection is one number, so an address is only taken for a connection that
// names exactly one instance; with none or several it cannot say whose number would write.
func TestAddressNeedsExactlyOneInstance(t *testing.T) {
	for name, instances := range map[string][]string{"none": nil, "several": {"acme", "other"}} {
		_, ok := address(connectors.Config{"instances": instances}, "5541999990000")
		require.False(t, ok, name)
	}
	_, ok := address(connectors.Config{}, "5541999990000")
	require.False(t, ok, "no instances field")
}

// CID-31: the kind offers the address rule, which is what lets the operator start a chat.
func TestKindAcceptsNewChats(t *testing.T) {
	require.NotNil(t, Kind().Address)
}
