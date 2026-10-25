package api_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/gif"
	"image/png"
	"net/http"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/attachments"
	"github.com/wnexous/partyline/internal/testkit"
)

/*
 * A photo for groups (GAV-01..GAV-05, messenger-polish): PUT/DELETE/GET /rooms/{id}/avatar work like the
 * avatar routes of an intelligence (the same processing, the same blob store), with the manage level on the
 * group deciding who changes it.
 */

func roomAvatarPath(roomID string) string { return "/api/v1/rooms/" + roomID + "/avatar" }

// putRoomAvatar sends the image as the raw request body, as the web app does.
func putRoomAvatar(c *testkit.Client, roomID string, body []byte) *testkit.Response {
	c.T.Helper()
	return c.DoRaw(http.MethodPut, roomAvatarPath(roomID), "image/png", bytes.NewReader(body))
}

func getRoomAvatar(c *testkit.Client, roomID string) *testkit.Response {
	c.T.Helper()
	return c.Get(roomAvatarPath(roomID))
}

func deleteRoomAvatar(c *testkit.Client, roomID string) *testkit.Response {
	c.T.Helper()
	return c.Delete(roomAvatarPath(roomID))
}

var roomAvatarURLPattern = regexp.MustCompile(`^/api/v1/rooms/([0-9a-f-]+)/avatar\?v=([0-9a-f]{64})$`)

