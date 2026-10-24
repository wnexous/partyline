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
