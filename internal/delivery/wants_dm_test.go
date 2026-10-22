package delivery

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
)

// DM-03, MEM-03, MEM-04: a message in a DM is addressed to the AI, so it passes every delivery
// mode except trusted, which still wants the owner's messages; in a group nothing changes.
func TestWantsInADM(t *testing.T) {
	plain := Delivered{Text: "no mention, no keyword, not a reply"}
	owner := Delivered{Text: "from the owner", Trusted: true}
	prefs := func(mode dto.DeliveryMode) MemberPrefs {
		return MemberPrefs{Delivery: mode, Names: []string{"Atlas"}, Handle: "atlas", Keywords: []string{"deploy"}}
	}
	inDM := func(d Delivered) Delivered { d.DM = true; return d }

	for _, tc := range []struct {
		name string
		mode dto.DeliveryMode
		msg  Delivered
		want bool
	}{
		{"dm all", dto.DeliveryModeAll, inDM(plain), true},
		{"dm default mode", "", inDM(plain), true},
		{"dm mentions without a mention", dto.DeliveryModeMentions, inDM(plain), true},
		{"dm trusted from a third party", dto.DeliveryModeTrusted, inDM(plain), false},
		{"dm trusted from the owner", dto.DeliveryModeTrusted, inDM(owner), true},
		{"group mentions without a mention", dto.DeliveryModeMentions, plain, false},
		{"group mentions with the handle", dto.DeliveryModeMentions, Delivered{Text: "hey @atlas"}, true},
		{"group trusted from a third party", dto.DeliveryModeTrusted, plain, false},
		{"group all", dto.DeliveryModeAll, plain, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, Wants(prefs(tc.mode), tc.msg))
		})
	}
}

// The facts of a stored message carry the room kind into the wants of the agent and of a project.
func TestFactsCarryTheRoomKind(t *testing.T) {
	prefs := MemberPrefs{Delivery: dto.DeliveryModeMentions}
	dm := Facts{Text: "hello", DM: true}
	group := Facts{Text: "hello"}
	require.True(t, dm.WantedBy("agent", "owner", prefs))
	require.False(t, group.WantedBy("agent", "owner", prefs))
	require.True(t, dm.WantedByProject("agent", "project", "owner", prefs))
	require.False(t, group.WantedByProject("agent", "project", "owner", prefs))
}

// RoomRef names a DM by the other participant and marks it; a group keeps its name and no kind.
func TestRoomRef(t *testing.T) {
	require.Equal(t, dto.RoomRef{ID: "r1", Name: "Ops"}, RoomRef("r1", "Ops", "group"))
	require.Equal(t, dto.RoomRef{ID: "r2", Name: "Alice", Kind: dto.ConversationKindDM}, RoomRef("r2", "Alice", "dm"))
}