// GAV-01: an accepted photo is stored centre-cropped and resized to a 256x256 PNG and served to the people who
// can see the group with the headers of the other avatars; the URL in the room carries the content hash as its
// cache key, and the list and the conversation list carry it too. PNG, JPEG and WebP are accepted.
func TestRoomAvatarIsStoredAsASquarePNGAndServed(t *testing.T) {
	env := testkit.New(t)
	alice, bob := env.NewUser("alice"), env.NewUser("bob")
	room := createRoom(alice, "ops")
	addToRoom(t, env, alice, room.ID, bob)
	getRoomAvatar(alice, room.ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	require.Empty(t, room.AvatarURL)

	webp, err := os.ReadFile("../avatars/testdata/tiny.webp")
	require.NoError(t, err)
	for name, body := range map[string][]byte{
		"png": pngBytes(t, bands(400, 200)), "jpeg": jpegBytes(t, bands(400, 200)), "webp": webp,
	} {
		put := testkit.Decode[dto.Room](putRoomAvatar(alice, room.ID, body).RequireStatus(http.StatusOK))
		m := roomAvatarURLPattern.FindStringSubmatch(put.AvatarURL)
		require.NotNil(t, m, "%s: avatar_url %q", name, put.AvatarURL)
		require.Equal(t, room.ID, m[1], name)
		require.Equal(t, put, roomOf(t, alice, room.ID), "%s: the PUT answers with the stored room", name)
		require.Equal(t, put.AvatarURL, roomOf(t, bob, room.ID).AvatarURL, "%s: the people in the room see it", name)

		for _, viewer := range []*testkit.Client{alice, bob} {
			resp := getRoomAvatar(viewer, room.ID).RequireStatus(http.StatusOK)
			require.Equal(t, "image/png", resp.Header.Get("Content-Type"), name)
			require.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"), name)
			require.Contains(t, resp.Header.Get("Content-Security-Policy"), "default-src 'none'", name)
			require.Equal(t, "private, max-age=31536000, immutable", resp.Header.Get("Cache-Control"), name)
			sum := sha256.Sum256(resp.Body)
			require.Equal(t, m[2], hex.EncodeToString(sum[:]), "%s: the cache key is the hash of the served bytes", name)
			img, err := png.Decode(bytes.NewReader(resp.Body))
			require.NoError(t, err, name)
			require.Equal(t, image.Rect(0, 0, 256, 256), img.Bounds(), name)
			if name != "webp" {
				requireGreenSquare(t, resp.Body)
			}
		}
		etag := `"` + m[2] + `"`
		require.Equal(t, etag, getRoomAvatar(alice, room.ID).Header.Get("ETag"), name)
		alice.WithHeader("If-None-Match", etag).Get(roomAvatarPath(room.ID)).RequireStatus(http.StatusNotModified) // the browser revalidates for free
	}
	env.Anon().Get(roomAvatarPath(room.ID)).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
}

// GAV-04: the room, the list of rooms and the conversation list of every viewer carry the photo of a group, and
// a group without one, like a DM, carries none (the key is absent, as for every other avatar).
func TestRoomAvatarURLInRoomsAndConversations(t *testing.T) {
	env := testkit.New(t)
	alice, bob := env.NewUser("alice"), env.NewUser("bob")
	room := createRoom(alice, "ops")
	addToRoom(t, env, alice, room.ID, bob)
	bare := createRoom(alice, "bare")
	dm := storeDM(t, env, alice.User.ID, alice.User.ID, bob.User.ID)

	photo := testkit.Decode[dto.Room](putRoomAvatar(alice, room.ID, pngBytes(t, bands(300, 300))).RequireStatus(http.StatusOK)).AvatarURL
	require.Regexp(t, roomAvatarURLPattern, photo)

	for _, viewer := range []*testkit.Client{alice, bob} {
		rooms := map[string]dto.Room{}
		for _, r := range testkit.Decode[dto.List[dto.Room]](viewer.Get("/api/v1/rooms").RequireStatus(http.StatusOK)).Items {
			rooms[r.ID] = r
		}
		require.Equal(t, photo, rooms[room.ID].AvatarURL, "the rooms list")
		require.Empty(t, rooms[dm].AvatarURL, "a DM has no avatar of its own")
		if viewer == alice {
			require.Empty(t, rooms[bare.ID].AvatarURL, "a group without a photo")
		}

		conversations := map[string]dto.Conversation{}
		for _, c := range testkit.Decode[dto.List[dto.Conversation]](viewer.Get("/api/v1/conversations").RequireStatus(http.StatusOK)).Items {
			conversations[c.ID] = c
		}
		require.Equal(t, photo, conversations[room.ID].AvatarURL, "the conversation list")
		require.Empty(t, conversations[dm].AvatarURL, "a DM is drawn with its counterpart, not with a photo of its own")
		require.NotNil(t, conversations[dm].Counterpart)
	}

	require.NotContains(t, string(alice.Get("/api/v1/rooms/"+bare.ID).RequireStatus(http.StatusOK).Body), "avatar_url", "the key is absent without a photo")
	require.Contains(t, string(alice.Get("/api/v1/rooms/"+room.ID).RequireStatus(http.StatusOK).Body), `"avatar_url":"`+photo+`"`)
}

// roomAvatarSetup is roomSetup with a photo on the group, so that reading it has something to return.
func roomAvatarSetup(env *testkit.Env, roles *testkit.OrgRoles) func(t *testing.T) map[string]string {
	setup := roomSetup(env, roles)
	return func(t *testing.T) map[string]string {
		res := setup(t)
		creator := *roles.Member
		creator.T = t
		putRoomAvatar(&creator, res["room"], pngBytes(t, bands(300, 300))).RequireStatus(http.StatusOK)
		return res
	}
}

// GAV-02, ACL-01..04 (auth matrix, design): the avatar routes of a group over the roles of the organization.
// Whoever manages the group - its creator and the admins of its organization - changes the photo; the people in
// the room read it and cannot change it (403); everybody else - a manager above the creator who is not in the
// room, a peer, an inviter, an outsider, an admin of the instance - finds nothing (404).
func TestRoomAvatarRoutesAuthMatrix(t *testing.T) {
	env := testkit.New(t)
	roles := env.OrgRoles("acme")
	runRoleRoutes(t, roles, roomAvatarSetup(env, roles), []roleRoute{
		{"get avatar", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return getRoomAvatar(c, res["room"])
		}, groupWant(200, 200, 200, 404)},
		{"put avatar", func(t *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return putRoomAvatar(c, res["room"], pngBytes(t, bands(200, 200)))
		}, groupWant(200, 200, 403, 404)},
		{"delete avatar", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return deleteRoomAvatar(c, res["room"])
		}, groupWant(204, 204, 403, 404)},
	})
}

