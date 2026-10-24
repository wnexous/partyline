package delivery

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/store/db"
)

// fakeTrust trusts the (author, agent) pairs it was given and records what it was asked.
type fakeTrust struct {
	trusted map[[2]string]bool
	err     error
	asked   [][2]string
}

func (f *fakeTrust) Trusts(_ context.Context, authorIID, agentID string) (bool, error) {
	f.asked = append(f.asked, [2]string{authorIID, agentID})
	return f.trusted[[2]string{authorIID, agentID}], f.err
}

// POS-10: the author of a message is the intelligence that wrote it, never the user an AI's post
// resolves to (author_user_id), which is how an AI used to be trusted as its owner.
func TestAuthorOfIsTheIntelligenceThatWroteTheMessage(t *testing.T) {
	for _, tc := range []struct {
		name string
		view db.MessageView
		want string
	}{
		{"a user", db.MessageView{AuthorKind: "user", AuthorID: "u1", AuthorUserID: "u1", AuthorIid: "u1"}, "u1"},
		{"an AI, not its owner", db.MessageView{AuthorKind: "agent", AuthorID: "a1", AuthorUserID: "u1", AuthorIid: "a1"}, "a1"},
		{"a user written before authors were recorded", db.MessageView{AuthorKind: "user", AuthorID: "u1", AuthorUserID: "u1"}, "u1"},
		{"an AI written before authors were recorded, not its owner", db.MessageView{AuthorKind: "agent", AuthorID: "a1", AuthorUserID: "u1"}, "a1"},
		{"a chat sender linked to a user", db.MessageView{AuthorKind: "chat", AuthorID: "c1", AuthorUserID: "u1", AuthorIid: "u1"}, "u1"},
		{"a chat sender with no intelligence", db.MessageView{AuthorKind: "chat", AuthorID: "c1"}, ""},
		{"a system message", db.MessageView{AuthorKind: "system"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, AuthorOf(tc.view))
		})
	}
}

// With no source of trust nobody is trusted: a component that was not given one fails closed.
func TestNoTrustSourceTrustsNobody(t *testing.T) {
	var source TrustSource
	ok, err := source.Open().Trusts(context.Background(), "alice", "atlas")
	require.NoError(t, err)
	require.False(t, ok)
}

// Each Open is a Trust of its own operation.
func TestOpenCallsTheSourceOncePerOperation(t *testing.T) {
	opened := 0
	source := TrustSource(func() Trust { opened++; return &fakeTrust{} })
	source.Open()
	source.Open()
	require.Equal(t, 2, opened)
}

// MEM-04, POS-10: the trusted delivery mode wants exactly the messages the AI trusts, and WantedBy
// reports the trust for the frame that carries it.
func TestWantedByAsksTheTrustAboutTheAuthorAndTheRecipient(t *testing.T) {
	prefs := MemberPrefs{Delivery: dto.DeliveryModeTrusted}
	trust := &fakeTrust{trusted: map[[2]string]bool{{"lead", "atlas"}: true}}
	ctx := context.Background()

	wanted, trusted, err := Facts{Text: "hi", AuthorIID: "lead"}.WantedBy(ctx, trust, "atlas", prefs)
	require.NoError(t, err)
	require.True(t, wanted)
	require.True(t, trusted)

	wanted, trusted, err = Facts{Text: "hi", AuthorIID: "peer"}.WantedBy(ctx, trust, "atlas", prefs)
	require.NoError(t, err)
	require.False(t, wanted, "trusted mode holds back what the AI does not trust")
	require.False(t, trusted)

	wanted, trusted, err = Facts{Text: "hi", AuthorIID: "lead"}.WantedBy(ctx, trust, "builder", prefs)
	require.NoError(t, err)
	require.False(t, wanted, "trust is relative to the recipient: lead does not manage builder")
	require.False(t, trusted)

	require.Equal(t, [][2]string{{"lead", "atlas"}, {"peer", "atlas"}, {"lead", "builder"}}, trust.asked)
}

// Another delivery mode does not need the trust to decide, but still reports it.
func TestWantedByReportsTheTrustInEveryMode(t *testing.T) {
	trust := &fakeTrust{trusted: map[[2]string]bool{{"lead", "atlas"}: true}}
	for _, mode := range []dto.DeliveryMode{dto.DeliveryModeAll, dto.DeliveryModeMentions} {
		wanted, trusted, err := Facts{Text: "@atlas hi", AuthorIID: "lead"}.WantedBy(context.Background(), trust, "atlas",
			MemberPrefs{Delivery: mode, Handle: "atlas"})
		require.NoError(t, err, mode)
		require.True(t, wanted, mode)
		require.True(t, trusted, mode)
	}
}

// A failing trust check is an error, not a silent "untrusted": the caller does not deliver on a guess.
func TestWantedByFailsWhenTheTrustCheckFails(t *testing.T) {
	boom := errors.New("database is closed")
	_, _, err := Facts{Text: "hi", AuthorIID: "lead"}.WantedBy(context.Background(), &fakeTrust{err: boom}, "atlas", MemberPrefs{})
	require.ErrorIs(t, err, boom)
}

// An author with no intelligence (a system notice, an unknown chat sender) is never trusted, and the
// trust is not even asked.
func TestWantedByNeverTrustsAnUnknownAuthor(t *testing.T) {
	trust := &fakeTrust{trusted: map[[2]string]bool{{"", "atlas"}: true}}
	_, trusted, err := Facts{Text: "notice"}.WantedBy(context.Background(), trust, "atlas", MemberPrefs{})
	require.NoError(t, err)
	require.False(t, trusted)
	require.Empty(t, trust.asked)
}
