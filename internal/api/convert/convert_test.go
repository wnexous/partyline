package convert_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/convert"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/rooms"
	"github.com/wnexous/partyline/internal/store/db"
)

const sha = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// GRP-03, PROF-03: a rendered message names its author by the author's intelligence: the
// user, the AI, the person of a chat or the linked user of a chat; a system message or a chat
// sender without an intelligence has no id and only the name that shows.
func TestMessageAuthor(t *testing.T) {
	view := func(over func(*db.MessageView)) rooms.Message {
		v := db.MessageView{ID: "m1", RoomID: "r1", Seq: 1, Kind: "text", Text: "hi", RecipientsJson: "[]"}
		over(&v)
		return rooms.Message{MessageView: v}
	}
	telegram := &db.ListChatRefsRow{ID: "c1", Title: "family", Kind: "group", Platform: "telegram"}

	cases := []struct {
		name string
		m    rooms.Message
		want dto.IntelligenceRef
	}{
		{"user with author_iid", view(func(v *db.MessageView) {
			v.AuthorKind, v.AuthorID, v.AuthorIid = "user", "u1", "u1"
			v.AuthorName, v.AuthorHandle, v.AuthorIkind, v.AuthorAvatar = "Ana", "ana", "human", sha
		}), dto.IntelligenceRef{
			ID: "u1", Kind: dto.IntelligenceKindHuman, Role: dto.IntelligenceRoleUser, DisplayName: "Ana", Handle: "ana",
			AvatarURL: "/api/v1/intelligences/u1/avatar?v=" + sha,
		}},
		{"user written before author_iid existed", view(func(v *db.MessageView) {
			v.AuthorKind, v.AuthorID = "user", "u1"
			v.AuthorName, v.AuthorHandle, v.AuthorIkind = "Ana", "ana", "human"
		}), dto.IntelligenceRef{ID: "u1", Kind: dto.IntelligenceKindHuman, Role: dto.IntelligenceRoleUser, DisplayName: "Ana", Handle: "ana"}},
		{"AI", view(func(v *db.MessageView) {
			v.AuthorKind, v.AuthorID, v.AuthorIid = "agent", "a1", "a1"
			v.AuthorName, v.AuthorHandle, v.AuthorIkind = "Vega", "atlas", "ai"
		}), dto.IntelligenceRef{ID: "a1", Kind: dto.IntelligenceKindAI, Role: dto.IntelligenceRoleAI, DisplayName: "Vega", Handle: "atlas"}},
		{"AI without an intelligence row", view(func(v *db.MessageView) {
			v.AuthorKind, v.AuthorID, v.AuthorName = "agent", "a1", "builder"
		}), dto.IntelligenceRef{ID: "a1", Kind: dto.IntelligenceKindAI, Role: dto.IntelligenceRoleAI, DisplayName: "builder"}},
		{"chat sender who is a person", func() rooms.Message {
			m := view(func(v *db.MessageView) {
				v.AuthorKind, v.AuthorID, v.AuthorIid, v.SenderID = "chat", "c1", "p1", "5511999990000"
				v.AuthorName, v.AuthorHandle, v.AuthorIkind = "Pat", "pat", "human"
			})
			m.Chat = telegram
			return m
		}(), dto.IntelligenceRef{ID: "p1", Kind: dto.IntelligenceKindHuman, Role: dto.IntelligenceRolePerson, DisplayName: "Pat", Handle: "pat", Platform: "telegram"}},
		{"chat sender linked to a user", func() rooms.Message {
			m := view(func(v *db.MessageView) {
				v.AuthorKind, v.AuthorID, v.AuthorIid, v.AuthorUserID = "chat", "c1", "u1", "u1"
				v.AuthorName, v.AuthorHandle, v.AuthorIkind = "Ana", "ana", "human"
			})
			m.Chat = telegram
			return m
		}(), dto.IntelligenceRef{ID: "u1", Kind: dto.IntelligenceKindHuman, Role: dto.IntelligenceRoleUser, DisplayName: "Ana", Handle: "ana"}},
		{"chat sender without an intelligence", func() rooms.Message {
			m := view(func(v *db.MessageView) { v.AuthorKind, v.AuthorID, v.AuthorName = "chat", "c1", "Pat P." })
			m.Chat = telegram
			return m
		}(), dto.IntelligenceRef{DisplayName: "Pat P."}},
		{"system", view(func(v *db.MessageView) { v.AuthorKind, v.AuthorName = "system", "Partyline" }), dto.IntelligenceRef{DisplayName: "Partyline"}},
	}
	for _, c := range cases {
		require.Equal(t, c.want, convert.Message(c.m).Author, c.name)
	}
}

