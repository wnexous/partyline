package convert

import (
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/orgs"
)

// OrgAvatarURL is the URL of an organization's avatar, empty without one. The content hash is a
// cache key: a new avatar has a new URL.
func OrgAvatarURL(id, sha string) string {
	if sha == "" {
		return ""
	}
	return "/api/v1/orgs/" + id + "/avatar?v=" + sha
}

// Position renders one node of an organization's chart (POS-01).
func Position(p orgs.Position) dto.Position {
	return dto.Position{ID: p.ID, Name: p.Name, ParentID: p.ParentID}
}

// Organization renders an organization as one of its members sees it, with the viewer's own
// permission and position.
func Organization(m orgs.Membership) dto.Organization {
	return dto.Organization{
		ID: m.ID, Name: m.Name, Slug: m.Slug,
		AvatarURL:  OrgAvatarURL(m.ID, m.AvatarSha256),
		Permission: dto.Permission(m.Permission), PositionID: m.PositionID,
		CreatedAt: Time(m.CreatedAt), UpdatedAt: Time(m.UpdatedAt),
	}
}

// OrgMember renders one membership as the members list shows it: the identity, its permission and
// position and, for a guest, the identity that invited it (OMEM-01, OMEM-03).
func OrgMember(r orgs.MemberRow) dto.OrgMember {
	m := dto.OrgMember{
		Intelligence: memberRef(r.IID, r.Kind, r.DisplayName, r.Handle, r.AvatarSha256),
		Permission:   dto.Permission(r.Permission),
		PositionID:   r.PositionID, PositionName: r.PositionName,
		JoinedAt: Time(r.JoinedAt),
	}
	if r.Permission == string(orgs.PermissionGuest) && r.InviterIID != "" {
		inviter := memberRef(r.InviterIID, r.InviterKind, r.InviterDisplayName, r.InviterHandle, r.InviterAvatarSha256)
		m.InvitedBy = &inviter
	}
	m.CanDelete, m.DeleteEffect, m.CanMove = r.CanDelete, deleteEffect(r), r.CanMove
	return m
}

// deleteEffect is what deleting the member does in the organization (POS-06).
func deleteEffect(r orgs.MemberRow) dto.DeleteEffect {
	if r.DeletedForGood {
		return dto.DeleteEffectDelete
	}
	return dto.DeleteEffectRemove
}

// OrgChart renders an organization's org chart; online tells whether an AI has a live session (CHART-01).
func OrgChart(v orgs.ChartView, online func(agentID string) bool) dto.OrgChart {
	member := func(r orgs.MemberRow) dto.ChartMember {
		ref := memberRef(r.IID, r.Kind, r.DisplayName, r.Handle, r.AvatarSha256)
		return dto.ChartMember{
			Intelligence: ref, Permission: dto.Permission(r.Permission),
			Online: ref.Kind == dto.IntelligenceKindAI && online(r.IID), Under: r.Under,
			CanDelete: r.CanDelete, DeleteEffect: deleteEffect(r), CanMove: r.CanMove,
		}
	}
	members := func(rows []orgs.MemberRow) []dto.ChartMember {
		out := make([]dto.ChartMember, len(rows))
		for i, r := range rows {
			out[i] = member(r)
		}
		return out
	}
	out := dto.OrgChart{
		Viewer: dto.ChartViewer{Admin: v.ViewerAdmin}, Positions: make([]dto.ChartPosition, len(v.Positions)),
		Members: members(v.Members), Unassigned: members(v.Unassigned),
	}
	for i, p := range v.Positions {
		out.Positions[i] = dto.ChartPosition{ID: p.ID, Name: p.Name, ParentID: p.ParentID, Holders: members(p.Holders)}
	}
	return out
}

// memberRef is the summary of an organization member, who is a user or an AI.
func memberRef(id, kind, displayName, handle, avatarSha string) dto.IntelligenceRef {
	role := dto.IntelligenceRoleUser
	if kind == string(dto.IntelligenceKindAI) {
		role = dto.IntelligenceRoleAI
	}
	return dto.IntelligenceRef{
		ID: id, Kind: dto.IntelligenceKind(kind), Role: role, DisplayName: displayName, Handle: handle,
		AvatarURL: AvatarURL(id, avatarSha),
	}
}

// Invitation renders a pending invitation with the identities it names (OMEM-01).
func Invitation(i orgs.Invitation) dto.Invitation {
	return dto.Invitation{
		ID: i.ID, OrgID: i.OrgID, OrgName: i.OrgName, OrgSlug: i.OrgSlug,
		Invitee:    invitationRef(i.InviteeIID, i.InviteeKind, i.InviteeName, i.InviteeHandle, i.InviteeAvatarSha256),
		Permission: dto.Permission(i.Permission), PositionID: i.PositionID, PositionName: i.PositionName,
		InvitedBy: invitationRef(i.InvitedByIID, i.InvitedByKind, i.InvitedByName, i.InvitedByHandle, i.InvitedByAvatarSha256),
		CreatedAt: i.CreatedAt, ExpiresAt: i.ExpiresAt,
	}
}

// invitationRef is the summary of an invitee or inviter, whose role is "user" or "ai".
func invitationRef(id, role, displayName, handle, avatarSha string) dto.IntelligenceRef {
	kind := string(dto.IntelligenceKindHuman)
	if role == string(dto.IntelligenceRoleAI) {
		kind = string(dto.IntelligenceKindAI)
	}
	return memberRef(id, kind, displayName, handle, avatarSha)
}
