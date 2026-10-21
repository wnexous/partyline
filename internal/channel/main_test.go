package channel

import (
	"os"
	"testing"

	"github.com/wnexous/partyline/internal/channel/mcp"
)

func TestMain(m *testing.M) {
	// Channel notifications go out right after notifications/initialized in these tests; the
	// readiness waits for real clients are tested in package mcp.
	mcp.ReadyDelay, mcp.ReadyFallback = 0, 0
	os.Exit(m.Run())
}
