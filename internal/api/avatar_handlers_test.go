package api_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/attachments"
	"github.com/wnexous/partyline/internal/testkit"
)

// bands is a w x h image: the outer quarters red (left) and blue (right), the middle half
// green, so a centre crop of a wide image is all green.
func bands(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			switch {
			case x < w/4:
				img.Set(x, y, color.RGBA{R: 220, G: 20, B: 20, A: 255})
			case x < 3*w/4:
				img.Set(x, y, color.RGBA{R: 20, G: 200, B: 20, A: 255})
			default:
				img.Set(x, y, color.RGBA{R: 20, G: 20, B: 220, A: 255})
			}
		}
	}
	return img
}

func pngBytes(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func jpegBytes(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, jpeg.Encode(&buf, img, nil))
	return buf.Bytes()
}

func putAvatar(c *testkit.Client, id string, body []byte) *testkit.Response {
	c.T.Helper()
	return c.DoRaw(http.MethodPut, "/api/v1/intelligences/"+id+"/avatar", "image/png", bytes.NewReader(body))
}

func getAvatar(c *testkit.Client, id string) *testkit.Response {
	c.T.Helper()
	return c.Get("/api/v1/intelligences/" + id + "/avatar")
}

var avatarURLPattern = regexp.MustCompile(`^/api/v1/intelligences/([0-9a-f-]+)/avatar\?v=([0-9a-f]{64})$`)

// requireGreenSquare asserts a served avatar: a 256x256 PNG whose pixels are the green centre
// of bands (a wide image keeps its middle square).
func requireGreenSquare(t *testing.T, body []byte) {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(body))
	require.NoError(t, err)
	require.Equal(t, image.Rect(0, 0, 256, 256), img.Bounds())
	for _, p := range []image.Point{{4, 4}, {251, 4}, {4, 251}, {251, 251}, {128, 128}} {
		r, g, b, _ := img.At(p.X, p.Y).RGBA()
		require.Greater(t, g>>8, uint32(150), "green at %v", p)
		require.Less(t, r>>8, uint32(60), "no red at %v", p)
		require.Less(t, b>>8, uint32(60), "no blue at %v", p)
	}
}

// PROF-07: an accepted avatar is stored centre-cropped and resized to a 256x256 PNG, and
// served to the users who can see the intelligence with the headers of the design; the URL
// in the profile carries the content hash as its cache key. PNG, JPEG and WebP are accepted.
func TestAvatarIsStoredAsASquarePNGAndServed(t *testing.T) {
	env := testkit.New(t)
	alice, bob := env.NewUser("alice"), env.NewUser("bob")
	getAvatar(alice, alice.User.ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)

	webp, err := os.ReadFile("../avatars/testdata/tiny.webp")
	require.NoError(t, err)
	for name, body := range map[string][]byte{
		"png": pngBytes(t, bands(400, 200)), "jpeg": jpegBytes(t, bands(400, 200)), "webp": webp,
	} {
		prof := testkit.Decode[dto.IntelligenceProfile](putAvatar(alice, alice.User.ID, body).RequireStatus(http.StatusOK))
		m := avatarURLPattern.FindStringSubmatch(prof.AvatarURL)
		require.NotNil(t, m, "%s: avatar_url %q", name, prof.AvatarURL)
		require.Equal(t, alice.User.ID, m[1])
		require.Equal(t, prof, profileOf(t, alice, alice.User.ID), "%s: the PUT answers with the stored profile", name)

		for _, viewer := range []*testkit.Client{alice, bob} {
			resp := getAvatar(viewer, alice.User.ID).RequireStatus(http.StatusOK)
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
		require.Equal(t, etag, getAvatar(alice, alice.User.ID).Header.Get("ETag"), name)
		alice.WithHeader("If-None-Match", etag).Get("/api/v1/intelligences/" + alice.User.ID + "/avatar").
			RequireStatus(http.StatusNotModified) // the browser revalidates for free
	}
	seen := profileOf(t, bob, alice.User.ID)
	require.Regexp(t, avatarURLPattern, seen.AvatarURL, "other users see the avatar in the profile")
	env.Anon().Get("/api/v1/intelligences/"+alice.User.ID+"/avatar").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
}

// PROF-07: an AI's avatar is served to its owner, its grantees and users who share a viewable
// conversation with it, and is 404 for everybody else (not 403).
func TestAIAvatarIsServedToWhoCanSeeTheAI(t *testing.T) {
	env := testkit.New(t)
	alice, bob, carol, dave := env.NewUser("alice"), env.NewUser("bob"), env.NewUser("carol"), env.NewUser("dave")
	id := registerAgent(t, env, alice, "builder")
	shareAgent(alice, id, bob.User.ID)
	putAvatar(alice, id, pngBytes(t, bands(300, 300))).RequireStatus(http.StatusOK)

	getAvatar(alice, id).RequireStatus(http.StatusOK)
	getAvatar(bob, id).RequireStatus(http.StatusOK)
	getAvatar(carol, id).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	getAvatar(dave, id).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)

	room := createRoom(alice, "ops")
	alice.Post("/api/v1/rooms/"+room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: id}).RequireStatus(http.StatusCreated)
	shareRoom(alice, room.ID, dto.SharingUser{UserID: carol.User.ID, Level: dto.LevelView})
	getAvatar(carol, id).RequireStatus(http.StatusOK)
	getAvatar(dave, id).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)

	// Seeing the AI through a room shared with it is no access to the AI itself.
	seen := profileOf(t, carol, id)
	require.Equal(t, []any{false, dto.Level("")}, []any{seen.CanEdit, seen.AI.MyLevel})
	patchProfile(carol, id, dto.UpdateProfileRequest{Bio: ptr("hijack")}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
}

