package orgs

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// shareOrg is the chart of the share-trust tests (ST-01, ST-05):
//
//	Admin: ana (admin), carl (member, already a co-holder)
//	  Lead: max (member)
//	    Worker: zed (member), bolt (an AI of the organization made by ana), ext (an AI of another organization)
//	no position: ben (admin), mia (member), gus (a guest mia invited), atlas (an AI of the organization made by ana)
type shareOrg struct {
	org, admin, lead, worker                          string
	ana, carl, max, zed, ben, mia, gus, outsider, oth string
	atlas, bolt, ext                                  string
}

func newShareOrg(f *fixture) shareOrg {
	s := shareOrg{org: f.org("share")}
	s.admin = f.position(s.org, "Admin", "")
	s.lead = f.position(s.org, "Lead", s.admin)
	s.worker = f.position(s.org, "Worker", s.lead)
	s.ana, s.carl, s.max, s.zed = f.user("ana"), f.user("carl"), f.user("max"), f.user("zed")
	s.ben, s.mia, s.gus, s.outsider = f.user("ben"), f.user("mia"), f.user("gus"), f.user("outsider")
	f.join(s.org, s.ana, PermissionAdmin, s.admin, "")
	f.join(s.org, s.carl, PermissionMember, s.admin, "")
	f.join(s.org, s.max, PermissionMember, s.lead, "")
	f.join(s.org, s.zed, PermissionMember, s.worker, "")
	f.join(s.org, s.ben, PermissionAdmin, "", "")
	f.join(s.org, s.mia, PermissionMember, "", "")
	f.join(s.org, s.gus, PermissionGuest, "", s.mia)
	s.atlas, s.bolt = f.ai("atlas", s.ana), f.ai("bolt", s.ana)
	f.home(s.atlas, s.org, s.ana, PermissionMember, "")
	f.home(s.bolt, s.org, s.ana, PermissionMember, s.worker)
	// ext lives in another organization and is only a member here.
	s.oth = f.org("other")
	f.join(s.oth, s.outsider, PermissionAdmin, "", "")
	s.ext = f.ai("ext", s.outsider)
	f.home(s.ext, s.oth, s.outsider, PermissionMember, "")
	f.join(s.org, s.ext, PermissionMember, s.worker, "")
	return s
}

// sharable lists, for the viewer, the members whose row offers "the same access as me".
func sharable(t *testing.T, f *fixture, org, viewer string) []string {
	t.Helper()
	rows, err := f.svc.ListMembers(context.Background(), viewer, org)
	require.NoError(t, err)
	out := []string{}
	for _, r := range rows {
		if r.CanShareAccess {
			out = append(out, r.IID)
		}
	}
	return out
}

// ST-01: the flag follows the move rule (POS-04): only a viewer who holds a position and may put the person
// in it, which today is an admin. Never the viewer itself, an AI or someone who already holds the position.
func TestCanShareAccessFollowsThePositionRule(t *testing.T) {
	f := newFixture(t)
	s := newShareOrg(f)

	// ana (admin, Admin position): every person outside her position; not herself, not carl (already there),
	// not the AIs.
	require.ElementsMatch(t, []string{s.max, s.zed, s.ben, s.mia, s.gus}, sharable(t, f, s.org, s.ana))
	// An admin without a position has no position to share.
	require.Empty(t, sharable(t, f, s.org, s.ben))
	// A manager who is not an admin may only place below its own level (POS-04).
	require.Empty(t, sharable(t, f, s.org, s.max))
	// A member without a position and a guest: nothing.
	require.Empty(t, sharable(t, f, s.org, s.mia))
	require.Empty(t, sharable(t, f, s.org, s.gus))
	// The co-holder who is a plain member cannot pass the position on either.
	require.Empty(t, sharable(t, f, s.org, s.carl))

	// What the flag promises the move does; what it refuses the move refuses.
	ctx := context.Background()
	_, err := f.svc.SetPosition(ctx, s.max, s.org, s.zed, s.lead)
	require.ErrorIs(t, err, ErrForbidden, "a manager cannot put someone at its own level")
	_, err = f.svc.SetPosition(ctx, s.gus, s.org, s.mia, s.admin)
	require.ErrorIs(t, err, ErrForbidden, "a guest cannot")
	_, err = f.svc.SetPosition(ctx, s.mia, s.org, s.zed, s.admin)
	require.ErrorIs(t, err, ErrForbidden, "a member without a position cannot")
	_, err = f.svc.SetPosition(ctx, s.outsider, s.org, s.mia, s.admin)
	require.ErrorIs(t, err, ErrNotFound, "an identity of another organization does not find it")
}

