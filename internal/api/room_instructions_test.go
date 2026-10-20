package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

// INS-01: instructions up to 4,000 characters (code points) are stored on create and update
// and returned by the room endpoints; 4,001 get 422 and change nothing; only managers
// change them (viewer 403, no access 404).
func TestRoomInstructions(t *testing.T) {
	env := testkit.New(t)
	alice, bob, carol := env.NewUser("alice"), env.NewUser("bob"), env.NewUser("carol")
	full := strings.Repeat("ã", dto.MaxRoomInstructions) // 8,000 bytes, 4,000 code points
	require.Equal(t, 4000, dto.MaxRoomInstructions)

	r := testkit.Decode[dto.Room](alice.Post("/api/v1/rooms", dto.CreateRoomRequest{Name: "ops", Instructions: full}).RequireStatus(http.StatusCreated))
	require.Equal(t, full, r.Instructions)
	path := "/api/v1/rooms/" + r.ID
	require.Equal(t, full, testkit.Decode[dto.Room](alice.Get(path).RequireStatus(http.StatusOK)).Instructions)

	e := alice.Post("/api/v1/rooms", dto.CreateRoomRequest{Name: "too long", Instructions: full + "a"}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Contains(t, e.Fields, "instructions")
	require.Len(t, testkit.Decode[dto.List[dto.Room]](alice.Get("/api/v1/rooms").RequireStatus(http.StatusOK)).Items, 1, "the invalid room was not created")

	text := "Client: Lollerie. Answer in Portuguese, friendly tone."
	got := testkit.Decode[dto.Room](alice.Patch(path, dto.UpdateRoomRequest{Instructions: &text}).RequireStatus(http.StatusOK))
	require.Equal(t, text, got.Instructions)
	require.Equal(t, "ops", got.Name, "other fields unchanged")
	listed := testkit.Decode[dto.List[dto.Room]](alice.Get("/api/v1/rooms").RequireStatus(http.StatusOK)).Items
	require.Equal(t, text, listed[0].Instructions)

	e = alice.Patch(path, dto.UpdateRoomRequest{Instructions: ptr(full + "a")}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Contains(t, e.Fields, "instructions")
	require.Equal(t, text, testkit.Decode[dto.Room](alice.Get(path).RequireStatus(http.StatusOK)).Instructions)

	// A later update of another field keeps the instructions.
	require.Equal(t, text, testkit.Decode[dto.Room](alice.Patch(path, dto.UpdateRoomRequest{Name: ptr("ops 2")}).RequireStatus(http.StatusOK)).Instructions)

	shareRoom(alice, r.ID, dto.SharingUser{UserID: bob.User.ID, Level: dto.LevelPost})
	require.Equal(t, text, testkit.Decode[dto.Room](bob.Get(path).RequireStatus(http.StatusOK)).Instructions, "viewers read them")
	bob.Patch(path, dto.UpdateRoomRequest{Instructions: ptr("hijack")}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	carol.Patch(path, dto.UpdateRoomRequest{Instructions: ptr("hijack")}).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	require.Equal(t, text, testkit.Decode[dto.Room](alice.Get(path).RequireStatus(http.StatusOK)).Instructions)
}
