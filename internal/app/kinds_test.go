package app

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/config"
	"github.com/wnexous/partyline/internal/connectors/whatsapp"
)

// LF-05: the WhatsApp kind takes its media limit from PARTYLINE_WHATSAPP_MAX_FILE_MB.
func TestBuiltinKindsTakeTheWhatsAppMediaLimit(t *testing.T) {
	for _, k := range BuiltinKindsFor(&config.Config{WhatsAppMaxFileBytes: 100 << 20}) {
		if k.Name == whatsapp.KindName {
			require.Equal(t, int64(100<<20), k.MaxFileBytes)
			return
		}
	}
	t.Fatal("no WhatsApp kind")
}