// GAV-02: who may not change the photo is told so before the body is looked at, so a refusal never depends on
// the image, and changes nothing.
func TestRoomAvatarRefusalsChangeNothing(t *testing.T) {
	env := testkit.New(t)
	roles := env.OrgRoles("acme")
	res := roomAvatarSetup(env, roles)(t)
	before := getRoomAvatar(roles.Member, res["room"]).RequireStatus(http.StatusOK).Body
	other := pngBytes(t, jpegImage(t))

	for _, tc := range []struct {
		role   string
		c      *testkit.Client
		status int
	}{
		{"a member of the room", roles.Target, http.StatusForbidden},
		{"a guest in the room", roles.Guest, http.StatusForbidden},
		{"a colleague outside the room", roles.Peer, http.StatusNotFound},
		{"an outsider", roles.Outsider, http.StatusNotFound},
		{"the admin of the instance", roles.InstanceAdmin, http.StatusNotFound},
	} {
		code := dto.ErrorCodeForbidden
		if tc.status == http.StatusNotFound {
			code = dto.ErrorCodeNotFound
		}
		for _, resp := range []*testkit.Response{
			putRoomAvatar(tc.c, res["room"], other),
			putRoomAvatar(tc.c, res["room"], []byte("not an image at all")),
			deleteRoomAvatar(tc.c, res["room"]),
		} {
			resp.RequireError(tc.status, code)
		}
	}
	require.Equal(t, before, getRoomAvatar(roles.Member, res["room"]).RequireStatus(http.StatusOK).Body, "the photo is the creator's")

	env.Anon().DoRaw(http.MethodPut, roomAvatarPath(res["room"]), "image/png", bytes.NewReader(other)).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	env.Anon().Delete(roomAvatarPath(res["room"])).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	require.Equal(t, before, getRoomAvatar(roles.Member, res["room"]).RequireStatus(http.StatusOK).Body)

	// An AI reaches the routes of agents only: its token is no way to the photo of a group.
	token := roles.Manager.Token(dto.TokenKindAgent, "tools").AsAgent("acme-ai")
	putRoomAvatar(token, res["room"], other).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	require.Equal(t, before, getRoomAvatar(roles.Member, res["room"]).RequireStatus(http.StatusOK).Body)
}

// GAV-02, DM-02: a DM has no avatar of its own - it shows the other party. Nobody manages a DM, so the routes
// answer like the other routes only a manager may use: 403 to the people who see it (its participant and the
// managers of its AI), 404 to everybody else; and there is never a photo to read.
func TestDMHasNoAvatar(t *testing.T) {
	env := testkit.New(t)
	roles := env.OrgRoles("acme")
	runRoleRoutes(t, roles, dmRoomSetup(env, roles), []roleRoute{
		{"get avatar", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return getRoomAvatar(c, res["room"])
		}, everyRole(http.StatusNotFound)},
		{"put avatar", func(t *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return putRoomAvatar(c, res["room"], pngBytes(t, bands(200, 200)))
		}, dmWant(403, 403, 404)},
		{"delete avatar", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return deleteRoomAvatar(c, res["room"])
		}, dmWant(403, 403, 404)},
	})
	res := dmRoomSetup(env, roles)(t)
	require.Empty(t, roomOf(t, roles.Target, res["room"]).AvatarURL)
}

