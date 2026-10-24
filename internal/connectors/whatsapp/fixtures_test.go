package whatsapp

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// Samples of what evolution-go answers, as seen in production (names synthetic). They are the raw
// JSON on purpose: the tests read them the way the connector does.
const (
	// An instance whose number is reconnecting after an outage (GET /instance/all, CID-28).
	reconnectingInstance = `{"name":"acme","connected":false,"disconnect_reason":"Reconnecting","websocketEnable":""}`
	// An instance whose number is open.
	openInstance = `{"name":"atlas","connected":true,"disconnect_reason":"","websocketEnable":"enabled"}`
)

// useInstances makes GET /instance/all answer exactly the given instances, each a raw JSON object.
func (f *fakeEvolution) useInstances(t *testing.T, samples ...string) {
	t.Helper()
	list := make([]map[string]any, len(samples))
	for i, sample := range samples {
		require.NoError(t, json.Unmarshal([]byte(sample), &list[i]), sample)
	}
	f.mu.Lock()
	f.instances = list
	f.mu.Unlock()
}

// An album as WhatsApp sends it and evolution-go reports it (CID-29): a header that only counts, and
// then each photo or video as a message of its own that names the header as its parent. Current
// WhatsApp wraps each item in an associatedChildMessage; an item can also come plain.
const (
	// albumHeader announces two photos and a video; it has no content.
	albumHeader = `{"albumMessage":{"expectedImageCount":2,"expectedVideoCount":1},"messageContextInfo":{"messageSecret":"c3ludGhldGljLXNlY3JldA=="}}`
	// albumParent is the key of the header, which every item names as its parent.
	albumParent = `{"remoteJID":"120363000000000001@g.us","fromMe":false,"ID":"ALBUM","participant":"5511999990000@s.whatsapp.net"}`
)

// albumPhoto is the node of a photo of the album: the caption is on the first one.
func albumPhoto(caption string) string {
	return `{"mimetype":"image/jpeg","fileLength":"2048","caption":` + quote(caption) + `,"mediaKey":"c3ludGhldGljLWtleQ=="}`
}

// association is the messageContextInfo that ties a message to the album header, with the type
// as evolution-go writes it: a number (1), or the name of the enum.
func association(typ string) string {
	return `"messageContextInfo":{"messageAssociation":{"associationType":` + typ + `,"parentMessageKey":` + albumParent + `,"messageIndex":1}}`
}

// The three shapes an item takes: plain, wrapped with the association beside the wrapper, and
// wrapped with the association inside it.
func plainItem(node, typ string) string { return `{` + node + `,` + association(typ) + `}` }
func wrappedItem(node, typ string) string {
	return `{"associatedChildMessage":{"message":{` + node + `}},` + association(typ) + `}`
}
func wrappedItemInside(node, typ string) string {
	return `{"associatedChildMessage":{"message":{` + node + `,` + association(typ) + `}}}`
}

// Contacts as WhatsApp shares them (CID-30), vCards with synthetic names and numbers: the number is
// in a TEL line whose waid parameter is the WhatsApp id (digits only) and whose value is the number
// as the sharer's phone formats it.
const (
	// cardAna has one number, with the labelled item1. prefix some phones write.
	cardAna = "BEGIN:VCARD\nVERSION:3.0\nN:;Ana Test;;;\nFN:Ana Test\nitem1.TEL;waid=5511999990000:+55 11 99999-0000\nitem1.X-ABLabel:Mobile\nEND:VCARD"
	// cardBruno has two numbers, the second not on WhatsApp (an empty waid), and CRLF line ends.
	cardBruno = "BEGIN:VCARD\r\nVERSION:3.0\r\nN:Test;Bruno;;;\r\nFN:Bruno Test\r\nTEL;type=CELL;type=VOICE;waid=5521988880000:+55 21 98888-0000\r\nTEL;type=HOME;waid=:(21) 2555-0000\r\nEND:VCARD\r\n"
)
