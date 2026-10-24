package channel

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
)

// ORG-05, POS-07, ORG-04 (design "Error Handling Strategy"): a start the server refuses because of where it
// would put the AI - a creator who is not in the organization (403), a position it may not give (403), an
// organization other than the AI's home (409), an organization or position that does not exist (422) - is told
// to the person in the server's own words, once, and not retried: asking again would be refused again. The
// message is not the one for a rejected token or for the agent limit, which 403 and 409 mean otherwise.
func TestLinkReportsARefusedPlacementWithoutRetrying(t *testing.T) {
	cases := []struct {
		name    string
		id      Identity
		status  int
		code    dto.ErrorCode
		message string
	}{
		{"a creator who is not in the organization", Identity{Org: "acme"}, 403, dto.ErrorCodeForbidden, "you are not a member of that organization"},
		{"a position the creator may not give", Identity{Position: "Director"}, 403, dto.ErrorCodeForbidden, "that position is not below your own"},
		{"an organization and a position", Identity{Org: "acme", Position: "Director", Parent: "@lead"}, 403, dto.ErrorCodeForbidden, "that position is not below the parent AI's own"},
		{"an organization other than the AI's home", Identity{Org: "globex"}, 409, dto.ErrorCodeOrganizationMismatch, "this AI belongs to Acme (acme): an AI does not move to another organization"},
		{"an organization that does not exist", Identity{Org: "nope"}, 422, dto.ErrorCodeOrganizationRequired, "no organization has that slug or id: partyline orgs lists yours"},
		{"a position that does not exist", Identity{Position: "Nope"}, 422, dto.ErrorCodePositionUnknown, "the organization has no position with that name: partyline orgs <slug> lists them"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var log dialLog
			base := refuseWith(t, &log, c.status, c.code, c.message)
			l, stderr, _ := runLinkAs(t, base, c.id, fastTiming, false)

			require.Eventually(t, func() bool { return l.Err() != nil }, wait, 5*time.Millisecond)
			require.EqualError(t, l.Err(), "partyline: the server refused this session: "+c.message)
			time.Sleep(4 * fastTiming.maxBackoff)
			require.Equal(t, 1, log.count(), "no retry")
			require.Contains(t, stderr.String(), "partyline: the server refused this session: "+c.message)
			require.NotContains(t, stderr.String(), "PARTYLINE_TOKEN")
			require.NotContains(t, stderr.String(), "agent limit")
		})
	}
}

// 403 and 409 keep their other meanings when the session asked for no placement the server could refuse: a 403
// is a token the server will not take (a user's token, a revoked one), retried; a 409 other than the AI's
// organization is the agent limit, retried; a session that asked only for a parent AI cannot be refused for
// it, so a 403 there is a token too (L-018: each condition is the only thing that differs).
func TestLinkKeepsTheOtherMeaningsOfForbiddenAndConflict(t *testing.T) {
	cases := []struct {
		name    string
		id      Identity
		status  int
		code    dto.ErrorCode
		message string
		stderr  string
		err     string
	}{
		{"a token the server will not take", Identity{}, 403, dto.ErrorCodeForbidden, "agent token required",
			"the server rejected PARTYLINE_TOKEN", "partyline: not connected (invalid token)"},
		{"a token, with only a parent asked for", Identity{Parent: "@lead"}, 403, dto.ErrorCodeForbidden, "agent token required",
			"the server rejected PARTYLINE_TOKEN", "partyline: not connected (invalid token)"},
		{"the agent limit", Identity{Org: "acme"}, 409, dto.ErrorCodeConflict, "agent limit reached",
			"agent limit reached", errAgentLimit.Error()},
		{"the agent limit, nothing asked for", Identity{}, 409, dto.ErrorCodeConflict, "agent limit reached",
			"agent limit reached", errAgentLimit.Error()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var log dialLog
			base := refuseWith(t, &log, c.status, c.code, c.message)
			l, stderr, _ := runLinkAs(t, base, c.id, fastTiming, false)

			require.Eventually(t, func() bool { return log.count() >= 3 }, wait, 5*time.Millisecond, "retried")
			require.Eventually(t, func() bool { return l.Err() != nil }, wait, 5*time.Millisecond)
			require.EqualError(t, l.Err(), c.err)
			require.Contains(t, stderr.String(), c.stderr)
			require.NotContains(t, stderr.String(), "the server refused this session")
		})
	}
}
