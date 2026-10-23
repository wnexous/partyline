package orgs

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/audit"
	"github.com/wnexous/partyline/internal/avatars"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/validation"
)

// invalid asserts that err is a validation error that names exactly the field.
func invalid(t *testing.T, err error, field string) {
	t.Helper()
	var verr *validation.Error
	require.ErrorAs(t, err, &verr)
	require.Contains(t, verr.Fields, field)
	require.Len(t, verr.Fields, 1)
}

func ptr[T any](v T) *T { return &v }

// ORG-01: the creator becomes admin of the new organization and holds a root position named "Admin".
func TestCreateMakesTheCreatorAdminHoldingTheRootAdminPosition(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user("alice")
	next := f.events()

	got, err := f.svc.Create(ctx, alice, "Acme Inc.")
	require.NoError(t, err)

	require.NotEmpty(t, got.ID)
	require.Equal(t, "Acme Inc.", got.Name)
	require.Equal(t, "acme-inc", got.Slug)
	require.Equal(t, alice, got.CreatedBy)
	require.Equal(t, t0.UnixMilli(), got.CreatedAt)
	require.Equal(t, t0.UnixMilli(), got.UpdatedAt)
	require.Empty(t, got.AvatarSha256)
	require.Equal(t, PermissionAdmin, got.Permission, "the view is the creator's own membership")

	c, err := Load(ctx, f.st.R(), got.ID)
	require.NoError(t, err)
	require.Len(t, c.Positions, 1, "one position and no other")
	root := c.Positions[got.PositionID]
	require.Equal(t, Position{ID: got.PositionID, Name: "Admin"}, root, "the root position Admin")
	require.Equal(t, map[string]Member{alice: {IID: alice, Permission: PermissionAdmin, PositionID: root.ID}}, c.Members,
		"alice is the only member: admin holding Admin")

	require.Equal(t, []events.Event{events.OrgChanged{OrgID: got.ID}}, next(), "announced once, after the commit")
	require.Equal(t, []string{"org.create " + got.ID}, f.audited())
	rows, _, err := f.audit.List(ctx, "")
	require.NoError(t, err)
	require.Equal(t, alice, rows[0].ActorID)
	require.Equal(t, audit.TargetOrganization, rows[0].TargetKind)
	var detail map[string]any
	require.NoError(t, json.Unmarshal([]byte(rows[0].DetailJson), &detail))
	require.Equal(t, map[string]any{"name": "Acme Inc.", "slug": "acme-inc"}, detail)
}

// ORG-01: a name has 1 to 100 characters, counted in code points, not bytes (L-023).
func TestCreateNameBounds(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user("alice")
	accepted := 0
	for name, ok := range map[string]bool{
		"":                                     false,
		"   \t\n ":                             false,
		"\x00\x07":                             false,
		"a":                                    true,
		strings.Repeat("a", 100):               true,
		strings.Repeat("a", 101):               false,
		strings.Repeat("é", 100):               true, // 200 bytes, 100 characters
		strings.Repeat("é", 101):               false,
		strings.Repeat("李", 100):               true, // 300 bytes
		strings.Repeat("李", 101):               false,
		"  " + strings.Repeat("a", 100) + "  ": true, // trimmed first
	} {
		_, err := f.svc.Create(ctx, alice, name)
		if ok {
			require.NoError(t, err, "%d characters", len([]rune(name)))
			accepted++
			continue
		}
		invalid(t, err, "name")
	}
	require.Equal(t, 5, accepted, "fixture sanity")
	require.Equal(t, accepted, f.count(`SELECT COUNT(*) FROM organizations`), "a refused name creates nothing")
	require.Equal(t, accepted, f.count(`SELECT COUNT(*) FROM positions`))
	require.Equal(t, accepted, f.count(`SELECT COUNT(*) FROM org_members`))

	got, err := f.svc.Create(ctx, alice, "  Ana\x00 Maria\n ")
	require.NoError(t, err)
	require.Equal(t, "Ana Maria", got.Name, "control characters are dropped and the name is trimmed")
}

// ORG-01: only a user creates an organization. An AI, an unknown identity and a disabled account are
// refused and nothing is created.
func TestCreateIsForActiveUsers(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user("alice")
	bot := f.ai("bot", alice)
	gone := f.user("gone")
	f.tx(func(ctx context.Context, q *db.Queries) error {
		return q.SetUserDisabledAt(ctx, db.SetUserDisabledAtParams{DisabledAt: sql.NullInt64{Int64: 1, Valid: true}, ID: gone})
	})
	for name, creator := range map[string]string{"an AI": bot, "an unknown identity": "u-nobody", "a disabled user": gone, "nobody": ""} {
		_, err := f.svc.Create(ctx, creator, "Acme")
		require.ErrorIs(t, err, ErrForbidden, name)
	}
	require.Zero(t, f.count(`SELECT COUNT(*) FROM organizations`))
	require.Empty(t, f.audited())
}

