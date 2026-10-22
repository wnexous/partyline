package rooms

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
)

// authorIID reads the author intelligence stored with a message.
func (f *fixture) authorIID(t *testing.T, messageID string) string {
	t.Helper()
	var iid string
	require.NoError(t, f.raw.QueryRow("SELECT author_iid FROM messages WHERE id = ?", messageID).Scan(&iid))
	return iid
}

// PROF-01, GRP-03: a post by a user or an agent stores its author's intelligence (the user's
// and the agent's ids are their intelligences' ids); chat messages (until their person is
// known) and system messages have none.
func TestPostsRecordTheirAuthorIntelligence(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	a1 := f.agent(t, alice, "a1")
	_, chat := f.chat(t, alice, "c1")
	r := f.room(t, alice, "ops")
	f.addMember(t, alice, r.ID, dto.MemberKindAgent, a1, true)
	f.addMember(t, alice, r.ID, dto.MemberKindChat, chat, true)

	require.Equal(t, alice.UserID, f.authorIID(t, f.post(t, alice, r.ID, "from the user").ID))
	agent := agentPrincipal(alice, a1)
	require.Equal(t, a1, f.authorIID(t, f.post(t, agent, r.ID, "from the agent").ID))
	fromChat, err := f.svc.PostFromChat(ctx, r.ID, ChatMessage{ChatID: chat, SenderID: "5511999990000", Text: "from the chat"})
	require.NoError(t, err)
	require.Empty(t, f.authorIID(t, fromChat.ID))

	for i := 0; i < 25; i++ { // the 25th consecutive agent message pauses the room with a system notice
		f.post(t, agent, r.ID, "ping")
	}
	last, _, err := f.svc.Messages(ctx, alice, r.ID, Page{Limit: 1})
	require.NoError(t, err)
	require.Equal(t, string(dto.AuthorKindSystem), last[0].AuthorKind)
	require.Empty(t, f.authorIID(t, last[0].ID))
}
