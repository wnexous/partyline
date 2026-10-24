package agents

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/store/db"
)

func headers(kv ...string) http.Header {
	h := http.Header{}
	h.Set(dto.HeaderFolder, url.PathEscape("/work/acme"))
	for i := 0; i < len(kv); i += 2 {
		h.Set(kv[i], kv[i+1])
	}
	return h
}

// AD-020: the three placement headers are optional; --org and --parent are plain text, --position is
// percent-encoded like the folder so a name with spaces or accents survives a header.
func TestFolderIDFromHeaderReadsThePlacementHeaders(t *testing.T) {
	id, err := FolderIDFromHeader(headers(
		dto.HeaderOrg, "  acme ",
		dto.HeaderPosition, url.PathEscape("Líder de equipe"),
		dto.HeaderParent, " @lead ",
	))
	require.NoError(t, err)
	require.Equal(t, "acme", id.Org)
	require.Equal(t, "Líder de equipe", id.Position)
	require.Equal(t, "@lead", id.Parent)
	require.Equal(t, "/work/acme", id.Folder)
}

// A v0.5 CLI sends none of them, and a blank one is none: the defaults apply (AD-020).
func TestFolderIDFromHeaderWithoutPlacementHeadersReadsEmpty(t *testing.T) {
	for name, h := range map[string]http.Header{
		"absent": headers(),
		"blank":  headers(dto.HeaderOrg, " ", dto.HeaderPosition, "%20", dto.HeaderParent, "\t"),
	} {
		t.Run(name, func(t *testing.T) {
			id, err := FolderIDFromHeader(h)
			require.NoError(t, err)
			require.Empty(t, id.Org)
			require.Empty(t, id.Position)
			require.Empty(t, id.Parent)
		})
	}
}

// A position that is not valid percent-encoding is a position no organization has: 422 position_unknown on
// the field, never a silent default (a plain "100%" must have been sent as "100%25").
func TestFolderIDFromHeaderRefusesAPositionThatIsNotPercentEncoded(t *testing.T) {
	_, err := FolderIDFromHeader(headers(dto.HeaderPosition, "100%"))
	var he *httpx.Error
	require.True(t, errors.As(err, &he), "got %v", err)
	require.Equal(t, http.StatusUnprocessableEntity, he.Status)
	require.Equal(t, dto.ErrorCodePositionUnknown, he.Code)
	require.Contains(t, he.Fields, "position")
}

// teamOf creates an organization the user administers, next to their personal one.
func (f *fixture) teamOf(t *testing.T, owner authn.Principal, name string) db.Organization {
	t.Helper()
	m, err := orgs.NewService(orgs.Deps{Store: f.st, Now: f.clock.Now}).Create(context.Background(), owner.UserID, name)
	require.NoError(t, err)
	return m.Organization
}

// placeUnlinked puts an unlinked identity in the organization the way the upgrade leaves it (OUPG-01): the
// owner's organization, created by the owner, a member with no position.
func (f *fixture) placeUnlinked(t *testing.T, owner authn.Principal, agentID string) string {
	t.Helper()
	ctx := context.Background()
	home, err := orgs.DefaultFor(ctx, f.st.R(), owner.UserID)
	require.NoError(t, err)
	f.exec(t, "UPDATE agents SET org_id = ?, creator_iid = ? WHERE id = ?", home, owner.UserID, agentID)
	require.NoError(t, f.st.WithTx(ctx, func(q *db.Queries) error {
		return orgs.AddMember(ctx, q, orgs.AddMemberInput{OrgID: home, IID: agentID, Permission: orgs.PermissionMember, Now: t0})
	}))
	return home
}

func requireMismatch(t *testing.T, err error, home db.Organization) {
	t.Helper()
	var he *httpx.Error
	require.True(t, errors.As(err, &he), "got %v", err)
	require.Equal(t, http.StatusConflict, he.Status)
	require.Equal(t, dto.ErrorCodeOrganizationMismatch, he.Code)
	require.Equal(t, home.Slug, he.Details[dto.MismatchDetailOrganization])
	require.Equal(t, home.Name, he.Details[dto.MismatchDetailName])
}

// ORG-04, MIG-02: a start the rules refuse links nothing. An unlinked identity (v0.3) adopted by a session
// that names another organization with --org is 409 organization_mismatch - it already belongs to its own -
// and stays unlinked; the same start without --org, or with its home, adopts it.
func TestARefusedStartDoesNotAdoptTheUnlinkedAI(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice := f.user(t, "alice")
	team := f.teamOf(t, alice, "Team")
	legacy := f.legacy(t, alice, "billing", "billing")
	homeID := f.placeUnlinked(t, alice, legacy)
	home, err := f.st.R().GetOrganization(ctx, homeID)
	require.NoError(t, err)

	id := FolderID{Machine: "devbox", Folder: "/work/billing", Adopt: "billing", Org: team.Slug}
	_, err = f.reg.ResolveFolder(ctx, alice.UserID, id, false)
	requireMismatch(t, err, home)
	require.Empty(t, f.storedAgent(t, legacy).Folder, "the identity stays unlinked")
	require.Zero(t, f.adoptions(t, legacy), "and nothing about it is audited")

	id.Org = home.Slug
	a, err := f.reg.ResolveFolder(ctx, alice.UserID, id, false)
	require.NoError(t, err)
	require.Equal(t, legacy, a.ID, "naming its home organization adopts it")
	require.Equal(t, "/work/billing", f.storedAgent(t, legacy).Folder)
}

// ORG-04: a first session that loses the race for a new folder is a later start of the AI the winner created:
// held to the winner's home organization. The loser has found no AI for its folder, another session creates one
// in another organization before it does, and it is refused with 409 organization_mismatch instead of being
// handed an AI it asked to place elsewhere.
func TestAFirstSessionThatLosesTheRaceIsHeldToTheWinnersOrganization(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice := f.user(t, "alice")
	team := f.teamOf(t, alice, "Team")
	personalID, err := orgs.DefaultFor(ctx, f.st.R(), alice.UserID)
	require.NoError(t, err)
	personal, err := f.st.R().GetOrganization(ctx, personalID)
	require.NoError(t, err)

	missed, release := make(chan struct{}), make(chan struct{})
	var waiting atomic.Bool
	SetAfterFolderMiss(f.reg, func() {
		if waiting.CompareAndSwap(false, true) { // only the first session waits
			close(missed)
			<-release
		}
	})
	loser := make(chan error, 1)
	go func() {
		_, err := f.reg.ResolveFolder(ctx, alice.UserID, FolderID{Machine: "devbox", Folder: "/work/race", Org: personal.Slug}, false)
		loser <- err
	}()
	<-missed // the first session found no AI and waits

	winner, err := f.reg.ResolveFolder(ctx, alice.UserID, FolderID{Machine: "devbox", Folder: "/work/race", Org: team.Slug}, false)
	require.NoError(t, err)
	require.Equal(t, team.ID, winner.OrgID.String)
	close(release)

	requireMismatch(t, <-loser, team)
	require.Equal(t, 1, f.count(t, "SELECT COUNT(*) FROM agents"), "one AI")
	require.Equal(t, team.ID, f.storedAgent(t, winner.ID).OrgID.String)
	require.Equal(t, 1, f.count(t, "SELECT COUNT(*) FROM org_members WHERE iid = ?", winner.ID), "in one organization")
}
