package api_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

// ROUTE-10 (HTTP): a user or agent post naming 11 chats answers 422 recipients "at most 10
// chats per message" and stores nothing; 10 chats are accepted.
func TestPostRecipientsCapHTTP(t *testing.T) {
	f := newAgentRoom(t)
	var chats []string
	for i := range 11 {
		chats = append(chats, f.chatMember(t, fmt.Sprintf("Customer %d", i)))
	}
	posts := []func(dto.PostMessageRequest) *testkit.Response{
		func(r dto.PostMessageRequest) *testkit.Response {
			return f.alice.Post("/api/v1/rooms/"+f.room.ID+"/messages", r)
		},
		func(r dto.PostMessageRequest) *testkit.Response {
			return f.rest().Post(agentRoomPath(f.room.ID, "/messages"), r)
		},
	}
	for _, post := range posts {
		fields := post(dto.PostMessageRequest{Text: "everyone", Recipients: chats}).
			RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid).Fields
		require.Equal(t, map[string]string{"recipients": "at most 10 chats per message"}, fields)
	}
	page := testkit.Decode[dto.Page[dto.Message]](f.alice.Get("/api/v1/rooms/" + f.room.ID + "/messages").RequireStatus(http.StatusOK))
	require.Empty(t, page.Items, "nothing stored")

	ten := testkit.Decode[dto.Message](posts[1](dto.PostMessageRequest{Text: "ten", Recipients: chats[:10]}).RequireStatus(http.StatusCreated))
	require.Len(t, outboxChatIDs(ten), 10)
}