// ORG-01: the slug is derived from the name; a second organization with the same address gets a
// suffix, whatever the case.
func TestCreateGivesADuplicateSlugASuffix(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice, bob := f.user("alice"), f.user("bob")
	var slugs []string
	for _, tc := range []struct{ creator, name string }{
		{alice, "Acme"}, {bob, "Acme"}, {alice, "Acme"}, {bob, "ACME!"}, {bob, "李雷"},
	} {
		got, err := f.svc.Create(ctx, tc.creator, tc.name)
		require.NoError(t, err)
		slugs = append(slugs, got.Slug)
	}
	require.Equal(t, []string{"acme", "acme-2", "acme-3", "acme-4", "org"}, slugs)
}

// CHART-02: an organization is seen by its members, guests included, and by nobody else.
func TestGetIsForMembers(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	for _, tc := range []struct {
		viewer string
		want   Permission
	}{{a.alice, PermissionAdmin}, {a.bob, PermissionMember}, {a.gus, PermissionGuest}, {a.notes, PermissionMember}} {
		got, err := f.svc.Get(ctx, tc.viewer, a.org)
		require.NoError(t, err, tc.viewer)
		require.Equal(t, a.org, got.ID)
		require.Equal(t, tc.want, got.Permission, tc.viewer)
	}
	got, err := f.svc.Get(ctx, a.walt, a.org)
	require.NoError(t, err)
	require.Equal(t, a.trees, got.PositionID, "the viewer's own position")

	_, err = f.svc.Get(ctx, f.user("out"), a.org)
	require.ErrorIs(t, err, ErrNotFound, "an outsider")
	_, err = f.svc.Get(ctx, a.alice, "o-nothing")
	require.ErrorIs(t, err, ErrNotFound, "no such organization")
	_, err = f.svc.Get(ctx, "", a.org)
	require.ErrorIs(t, err, ErrNotFound)
}

// ORG-04: --org takes a slug or an id, ignoring case; an id wins over a slug that looks like one.
func TestResolveTakesASlugOrAnID(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	acme := f.org("acme")
	globex := f.org("globex")
	for _, ref := range []string{acme, "acme", "ACME", " Acme "} {
		got, err := f.svc.Resolve(ctx, ref)
		require.NoError(t, err, ref)
		require.Equal(t, acme, got.ID, ref)
	}
	got, err := f.svc.Resolve(ctx, globex)
	require.NoError(t, err)
	require.Equal(t, "globex", got.Slug)
	for _, ref := range []string{"", "nothing", "o-nothing", "acme-2"} {
		_, err := f.svc.Resolve(ctx, ref)
		require.ErrorIs(t, err, ErrNotFound, "%q", ref)
	}

	// An organization whose slug equals another's id: the id is the address.
	f.tx(func(ctx context.Context, q *db.Queries) error {
		return q.CreateOrganization(ctx, db.CreateOrganizationParams{ID: "o-trick", Name: "Trick", Slug: globex, CreatedAt: 1, UpdatedAt: 1})
	})
	got, err = f.svc.Resolve(ctx, globex)
	require.NoError(t, err)
	require.Equal(t, globex, got.ID, "the id wins")
}

// OMEM-03: an identity lists the organizations it belongs to with its own permission and position in
// each, guests too, by name.
func TestListMine(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	zeta := f.org("zeta")
	beta := f.org("beta")
	f.join(zeta, a.lee, PermissionAdmin, "", "")
	f.join(beta, a.lee, PermissionGuest, "", a.bob)

	got, err := f.svc.ListMine(ctx, a.lee)
	require.NoError(t, err)
	var seen []string
	for _, m := range got {
		seen = append(seen, m.Slug+":"+string(m.Permission)+":"+m.PositionID)
	}
	require.Equal(t, []string{"acme:member:" + a.lead, "beta:guest:", "zeta:admin:"}, seen, "ordered by name; each with lee's own permission and position")

	none, err := f.svc.ListMine(ctx, f.user("out"))
	require.NoError(t, err)
	require.Empty(t, none)
	none, err = f.svc.ListMine(ctx, "")
	require.NoError(t, err)
	require.Empty(t, none)
}