// PROF-06: a file that is not PNG, JPEG or WebP, is over 2 MB or cannot be decoded is 422
// avatar_invalid and the old avatar keeps being served; a body over the instance upload limit
// is 413.
func TestInvalidAvatarKeepsTheOldOne(t *testing.T) {
	env := testkit.New(t, testkit.WithEnv(map[string]string{"PARTYLINE_MAX_UPLOAD_MB": "3"}))
	alice := env.NewUser("alice")
	good := pngBytes(t, bands(300, 300))
	prof := testkit.Decode[dto.IntelligenceProfile](putAvatar(alice, alice.User.ID, good).RequireStatus(http.StatusOK))
	before := getAvatar(alice, alice.User.ID).RequireStatus(http.StatusOK).Body

	var gifBuf bytes.Buffer
	require.NoError(t, gif.Encode(&gifBuf, bands(40, 40), nil))
	truncated := pngBytes(t, bands(300, 300))
	truncated = truncated[:len(truncated)/2]
	for name, body := range map[string][]byte{
		"gif":                      gifBuf.Bytes(),
		"text":                     []byte("not an image at all"),
		"empty":                    {},
		"truncated png":            truncated,
		"2 MiB + 1 byte":           append(good, bytes.Repeat([]byte{0}, 2<<20+1-len(good))...),
		"3 MiB (the upload limit)": bytes.Repeat([]byte{0}, 3<<20),
		"5000 x 10 pixels":         pngBytes(t, bands(5000, 10)),
		"polyglot html after PNG":  append(append([]byte{}, good[:8]...), []byte("<script>alert(1)</script>")...),
	} {
		e := putAvatar(alice, alice.User.ID, body).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeAvatarInvalid)
		require.NotEmpty(t, e.Message, name)
		require.Equal(t, before, getAvatar(alice, alice.User.ID).RequireStatus(http.StatusOK).Body, "%s: the old avatar is still served", name)
		require.Equal(t, prof.AvatarURL, profileOf(t, alice, alice.User.ID).AvatarURL, name)
	}

	huge := bytes.Repeat([]byte{0}, 3<<20+1)
	putAvatar(alice, alice.User.ID, huge).RequireError(http.StatusRequestEntityTooLarge, dto.ErrorCodeTooLarge)
	require.Equal(t, before, getAvatar(alice, alice.User.ID).RequireStatus(http.StatusOK).Body)
}

// An accepted upload of exactly 2 MiB is fine (PROF-06 refuses only larger files).
func TestAvatarOfTwoMiBIsAccepted(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	// A PNG padded with trailing bytes to exactly 2 MiB still decodes.
	body := pngBytes(t, bands(64, 64))
	body = append(body, bytes.Repeat([]byte{0}, 2<<20-len(body))...)
	require.Len(t, body, 2<<20)
	putAvatar(alice, alice.User.ID, body).RequireStatus(http.StatusOK)
}

