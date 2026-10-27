package convert_test

import (
	"encoding/json"
	"testing"
	"time"

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

// GAV-01, GAV-04: the photo of a group is a URL under its room that carries the content hash as its cache
// key, absent without a photo, and the room and its DTO say the same.
func TestRoomAvatarURL(t *testing.T) {
	require.Equal(t, "", convert.RoomAvatarURL("r1", ""))
	require.Equal(t, "/api/v1/rooms/r1/avatar?v="+sha, convert.RoomAvatarURL("r1", sha))

	room := func(sha string) rooms.Room {
		return rooms.Room{Room: db.Room{ID: "r1", Kind: "group", Name: "ops", AvatarSha256: sha}}
	}
	require.Equal(t, "/api/v1/rooms/r1/avatar?v="+sha, convert.Room(room(sha)).AvatarURL)
	encoded, err := json.Marshal(convert.Room(room("")))
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "avatar_url", "the key is absent without a photo")
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
		JoinedAt: convert.Time(1700000000000), DeleteEffect: dto.DeleteEffectRemove,
	}, got, "a guest shows its inviter")

	// What the viewer may do to the member travels with it; an AI of the organization is deleted for good.
	rights := convert.OrgMember(row(func(r *orgs.MemberRow) { r.CanDelete, r.CanMove, r.DeletedForGood = true, true, true }))
	require.True(t, rights.CanDelete)
	require.True(t, rights.CanMove)
	require.Equal(t, dto.DeleteEffectDelete, rights.DeleteEffect)
	require.False(t, got.CanDelete || got.CanMove)

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

// OMEM-01: an invitation names its invitee and inviter as users or AIs, with their avatars, and carries the
// position only when it has one.
func TestInvitation(t *testing.T) {
	created := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	inv := orgs.Invitation{
		ID: "i1", OrgID: "o1", OrgName: "Acme", OrgSlug: "acme",
		InviteeIID: "a1", InviteeHandle: "atlas", InviteeName: "Atlas", InviteeKind: "ai", InviteeAvatarSha256: sha,
		Permission: orgs.PermissionGuest, PositionID: "p1", PositionName: "Lead",
		InvitedByIID: "u1", InvitedByHandle: "ana", InvitedByName: "Ana", InvitedByKind: "user",
		CreatedAt: created, ExpiresAt: created.Add(14 * 24 * time.Hour),
	}
	require.Equal(t, dto.Invitation{
		ID: "i1", OrgID: "o1", OrgName: "Acme", OrgSlug: "acme",
		Invitee: dto.IntelligenceRef{
			ID: "a1", Kind: dto.IntelligenceKindAI, Role: dto.IntelligenceRoleAI, DisplayName: "Atlas", Handle: "atlas",
			AvatarURL: "/api/v1/intelligences/a1/avatar?v=" + sha,
		},
		Permission: dto.PermissionGuest, PositionID: "p1", PositionName: "Lead",
		InvitedBy: dto.IntelligenceRef{ID: "u1", Kind: dto.IntelligenceKindHuman, Role: dto.IntelligenceRoleUser, DisplayName: "Ana", Handle: "ana"},
		CreatedAt: created, ExpiresAt: created.Add(14 * 24 * time.Hour),
	}, convert.Invitation(inv))

	// An AI can be the inviter, and a user the invitee; an invitation may carry no position.
	inv.InviteeKind, inv.InvitedByKind, inv.PositionID, inv.PositionName = "user", "ai", "", ""
	got := convert.Invitation(inv)
	require.Equal(t, dto.IntelligenceRoleUser, got.Invitee.Role)
	require.Equal(t, dto.IntelligenceKindHuman, got.Invitee.Kind)
	require.Equal(t, dto.IntelligenceRoleAI, got.InvitedBy.Role)
	require.Equal(t, dto.IntelligenceKindAI, got.InvitedBy.Kind)
	require.Empty(t, got.PositionID)
	require.Empty(t, got.PositionName)
}

// CHART-01, CHART-03: the chart renders every list as a list - an empty one is [], never null - and asks
// presence about AIs only, since people have none.
func TestOrgChart(t *testing.T) {
	asked := map[string]bool{}
	online := func(id string) bool { asked[id] = true; return true }
	chart := convert.OrgChart(orgs.ChartView{
		ViewerAdmin: true,
		Positions: []orgs.ChartPosition{{
			Position: orgs.Position{ID: "p1", Name: "Admin"},
			Holders: []orgs.MemberRow{
				{IID: "u1", Kind: "human", DisplayName: "Ana", Handle: "ana", Permission: "admin", CanDelete: false, CanMove: true},
				{IID: "a1", Kind: "ai", DisplayName: "Atlas", Handle: "atlas", Permission: "guest", CanDelete: true, DeletedForGood: true},
			},
		}, {Position: orgs.Position{ID: "p2", Name: "Lead", ParentID: "p1"}, Holders: []orgs.MemberRow{}}},
		Members: []orgs.MemberRow{{IID: "a2", Kind: "ai", DisplayName: "Vega", Handle: "vega", Permission: "member", Under: "u1"}},
	}, online)

	require.True(t, chart.Viewer.Admin)
	require.Equal(t, map[string]bool{"a1": true, "a2": true}, asked, "presence is asked for AIs, not for Ana")
	ana, atlas := chart.Positions[0].Holders[0], chart.Positions[0].Holders[1]
	require.False(t, ana.Online)
	require.True(t, atlas.Online)
	require.Equal(t, dto.DeleteEffectRemove, ana.DeleteEffect)
	require.Equal(t, dto.DeleteEffectDelete, atlas.DeleteEffect)
	require.True(t, ana.CanMove)
	require.True(t, atlas.CanDelete)
	require.Equal(t, dto.PermissionGuest, atlas.Permission)
	require.Equal(t, "p1", chart.Positions[1].ParentID)
	require.Equal(t, "u1", chart.Members[0].Under)

	raw, err := json.Marshal(chart)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"holders":[]`)
	require.Contains(t, string(raw), `"unassigned":[]`)
	require.NotContains(t, string(raw), "null")
}

// SESS-30: a rendered message carries the label of the session that wrote it, and an empty label
// stays empty for the principal and for every author that is not an AI.
func TestMessageAuthorLabel(t *testing.T) {
	view := func(kind, label string) rooms.Message {
		return rooms.Message{MessageView: db.MessageView{
			ID: "m1", RoomID: "r1", Seq: 1, Kind: "text", AuthorKind: kind, AuthorID: "a1",
			AuthorLabel: label, RecipientsJson: "[]",
		}}
	}

	labelled := convert.Message(view("agent", "claude-2"))
	require.Equal(t, "claude-2", labelled.AuthorLabel)
	data, err := json.Marshal(labelled)
	require.NoError(t, err)
	require.Contains(t, string(data), `"author_label":"claude-2"`)

	require.Empty(t, convert.Message(view("agent", "")).AuthorLabel, "the principal has no label")
	require.Empty(t, convert.Message(view("user", "")).AuthorLabel)
}