// OMEM-04: only an admin renames the organization or changes its address; an outsider does not see
// it and a member sees it but may not.
func TestUpdateIsForAdmins(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	in := UpdateInput{Name: ptr("Acme Corp")}

	_, err := f.svc.Update(ctx, f.user("out"), a.org, in)
	require.ErrorIs(t, err, ErrNotFound)
	for _, member := range []string{a.bob, a.gus, a.lee, a.notes} {
		_, err = f.svc.Update(ctx, member, a.org, in)
		require.ErrorIs(t, err, ErrForbidden, member)
	}
	_, err = f.svc.Update(ctx, a.alice, "o-nothing", in)
	require.ErrorIs(t, err, ErrNotFound)
	require.Equal(t, "acme", f.str(`SELECT name FROM organizations WHERE id = ?`, a.org), "nothing changed")
	require.Empty(t, f.audited())

	next := f.events()
	f.clock.Advance(time.Second)
	got, err := f.svc.Update(ctx, a.alice, a.org, in)
	require.NoError(t, err)
	require.Equal(t, "Acme Corp", got.Name)
	require.Equal(t, "acme", got.Slug, "the address stays unless asked")
	require.Equal(t, PermissionAdmin, got.Permission)
	require.Equal(t, t0.UnixMilli()+1000, got.UpdatedAt)
	require.Equal(t, int64(1), got.CreatedAt)
	require.Equal(t, []events.Event{events.OrgChanged{OrgID: a.org}}, next())
	require.Equal(t, []string{"org.update " + a.org}, f.audited())
}

// Authorization comes before validation: someone who may not change the organization is refused
// whatever they send, so a bad field never tells an outsider or a member anything.
func TestUpdateRefusesBeforeItValidates(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	bad := UpdateInput{Name: ptr(""), Slug: ptr("Not A Slug")}
	_, err := f.svc.Update(ctx, f.user("out"), a.org, bad)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = f.svc.Update(ctx, a.bob, a.org, bad)
	require.ErrorIs(t, err, ErrForbidden)
	_, err = f.svc.Update(ctx, a.alice, a.org, bad)
	var verr *validation.Error
	require.ErrorAs(t, err, &verr, "an admin is told what is wrong")
	require.Len(t, verr.Fields, 2)
}

// ORG-01: name and slug follow their rules on update; an address belongs to one organization.
func TestUpdateValidatesNameAndSlug(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	other := f.org("globex")

	for _, name := range []string{"", "  ", strings.Repeat("x", 101)} {
		_, err := f.svc.Update(ctx, a.alice, a.org, UpdateInput{Name: ptr(name)})
		invalid(t, err, "name")
	}
	for _, slug := range []string{"", "Acme Corp", "acme_corp", "-acme", "acme-", "ac--me", "é", strings.Repeat("a", 41), "a/b"} {
		_, err := f.svc.Update(ctx, a.alice, a.org, UpdateInput{Slug: ptr(slug)})
		invalid(t, err, "slug")
	}
	_, err := f.svc.Update(ctx, a.alice, a.org, UpdateInput{Slug: ptr("GLOBEX")})
	require.ErrorIs(t, err, ErrSlugTaken, "another organization's address, in any case")
	_, err = f.svc.Update(ctx, a.alice, a.org, UpdateInput{Name: ptr(""), Slug: ptr("GLOBEX")})
	invalid(t, err, "name") // a bad field is reported before a taken address
	require.Equal(t, "acme", f.str(`SELECT slug FROM organizations WHERE id = ?`, a.org), "nothing changed")
	require.Empty(t, f.audited())

	got, err := f.svc.Update(ctx, a.alice, a.org, UpdateInput{Slug: ptr("Acme-Labs")})
	require.NoError(t, err)
	require.Equal(t, "acme-labs", got.Slug, "stored lower case")
	require.Equal(t, "acme", got.Name)
	_, err = f.svc.Resolve(ctx, "acme")
	require.ErrorIs(t, err, ErrNotFound, "the old address is free")
	got, err = f.svc.Update(ctx, a.alice, a.org, UpdateInput{Slug: ptr(strings.Repeat("a", 40))})
	require.NoError(t, err)
	require.Len(t, got.Slug, 40)
	_, err = f.svc.Update(ctx, f.adminOf(other), other, UpdateInput{Slug: ptr("acme-labs")})
	require.NoError(t, err, "a freed address can be taken")
}

// An update that changes nothing is not an event: no audit entry, no announcement, no new updated_at.
func TestUpdateThatChangesNothingIsSilent(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	next := f.events()
	f.clock.Advance(5 * time.Second)
	for _, in := range []UpdateInput{{}, {Name: ptr("acme")}, {Slug: ptr("ACME")}, {Name: ptr("acme"), Slug: ptr("acme")}} {
		got, err := f.svc.Update(ctx, a.alice, a.org, in)
		require.NoError(t, err)
		require.Equal(t, "acme", got.Name)
		require.Equal(t, int64(1), got.UpdatedAt, "the clock moved, the row did not")
	}
	require.Empty(t, next())
	require.Empty(t, f.audited())
}