// Removing an avatar stops serving it and clears the URL; removing it again is fine.
func TestAvatarRemoval(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	putAvatar(alice, alice.User.ID, pngBytes(t, bands(300, 300))).RequireStatus(http.StatusOK)
	alice.Delete("/api/v1/intelligences/" + alice.User.ID + "/avatar").RequireStatus(http.StatusNoContent)
	getAvatar(alice, alice.User.ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	require.Empty(t, profileOf(t, alice, alice.User.ID).AvatarURL)
	alice.Delete("/api/v1/intelligences/" + alice.User.ID + "/avatar").RequireStatus(http.StatusNoContent)
}

// Authorization matrix of the avatar routes of an AI owned by the owner and shared (use) with
// the viewer: only the owner changes it, readers are the users who can see the AI.
func TestAvatarRoutesAuthMatrix(t *testing.T) {
	m := newMatrix(t)
	setup := func(t *testing.T, m *matrix) matrixResource {
		owner := m.as(t, roleOwner)
		id := registerAgent(t, m.env, owner, "builder")
		shareAgent(owner, id, m.clients[roleViewer].User.ID)
		putAvatar(owner, id, pngBytes(t, bands(300, 300))).RequireStatus(http.StatusOK)
		return matrixResource{"ai": id}
	}
	routes := []matrixRoute{
		{"get avatar", func(_ *testing.T, _ *matrix, c *testkit.Client, res matrixResource) *testkit.Response {
			return getAvatar(c, res["ai"])
		}, statuses(200, 200, 404, 404, 404)},
		{"put avatar", func(t *testing.T, _ *matrix, c *testkit.Client, res matrixResource) *testkit.Response {
			return putAvatar(c, res["ai"], pngBytes(t, bands(200, 200)))
		}, statuses(200, 403, 404, 404, 404)},
		{"delete avatar", func(_ *testing.T, _ *matrix, c *testkit.Client, res matrixResource) *testkit.Response {
			return c.Delete("/api/v1/intelligences/" + res["ai"] + "/avatar")
		}, statuses(204, 403, 404, 404, 404)},
	}
	m.run(t, setup, routes)
}

// agentAs is the agent token client of owner acting as the agent named name.
func agentAs(owner *testkit.Client, name string) *testkit.Client {
	return owner.Token(dto.TokenKindAgent, "laptop-"+name).WithHeader(dto.HeaderAgent, name)
}

// PROF-04 (server side): an agent token updates its own AI's display name, handle, bio and
// avatar, and nothing else: not another AI of the same owner, not its owner's profile.
func TestAgentUpdatesItsOwnProfile(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	builder := registerAgent(t, env, alice, "builder")
	nova := registerAgent(t, env, alice, "nova")
	agent := agentAs(alice, "builder")

	got := testkit.Decode[dto.IntelligenceProfile](agent.Patch("/api/v1/agent/profile", dto.UpdateProfileRequest{
		DisplayName: ptr("Atlas"), Handle: ptr("atlas"), Bio: ptr("Runs the builds"),
	}).RequireStatus(http.StatusOK))
	require.Equal(t, []any{builder, "Atlas", "atlas", "Runs the builds", dto.IntelligenceRoleAI, true},
		[]any{got.ID, got.DisplayName, got.Handle, got.Bio, got.Role, got.CanEdit})
	require.Equal(t, got, profileOf(t, alice, builder), "the owner reads the same profile")
	require.Equal(t, "Atlas", listedAgent(t, alice, builder).DisplayName, "the agents list follows (AID-06)")

	withAvatar := testkit.Decode[dto.IntelligenceProfile](agent.DoRaw(http.MethodPut, "/api/v1/agent/profile/avatar", "image/png",
		bytes.NewReader(pngBytes(t, bands(400, 200)))).RequireStatus(http.StatusOK))
	require.Regexp(t, avatarURLPattern, withAvatar.AvatarURL)
	requireGreenSquare(t, getAvatar(alice, builder).RequireStatus(http.StatusOK).Body)

	other := profileOf(t, alice, nova)
	require.Equal(t, []any{"nova", "nova", "", ""}, []any{other.DisplayName, other.Handle, other.Bio, other.AvatarURL}, "the sibling AI is untouched")
	require.Equal(t, "alice", profileOf(t, alice, alice.User.ID).Handle)

	// Refusals use the same codes: a handle in use is 409, a bad image 422, a bad field 422.
	agent.Patch("/api/v1/agent/profile", dto.UpdateProfileRequest{Handle: ptr("nova")}).RequireError(http.StatusConflict, dto.ErrorCodeHandleTaken)
	agent.Patch("/api/v1/agent/profile", dto.UpdateProfileRequest{DisplayName: ptr(strings.Repeat("x", 65))}).
		RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	agent.DoRaw(http.MethodPut, "/api/v1/agent/profile/avatar", "image/png", strings.NewReader("not an image")).
		RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeAvatarInvalid)
	require.Equal(t, withAvatar.AvatarURL, profileOf(t, alice, builder).AvatarURL, "the refused image changed nothing")
}