// ST-04: giving the same access is the ordinary position change, recorded as one org.member_position entry
// with where the member was and where it went.
func TestShareAccessRecordsThePositionChange(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	s := newShareOrg(f)

	m, err := f.svc.SetPosition(ctx, s.ana, s.org, s.mia, s.admin)
	require.NoError(t, err)
	require.Equal(t, s.admin, m.PositionID)
	require.Equal(t, []string{"org.member_position " + s.org}, f.audited())
	require.JSONEq(t, `{"iid":"`+s.mia+`","from":"","to":"`+s.admin+`"}`,
		f.str(`SELECT detail_json FROM audit_log WHERE action = 'org.member_position'`))

	// Now mia manages what ana's position manages, and bolt (home here, below) trusts her.
	trusted, err := f.z.Trusts(ctx, s.mia, s.bolt)
	require.NoError(t, err)
	require.True(t, trusted)
	// But not atlas, which ana manages only because she created it.
	trusted, err = f.z.Trusts(ctx, s.mia, s.atlas)
	require.NoError(t, err)
	require.False(t, trusted)
	require.NotContains(t, sharable(t, f, s.org, s.ana), s.mia, "a co-holder is not offered again")
}

// ST-05: the preview says whom the member would manage, which AIs would trust it and what the actor manages
// that the member still would not, with the authorization of the move itself.
func TestPreviewMoveSaysWhatTheMemberWillManage(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	s := newShareOrg(f)
	next := f.events()

	e, err := f.svc.PreviewMove(ctx, s.ana, s.org, s.mia, s.admin)
	require.NoError(t, err)
	// Everyone below Admin, and gus whom mia invited.
	require.ElementsMatch(t, []string{s.max, s.zed, s.bolt, s.ext, s.gus}, e.WillManage)
	// Only bolt: ext is managed here but its home is another organization (POS-10).
	require.Equal(t, []string{s.bolt}, e.WillBeTrustedBy)
	// atlas is ana's through creation only (POS-03), not through the position.
	require.Equal(t, []string{s.atlas}, e.NotShared)
	require.Equal(t, []string{}, e.Lose, "nobody manages mia now")
	require.Equal(t, Member{IID: s.mia, Permission: PermissionMember}, memberOfOrg(t, f, s.org, s.mia), "nothing moved")
	require.Empty(t, f.audited())
	require.Empty(t, next())

	// zed, who sits under max, would leave the management of everyone above it by moving up to Admin: max
	// first (one level), then the holders of Admin.
	e, err = f.svc.PreviewMove(ctx, s.ana, s.org, s.zed, s.admin)
	require.NoError(t, err)
	require.Equal(t, []string{s.max, s.ana, s.carl}, e.Lose)
	require.NotContains(t, e.NotShared, s.zed, "the member itself is never listed")

	// The move's refusals.
	_, err = f.svc.PreviewMove(ctx, s.max, s.org, s.zed, s.lead)
	require.ErrorIs(t, err, ErrForbidden)
	_, err = f.svc.PreviewMove(ctx, s.gus, s.org, s.mia, s.admin)
	require.ErrorIs(t, err, ErrForbidden)
	_, err = f.svc.PreviewMove(ctx, s.outsider, s.org, s.mia, s.admin)
	require.ErrorIs(t, err, ErrNotFound)

	// MovePreview is still the Lose part.
	lose, err := f.svc.MovePreview(ctx, s.ana, s.org, s.zed, s.admin)
	require.NoError(t, err)
	require.Equal(t, e.Lose, lose)
}