// ORG-06: an organization that still owns an AI, a connection or a group cannot be deleted; each one
// alone is enough, and the refusal counts them (L-018).
func TestDeleteRefusesWhileTheOrganizationOwnsSomething(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name                       string
		own                        func(f *fixture, a acmeChart)
		agents, connectors, groups int64
	}{
		{"an AI", func(f *fixture, a acmeChart) {
			f.ai("owned", a.alice)
			f.home("a-owned", a.org, a.alice, PermissionMember, "")
		}, 1, 0, 0},
		{"a connection", func(f *fixture, a acmeChart) { f.connector("k1", a.alice, a.org) }, 0, 1, 0},
		{"a group", func(f *fixture, a acmeChart) { f.group("g1", a.alice, a.org) }, 0, 0, 1},
		{"all of them", func(f *fixture, a acmeChart) {
			f.ai("owned", a.alice)
			f.home("a-owned", a.org, a.alice, PermissionMember, "")
			f.connector("k1", a.alice, a.org)
			f.connector("k2", a.alice, a.org)
			f.group("g1", a.alice, a.org)
			f.group("g2", a.alice, a.org)
			f.group("g3", a.alice, a.org)
		}, 1, 2, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			a := f.bareOrg() // people and a chart, but no AI, connection or group
			tc.own(f, a)
			next := f.events()

			err := f.svc.Delete(ctx, a.alice, a.org)

			require.ErrorIs(t, err, ErrNotEmpty)
			var blocked *NotEmptyError
			require.ErrorAs(t, err, &blocked)
			require.Equal(t, tc.agents, blocked.Agents, "AIs")
			require.Equal(t, tc.connectors, blocked.Connectors, "connections")
			require.Equal(t, tc.groups, blocked.Groups, "groups")
			require.Equal(t, 1, f.count(`SELECT COUNT(*) FROM organizations WHERE id = ?`, a.org), "nothing was deleted")
			require.Equal(t, 6, f.count(`SELECT COUNT(*) FROM positions WHERE org_id = ?`, a.org))
			require.Equal(t, 8+int(tc.agents), f.count(`SELECT COUNT(*) FROM org_members WHERE org_id = ?`, a.org))
			require.Empty(t, next())
			require.Empty(t, f.audited())
		})
	}
}

func TestDeleteRemovesAnEmptyOrganizationWithItsChart(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.bareOrg()
	other := f.org("globex")
	f.join(other, a.bob, PermissionMember, "", "")
	zed := f.user("zed")
	f.exec(`UPDATE users SET default_org_id = ? WHERE id = ?`, a.org, a.bob)
	f.exec(`INSERT INTO org_invitations (id, org_id, invitee_iid, permission, invited_by, created_at, expires_at) VALUES ('i1', ?, ?, 'guest', ?, 1, 2)`,
		a.org, zed, a.bob)
	people := f.count(`SELECT COUNT(*) FROM users`)
	f.group("dm-like", a.alice, "") // a room with no organization, like a DM: it never blocks
	next := f.events()

	require.NoError(t, f.svc.Delete(ctx, a.alice, a.org))

	require.Zero(t, f.count(`SELECT COUNT(*) FROM organizations WHERE id = ?`, a.org))
	require.Zero(t, f.count(`SELECT COUNT(*) FROM positions WHERE org_id = ?`, a.org))
	require.Zero(t, f.count(`SELECT COUNT(*) FROM org_members WHERE org_id = ?`, a.org))
	require.Zero(t, f.count(`SELECT COUNT(*) FROM org_invitations WHERE org_id = ?`, a.org))
	require.Equal(t, 1, f.count(`SELECT COUNT(*) FROM org_members WHERE org_id = ?`, other), "another organization is untouched")
	require.Equal(t, 1, f.count(`SELECT COUNT(*) FROM users WHERE id = ? AND default_org_id IS NULL`, a.bob), "the default organization is cleared")
	require.Equal(t, people, f.count(`SELECT COUNT(*) FROM users`), "the people stay")
	require.Equal(t, 1, f.count(`SELECT COUNT(*) FROM rooms WHERE id = 'dm-like'`))
	require.Equal(t, []events.Event{events.OrgChanged{OrgID: a.org}}, next())
	require.Equal(t, []string{"org.delete " + a.org}, f.audited())
	_, err := f.svc.Get(ctx, a.alice, a.org)
	require.ErrorIs(t, err, ErrNotFound)
}