// The agent profile routes are for agent tokens naming a registered agent of the token's
// owner: sessions and user tokens get 403, an unknown agent 403, anonymous 401.
func TestAgentProfileRoutesRequireAnAgentToken(t *testing.T) {
	env := testkit.New(t)
	alice, bob := env.NewUser("alice"), env.NewUser("bob")
	registerAgent(t, env, alice, "builder")
	body := dto.UpdateProfileRequest{Bio: ptr("hijack")}
	png := pngBytes(t, bands(100, 100))

	for name, c := range map[string]*testkit.Client{
		"session":    alice.WithHeader(dto.HeaderAgent, "builder"),
		"user token": alice.Token(dto.TokenKindUser, "script").WithHeader(dto.HeaderAgent, "builder"),
	} {
		c.Patch("/api/v1/agent/profile", body).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
		c.DoRaw(http.MethodPut, "/api/v1/agent/profile/avatar", "image/png", bytes.NewReader(png)).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
		_ = name
	}
	bobsAgent := agentAs(bob, "builder") // bob has no agent of that name: it is not alice's
	bobsAgent.Patch("/api/v1/agent/profile", body).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	agentAs(alice, "ghost").Patch("/api/v1/agent/profile", body).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	env.Anon().Patch("/api/v1/agent/profile", body).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	env.Anon().DoRaw(http.MethodPut, "/api/v1/agent/profile/avatar", "image/png", bytes.NewReader(png)).
		RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	require.Equal(t, "", profileOf(t, alice, registerAgent(t, env, alice, "builder")).Bio)
}

// A replaced avatar's blob is swept by the retention job after the grace period; the current
// avatar's blob is kept however old, and so is the blob of an avatar that was just replaced
// until the grace period passes.
func TestReplacedAvatarBlobIsSwept(t *testing.T) {
	clock := testkit.NewClock(time.Now())
	env := testkit.New(t, testkit.WithClock(clock.Now))
	alice := env.NewUser("alice")
	ctx := t.Context()

	sha := func(body []byte) string {
		prof := testkit.Decode[dto.IntelligenceProfile](putAvatar(alice, alice.User.ID, body).RequireStatus(http.StatusOK))
		return avatarURLPattern.FindStringSubmatch(prof.AvatarURL)[2]
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
	require.False(t, exists(first), "the replaced avatar is swept")
	require.True(t, exists(second), "the current avatar is kept")
	getAvatar(alice, alice.User.ID).RequireStatus(http.StatusOK)

	alice.Delete("/api/v1/intelligences/" + alice.User.ID + "/avatar").RequireStatus(http.StatusNoContent)
	clock.Advance(2 * time.Hour)
	_, err = env.App.Rooms.Cleanup(ctx)
	require.NoError(t, err)
	require.False(t, exists(second), "a removed avatar is swept too")
}

// jpegImage is an image different from bands, so its avatar has another hash.
func jpegImage(t *testing.T) image.Image {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 120, 120))
	for y := 0; y < 120; y++ {
		for x := 0; x < 120; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 2), G: uint8(y * 2), B: 128, A: 255})
		}
	}
	return img
}