// The avatar URL carries the content hash as its cache key and is empty without an avatar.
func TestAvatarURL(t *testing.T) {
	require.Equal(t, "", convert.AvatarURL("u1", ""))
	require.Equal(t, "/api/v1/intelligences/u1/avatar?v="+sha, convert.AvatarURL("u1", sha))
}

// OMEM-01, POS-05: a member of an organization is a user or an AI with its permission and position, and
// only a guest shows the identity that invited it - not a member or an admin, whatever the row holds, and
// not a guest whose inviter is gone.
func TestOrgMember(t *testing.T) {
	row := func(over func(*orgs.MemberRow)) orgs.MemberRow {
		r := orgs.MemberRow{
			IID: "u1", Permission: "member", Kind: "human", DisplayName: "Ana", Handle: "ana", AvatarSha256: sha,
			PositionID: "p1", PositionName: "Lead", JoinedAt: 1700000000000,
			InviterIID: "u2", InviterKind: "human", InviterDisplayName: "Bia", InviterHandle: "bia",
		}
		over(&r)
		return r
	}
	inviter := &dto.IntelligenceRef{ID: "u2", Kind: dto.IntelligenceKindHuman, Role: dto.IntelligenceRoleUser, DisplayName: "Bia", Handle: "bia"}

	got := convert.OrgMember(row(func(r *orgs.MemberRow) { r.Permission = "guest" }))
	require.Equal(t, dto.OrgMember{
		Intelligence: dto.IntelligenceRef{
			ID: "u1", Kind: dto.IntelligenceKindHuman, Role: dto.IntelligenceRoleUser, DisplayName: "Ana", Handle: "ana",
			AvatarURL: "/api/v1/intelligences/u1/avatar?v=" + sha,
		},
		Permission: dto.PermissionGuest, PositionID: "p1", PositionName: "Lead", InvitedBy: inviter,
		JoinedAt: convert.Time(1700000000000),
	}, got, "a guest shows its inviter")

	for _, permission := range []string{"member", "admin"} {
		require.Nil(t, convert.OrgMember(row(func(r *orgs.MemberRow) { r.Permission = permission })).InvitedBy,
			"%s: only a guest has an inviter", permission)
	}
	require.Nil(t, convert.OrgMember(row(func(r *orgs.MemberRow) {
		r.Permission, r.InviterIID, r.InviterKind, r.InviterDisplayName, r.InviterHandle = "guest", "", "", "", ""
	})).InvitedBy, "a guest whose inviter is gone has none")

	ai := convert.OrgMember(row(func(r *orgs.MemberRow) { r.Kind, r.AvatarSha256 = "ai", "" }))
	require.Equal(t, dto.IntelligenceRef{ID: "u1", Kind: dto.IntelligenceKindAI, Role: dto.IntelligenceRoleAI, DisplayName: "Ana", Handle: "ana"}, ai.Intelligence)
	withoutPosition := convert.OrgMember(row(func(r *orgs.MemberRow) { r.PositionID, r.PositionName = "", "" }))
	require.Empty(t, withoutPosition.PositionID)
	require.Empty(t, withoutPosition.PositionName)
}