// OMEM-04: only an admin deletes the organization.
func TestDeleteIsForAdmins(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.bareOrg()
	require.ErrorIs(t, f.svc.Delete(ctx, f.user("out"), a.org), ErrNotFound)
	for _, member := range []string{a.bob, a.gus, a.lee} {
		require.ErrorIs(t, f.svc.Delete(ctx, member, a.org), ErrForbidden, member)
	}
	require.ErrorIs(t, f.svc.Delete(ctx, a.alice, "o-nothing"), ErrNotFound)
	require.Equal(t, 1, f.count(`SELECT COUNT(*) FROM organizations WHERE id = ?`, a.org))
	require.Empty(t, f.audited())
	require.NoError(t, f.svc.Delete(ctx, a.alice, a.org))
}

// pngOf encodes a w×h image.
func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := range w {
		for y := range h {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 200, A: 255})
		}
	}
	var out bytes.Buffer
	require.NoError(t, png.Encode(&out, img))
	return out.Bytes()
}

// OMEM-04: an admin sets and clears the avatar of the organization, stored as a 256x256 PNG; members
// see it, outsiders do not.
func TestAvatarIsSetByAdminsAndSeenByMembers(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	img := pngOf(t, 300, 200)
	out := f.user("out")
	next := f.events()

	_, err := f.svc.SetAvatar(ctx, out, a.org, bytes.NewReader(img))
	require.ErrorIs(t, err, ErrNotFound)
	for _, member := range []string{a.bob, a.gus} {
		_, err = f.svc.SetAvatar(ctx, member, a.org, bytes.NewReader(img))
		require.ErrorIs(t, err, ErrForbidden, member)
		require.ErrorIs(t, f.svc.ClearAvatar(ctx, member, a.org), ErrForbidden, member)
	}
	_, err = f.svc.SetAvatar(ctx, a.alice, a.org, strings.NewReader("not an image"))
	require.ErrorIs(t, err, avatars.ErrInvalid)
	require.Empty(t, next())

	got, err := f.svc.SetAvatar(ctx, a.alice, a.org, bytes.NewReader(img))
	require.NoError(t, err)
	require.NotEmpty(t, got.AvatarSha256)
	require.Equal(t, []events.Event{events.OrgChanged{OrgID: a.org}}, next())
	require.Equal(t, []string{"org.update " + a.org}, f.audited())

	for _, viewer := range []string{a.alice, a.gus} {
		file, sha, err := f.svc.Avatar(ctx, viewer, a.org)
		require.NoError(t, err, viewer)
		require.Equal(t, got.AvatarSha256, sha)
		cfg, err := png.DecodeConfig(file)
		require.NoError(t, err)
		require.Equal(t, 256, cfg.Width)
		require.Equal(t, 256, cfg.Height)
		require.NoError(t, file.Close())
	}
	_, _, err = f.svc.Avatar(ctx, out, a.org)
	require.ErrorIs(t, err, ErrNotFound, "an outsider")

	// A bad upload keeps the avatar; clearing removes it and a second clear is quiet.
	_, err = f.svc.SetAvatar(ctx, a.alice, a.org, strings.NewReader("still not an image"))
	require.ErrorIs(t, err, avatars.ErrInvalid)
	require.Equal(t, got.AvatarSha256, f.str(`SELECT avatar_sha256 FROM organizations WHERE id = ?`, a.org))
	require.NoError(t, f.svc.ClearAvatar(ctx, a.alice, a.org))
	require.Empty(t, f.str(`SELECT avatar_sha256 FROM organizations WHERE id = ?`, a.org))
	_, _, err = f.svc.Avatar(ctx, a.alice, a.org)
	require.ErrorIs(t, err, ErrNotFound, "no avatar")
	next()
	require.NoError(t, f.svc.ClearAvatar(ctx, a.alice, a.org))
	require.Empty(t, next(), "clearing nothing announces nothing")
}

// The avatar blob must survive the retention sweep while the organization points at it: the sweep
// asks the store, and the store counts organizations.
func TestCountOrganizationAvatarSeesTheBlobInUse(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	got, err := f.svc.SetAvatar(ctx, a.alice, a.org, bytes.NewReader(pngOf(t, 64, 64)))
	require.NoError(t, err)
	n, err := f.st.R().CountOrganizationAvatar(ctx, got.AvatarSha256)
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
	n, err = f.st.R().CountOrganizationAvatar(ctx, "other")
	require.NoError(t, err)
	require.Zero(t, n)
}
