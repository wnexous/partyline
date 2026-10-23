package connectors_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/connectors"
)

// The platform label of the supported platforms, which names a chat's room and a nameless person.
func TestPlatformLabel(t *testing.T) {
	for kind, want := range map[string]string{
		"whatsapp": "WhatsApp", "telegram": "Telegram", "discord": "Discord", "webhook": "Webhook", "matrix": "Matrix",
	} {
		require.Equal(t, want, connectors.PlatformLabel(kind), kind)
	}
}
