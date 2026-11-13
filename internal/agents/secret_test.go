package agents

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
)

// SR-10: a session's client knows the secret frame from MinSecretClientVersion on; a local "dev" build is
// current, and a missing version or one that is no release version fails closed, as for heartbeats.
func TestSecretCurrentFailsClosed(t *testing.T) {
	for v, want := range map[string]bool{
		"dev": true, "v0.32.0": true, "v0.32.1": true, "v1.0.0": true, "0.32.0": true, "v0.32.0-rc1": true,
		"v0.31.9": false, "v0.31.2-4-gabc1234": false, "": false, "abc1234": false, "test": false,
	} {
		require.Equal(t, want, secretCurrent(v), v)
	}
}

// SR-06, SR-07: the reason a session gives for a failure reaches the person as one short line, and never
// when it carries the value.
func TestSecretReasonIsOneShortLineWithoutTheValue(t *testing.T) {
	require.Equal(t, "no space left on device", secretReason("  no space\nleft on device ", "v4lue"))
	require.Equal(t, "the session could not save it", secretReason("", "v4lue"))
	require.Equal(t, "the session could not save it", secretReason("cannot save v4lue here", "v4lue"))
	long := secretReason(strings.Repeat("x", 300), "v4lue")
	require.Equal(t, maxSecretReason+1, len([]rune(long)))
}

// SR-06: an acknowledgement goes to the request that waits for it, once; one for no request is dropped.
func TestSecretWaitsPairAnAckWithItsRequest(t *testing.T) {
	var w secretWaits
	wait := w.await("r1")
	w.answer(dto.AgentSecretAckFrame{RequestID: "other", OK: true})
	w.answer(dto.AgentSecretAckFrame{RequestID: "r1", OK: true, Path: "/p"})
	w.answer(dto.AgentSecretAckFrame{RequestID: "r1", OK: false})
	ack := <-wait
	require.True(t, ack.OK)
	require.Equal(t, "/p", ack.Path)
	select {
	case extra := <-wait:
		require.Failf(t, "a second acknowledgement was delivered", "%+v", extra)
	default:
	}
}
