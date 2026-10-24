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