// GAV-03: a file that is not PNG, JPEG or WebP, or cannot be decoded, is 422 avatar_invalid and the old photo
// keeps being served.
func TestInvalidRoomAvatarKeepsTheOldOne(t *testing.T) {
	env := testkit.New(t, testkit.WithEnv(map[string]string{"PARTYLINE_MAX_UPLOAD_MB": "3"}))
	alice := env.NewUser("alice")
	room := createRoom(alice, "ops")
	good := pngBytes(t, bands(300, 300))
	photo := testkit.Decode[dto.Room](putRoomAvatar(alice, room.ID, good).RequireStatus(http.StatusOK)).AvatarURL
	before := getRoomAvatar(alice, room.ID).RequireStatus(http.StatusOK).Body

	var gifBuf bytes.Buffer
	require.NoError(t, gif.Encode(&gifBuf, bands(40, 40), nil))
	truncated := pngBytes(t, bands(300, 300))
	truncated = truncated[:len(truncated)/2]
	for name, body := range map[string][]byte{
		"gif":                     gifBuf.Bytes(),
		"svg":                     []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><script>alert(1)</script></svg>`),
		"text":                    []byte("not an image at all"),
		"empty":                   {},
		"truncated png":           truncated,
		"5000 x 10 pixels":        pngBytes(t, bands(5000, 10)),
		"polyglot html after PNG": append(append([]byte{}, good[:8]...), []byte("<script>alert(1)</script>")...),
	} {
		e := putRoomAvatar(alice, room.ID, body).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeAvatarInvalid)
		require.NotEmpty(t, e.Message, name)
		require.Equal(t, before, getRoomAvatar(alice, room.ID).RequireStatus(http.StatusOK).Body, "%s: the old photo is still served", name)
		require.Equal(t, photo, roomOf(t, alice, room.ID).AvatarURL, name)
	}
}

// GAV-03: an oversized photo is refused as 413 avatar_invalid with a message, before the body is read, and the
// old photo stays.
func TestOversizedRoomAvatarIsRefusedWith413(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	room := createRoom(alice, "ops")
	good := pngBytes(t, bands(300, 300))
	photo := testkit.Decode[dto.Room](putRoomAvatar(alice, room.ID, good).RequireStatus(http.StatusOK)).AvatarURL
	before := getRoomAvatar(alice, room.ID).RequireStatus(http.StatusOK).Body

	for name, body := range map[string][]byte{
		"2 MiB + 1 byte": append(append([]byte{}, good...), bytes.Repeat([]byte{0}, 2<<20+1-len(good))...),
		"5 MB":           append(append([]byte{}, good...), bytes.Repeat([]byte{0}, 5<<20-len(good))...),
	} {
		e := putRoomAvatar(alice, room.ID, body).RequireError(http.StatusRequestEntityTooLarge, dto.ErrorCodeAvatarInvalid)
		require.NotEmpty(t, e.Message, name)
		require.Equal(t, before, getRoomAvatar(alice, room.ID).RequireStatus(http.StatusOK).Body, "%s: the old photo is still served", name)
		require.Equal(t, photo, roomOf(t, alice, room.ID).AvatarURL, name)
	}
}

// A photo of exactly 2 MiB is fine (GAV-03 refuses only larger ones).
func TestRoomAvatarOfTwoMiBIsAccepted(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	room := createRoom(alice, "ops")
	// A PNG padded with trailing bytes to exactly 2 MiB still decodes.
	body := pngBytes(t, bands(64, 64))
	body = append(body, bytes.Repeat([]byte{0}, 2<<20-len(body))...)
	require.Len(t, body, 2<<20)
	putRoomAvatar(alice, room.ID, body).RequireStatus(http.StatusOK)
}

// GAV-01: removing the photo stops serving it and clears the URL; removing it again is fine.
func TestRoomAvatarRemoval(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	room := createRoom(alice, "ops")
	putRoomAvatar(alice, room.ID, pngBytes(t, bands(300, 300))).RequireStatus(http.StatusOK)
	require.NotEmpty(t, roomOf(t, alice, room.ID).AvatarURL)

	deleteRoomAvatar(alice, room.ID).RequireStatus(http.StatusNoContent)
	getRoomAvatar(alice, room.ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	require.Empty(t, roomOf(t, alice, room.ID).AvatarURL)
	deleteRoomAvatar(alice, room.ID).RequireStatus(http.StatusNoContent)
	deleteRoomAvatar(alice, "no-such-room").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)

	// A new photo after a removal works, and a renamed or edited group keeps its photo.
	photo := testkit.Decode[dto.Room](putRoomAvatar(alice, room.ID, pngBytes(t, bands(300, 300))).RequireStatus(http.StatusOK)).AvatarURL
	renamed := testkit.Decode[dto.Room](alice.Patch("/api/v1/rooms/"+room.ID, dto.UpdateRoomRequest{Name: ptr("ops-2")}).RequireStatus(http.StatusOK))
	require.Equal(t, photo, renamed.AvatarURL, "saving the settings does not touch the photo")
}

// GAV-04, LIVE-02: a change of the photo is a room.changed for the people who can see the group, with the new
// room; the same image again changes nothing and announces nothing; nobody else hears of it.
func TestRoomAvatarChangeIsAnnouncedLive(t *testing.T) {
	env := testkit.New(t)
	alice, bob, carol := env.NewUser("alice"), env.NewUser("bob"), env.NewUser("carol")
	room := createRoom(alice, "ops")
	addToRoom(t, env, alice, room.ID, bob) // before they listen: joining is announced to an open socket
	aliceLive, bobLive, carolLive := dialLive(t, alice), dialLive(t, bob), dialLive(t, carol)
	photo := pngBytes(t, bands(300, 300))

	put := testkit.Decode[dto.Room](putRoomAvatar(alice, room.ID, photo).RequireStatus(http.StatusOK))
	require.NotEmpty(t, put.AvatarURL)
	for name, c := range map[string]*testkit.LiveConn{"alice": aliceLive, "bob": bobLive} {
		var got dto.Room
		c.Expect(dto.LiveEventTypeRoomChanged, &got, liveWait)
		require.Equal(t, room.ID, got.ID, name)
		require.Equal(t, put.AvatarURL, got.AvatarURL, name)
	}

	putRoomAvatar(alice, room.ID, photo).RequireStatus(http.StatusOK) // nothing changed
	aliceLive.ExpectNone(quiet)
	bobLive.ExpectNone(quiet)

	deleteRoomAvatar(alice, room.ID).RequireStatus(http.StatusNoContent)
	for name, c := range map[string]*testkit.LiveConn{"alice": aliceLive, "bob": bobLive} {
		var got dto.Room
		c.Expect(dto.LiveEventTypeRoomChanged, &got, liveWait)
		require.Equal(t, room.ID, got.ID, name)
		require.Empty(t, got.AvatarURL, name)
	}
	deleteRoomAvatar(alice, room.ID).RequireStatus(http.StatusNoContent) // nothing to remove
	aliceLive.ExpectNone(quiet)

	carolLive.ExpectNone(quiet) // she cannot see the group
}

// GAV-05: the blob of a replaced photo is swept by the retention job after the grace period; the current photo's
// blob is kept however old, and so is the blob of a photo that was just replaced until the grace period passes.
// The blob of a removed photo is swept too.
func TestReplacedRoomAvatarBlobIsSwept(t *testing.T) {
	clock := testkit.NewClock(time.Now())
	env := testkit.New(t, testkit.WithClock(clock.Now))
	alice := env.NewUser("alice")
	room := createRoom(alice, "ops")
	ctx := t.Context()

	sha := func(body []byte) string {
		put := testkit.Decode[dto.Room](putRoomAvatar(alice, room.ID, body).RequireStatus(http.StatusOK))
		return roomAvatarURLPattern.FindStringSubmatch(put.AvatarURL)[2]
	}
	exists := func(sha string) bool {
		f, err := env.App.Blobs.Open(sha)
		if err == nil {
			_ = f.Close()
			return true
		}
		require.ErrorIs(t, err, attachments.ErrNotFound)
		return false
	}

	first := sha(pngBytes(t, bands(300, 300)))
	second := sha(pngBytes(t, jpegImage(t)))
	require.NotEqual(t, first, second)
	_, err := env.App.Rooms.Cleanup(ctx)
	require.NoError(t, err)
	require.True(t, exists(first) && exists(second), "inside the grace period both blobs stay")

	clock.Advance(2 * time.Hour)
	_, err = env.App.Rooms.Cleanup(ctx)
	require.NoError(t, err)
	require.False(t, exists(first), "the replaced photo is swept")
	require.True(t, exists(second), "the current photo is kept")
	getRoomAvatar(alice, room.ID).RequireStatus(http.StatusOK)

	deleteRoomAvatar(alice, room.ID).RequireStatus(http.StatusNoContent)
	clock.Advance(2 * time.Hour)
	_, err = env.App.Rooms.Cleanup(ctx)
	require.NoError(t, err)
	require.False(t, exists(second), "a removed photo is swept too")
}

// GAV-05, ROOM-02: deleting a group releases its photo with its files, unless another group has the same image
// (one image is one blob); the last group to go takes the blob with it.
func TestDeletingAGroupReleasesItsAvatar(t *testing.T) {
	clock := testkit.NewClock(time.Now())
	env := testkit.New(t, testkit.WithClock(clock.Now))
	alice := env.NewUser("alice")
	one, two := createRoom(alice, "one"), createRoom(alice, "two")
	same := pngBytes(t, bands(300, 300))
	urlOf := func(room dto.Room) string {
		return testkit.Decode[dto.Room](putRoomAvatar(alice, room.ID, same).RequireStatus(http.StatusOK)).AvatarURL
	}
	sha := roomAvatarURLPattern.FindStringSubmatch(urlOf(one))[2]
	require.Equal(t, sha, roomAvatarURLPattern.FindStringSubmatch(urlOf(two))[2], "the same image is the same blob")
	exists := func() bool {
		f, err := env.App.Blobs.Open(sha)
		if err == nil {
			_ = f.Close()
			return true
		}
		require.ErrorIs(t, err, attachments.ErrNotFound)
		return false
	}

	clock.Advance(time.Hour) // the blob is older than the minute a delete leaves alone
	alice.Delete("/api/v1/rooms/" + one.ID).RequireStatus(http.StatusNoContent)
	require.True(t, exists(), "the other group still uses the blob")
	getRoomAvatar(alice, two.ID).RequireStatus(http.StatusOK)

	alice.Delete("/api/v1/rooms/" + two.ID).RequireStatus(http.StatusNoContent)
	require.False(t, exists(), "the last group that used it took it along")
}
