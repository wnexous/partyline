package api

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/attachments"
	"github.com/wnexous/partyline/internal/audit"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/avatars"
	"github.com/wnexous/partyline/internal/config"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/containeragents"
	"github.com/wnexous/partyline/internal/conversations"
	"github.com/wnexous/partyline/internal/delivery"
	"github.com/wnexous/partyline/internal/devices"
	"github.com/wnexous/partyline/internal/identities"
	"github.com/wnexous/partyline/internal/intelligences"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/ownersecrets"
	"github.com/wnexous/partyline/internal/platform"
	"github.com/wnexous/partyline/internal/rooms"
	"github.com/wnexous/partyline/internal/runners"
	"github.com/wnexous/partyline/internal/runners/spec"
	"github.com/wnexous/partyline/internal/users"
	"github.com/wnexous/partyline/internal/validation"
	"github.com/wnexous/partyline/internal/webpush"
)

// Deps are the services the API serves.
type Deps struct {
	Config *config.Config
	Users  *users.Service
	Rooms  *rooms.Service
	Agents *agents.Registry
	// Intelligences serves profiles of users, AIs and people.
	Intelligences *intelligences.Service
	// Conversations opens direct messages and creates groups.
	Conversations *conversations.Service
	// Orgs is the organization use cases: organizations, their chart, members, positions and
	// invitations.
	Orgs *orgs.Service
	// Connectors manages connectors and their chats.
	Connectors *connectors.Service
	// Hooks finds the running connector of POST /api/v1/hooks/{id}; FileLinks resolves the
	// signed attachment links of GET /api/v1/hooks/files/{token}.
	Hooks     HookSource
	FileLinks *connectors.FileLinks
	// Crashes counts the connectors that keep crashing: /readyz reports the server degraded while
	// there are some, and stays 200 (CON-15).
	Crashes CrashSource
	// Identities manages pairing codes and verified chat identities.
	Identities *identities.Service
	// Devices approves new machines (partyline setup --browser).
	Devices *devices.Service
	Blobs   *attachments.Store
	Audit   *audit.Log
	// Secrets stores the owner's named credentials for container AIs (CA-03).
	Secrets *ownersecrets.Service
	// Platform is the delivery queue of the messages that arrive on connections: the agent WebSocket
	// reads it for a session that opted in, and the agent attachments route serves its files.
	Platform *platform.Queue
	// Sends sends what an AI answers through a connection it operates: the reply to a message
	// of the queue (CID-15).
	Sends *connectors.Sends
	// Live serves the live WebSocket at GET /api/v1/live.
	Live http.Handler
	// AgentWS serves the agent WebSocket at GET /api/v1/agent/ws.
	AgentWS http.Handler
	// Runners is the registry of the servers that run container AIs (CA-01).
	Runners *runners.Service
	// Containers creates and operates the AIs that live in a container on a runner (CA-04, CA-06).
	Containers *containeragents.Service
	// RunnerWS serves the runner WebSocket at GET /api/v1/runner/ws (CA-02).
	RunnerWS http.Handler
	// Trust opens the trust of each request that tells an AI what it may trust: the history tool and the
	// room's instructions (POS-10). Without it nothing is trusted.
	Trust delivery.TrustSource
	// Push serves the Web Push routes: the key, the subscriptions and the test notification
	// (PUSH-02, PUSH-03, PUSH-04, PUSH-05, PUSH-15).
	Push *webpush.Service
	// WebUI serves the web app for every other GET path (SPA fallback).
	WebUI  http.Handler
	Logger *slog.Logger
	// Ready reports whether the app finished starting (migrations applied).
	Ready func() bool
	// Version is the server build version, reported by /healthz (partyline doctor, ONB-06).
	Version string
}

// server holds the handler dependencies.
type server struct {
	Deps
}

// New returns the full HTTP handler: routes wrapped in the middleware chain.
func New(d Deps) http.Handler {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	s := &server{Deps: d}
	mux := http.NewServeMux()
	s.routes(mux)
	return Middleware(MiddlewareConfig{
		PublicURL:      d.Config.PublicURL,
		TrustedProxies: d.Config.TrustedProxies,
		MaxUploadBytes: d.Config.MaxUploadBytes,
		Auth:           d.Users,
		Logger:         d.Logger,
	}, mux)
}

func (s *server) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /readyz", s.readyz)
	mux.HandleFunc("GET /install.sh", s.installScript)
	s.authRoutes(mux)
	s.adminRoutes(mux)
	s.orgRoutes(mux)
	s.positionRoutes(mux)
	s.orgMemberRoutes(mux)
	s.orgInvitationRoutes(mux)
	s.orgChartRoutes(mux)
	s.roomRoutes(mux)
	s.reactionRoutes(mux)
	s.conversationRoutes(mux)
	s.intelligenceRoutes(mux)
	s.contactRoutes(mux)
	s.avatarRoutes(mux)
	s.agentRoutes(mux)
	s.identityDeleteRoutes(mux)
	s.agentAPIRoutes(mux)
	s.agentConversationRoutes(mux)
	s.connectorRoutes(mux)
	s.identityRoutes(mux)
	s.chatRoutes(mux)
	s.hookRoutes(mux)
	s.pushRoutes(mux)
	s.deviceRoutes(mux)
	s.secretRoutes(mux)
	s.runnerRoutes(mux)
	s.containerAgentRoutes(mux)
	if s.Live != nil {
		mux.Handle("GET /api/v1/live", s.Live)
	}
	if s.AgentWS != nil {
		mux.Handle("GET /api/v1/agent/ws", s.AgentWS)
	}
	// Unknown API paths get the JSON envelope, never the web app.
	s.handle(mux, "/api/", func(http.ResponseWriter, *http.Request) error {
		return httpx.NewError(http.StatusNotFound, dto.ErrorCodeNotFound, "not found")
	})
	if s.WebUI != nil {
		mux.Handle("/", s.WebUI) // answers 405 to methods other than GET and HEAD
	}
}

// apiFunc is a handler that returns its error; handle renders it as the error envelope.
type apiFunc func(w http.ResponseWriter, r *http.Request) error

// principalFunc is a handler for authenticated requests.
type principalFunc func(w http.ResponseWriter, r *http.Request, p authn.Principal) error

var errNotLoggedIn = httpx.NewError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized, "authentication required")

// handle registers h on mux, rendering returned errors with mapError.
func (s *server) handle(mux *http.ServeMux, pattern string, h apiFunc) {
	mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			s.writeError(w, r, err)
		}
	})
}

// authed requires a user principal (session or user token); anonymous requests get 401.
func authed(h principalFunc) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		p, ok := authn.PrincipalFrom(r.Context())
		if !ok {
			return errNotLoggedIn
		}
		return h(w, r, p)
	}
}

// userOnly requires a user principal: a session or a user token. An agent token never reaches
// here (the middleware restricts it to the agent API) and any other credential kind, such as a
// runner token, is refused.
func userOnly(h principalFunc) apiFunc {
	return authed(func(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
		if p.Kind != authn.KindUser {
			return httpx.NewError(http.StatusForbidden, dto.ErrorCodeForbidden, "user credentials required")
		}
		return h(w, r, p)
	})
}

// adminOnly requires an admin user principal; other users get 403.
func adminOnly(h principalFunc) apiFunc {
	return authed(func(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
		if p.Role != string(dto.RoleAdmin) {
			return httpx.NewError(http.StatusForbidden, dto.ErrorCodeForbidden, "admin role required")
		}
		return h(w, r, p)
	})
}

// writeError maps service errors to API errors; unknown errors are logged and become 500.
func (s *server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	mapped := mapError(err)
	var he *httpx.Error
	if !errors.As(mapped, &he) {
		s.Logger.ErrorContext(r.Context(), "request failed", "err", err,
			"request_id", httpx.RequestID(r.Context()), "method", r.Method, "path", r.URL.Path)
	}
	httpx.WriteError(w, mapped)
}

// mapError translates service errors into API errors with their status code.
func mapError(err error) error {
	var verr *validation.Error
	var refused *conversations.MembersRefusedError
	var notEmpty *orgs.NotEmptyError
	var specErr *spec.ValidationError
	var opErr *runners.OpError
	switch {
	case errors.As(err, &verr):
		return httpx.Invalid(verr.Fields)
	case errors.As(err, &specErr):
		return httpx.Invalid(specErr.Fields)
	case errors.As(err, &opErr):
		return httpx.NewError(http.StatusBadGateway, dto.ErrorCodeUnavailable, opErr.Error())
	case errors.Is(err, runners.ErrTokenRequired):
		return httpx.NewError(http.StatusForbidden, dto.ErrorCodeForbidden, "runner token required")
	case errors.Is(err, runners.ErrNotRegistered):
		return httpx.NewError(http.StatusForbidden, dto.ErrorCodeForbidden, "runner token is not registered")
	case errors.Is(err, runners.ErrNotFound):
		return httpx.NewError(http.StatusNotFound, dto.ErrorCodeNotFound, "not found")
	case errors.Is(err, runners.ErrInUse):
		return httpx.NewError(http.StatusConflict, dto.ErrorCodeRunnerInUse, "the runner still runs containers: delete them first")
	case errors.Is(err, runners.ErrOffline):
		return httpx.NewError(http.StatusConflict, dto.ErrorCodeRunnerOffline, "the runner is offline")
	case errors.Is(err, runners.ErrTimeout):
		return httpx.NewError(http.StatusGatewayTimeout, dto.ErrorCodeUnavailable, "the runner did not answer in time")
	case errors.Is(err, runners.ErrNameTaken):
		return httpx.NewError(http.StatusConflict, dto.ErrorCodeConflict, "another runner already has this name")
	case errors.Is(err, users.ErrSetupDone):
		return httpx.NewError(http.StatusConflict, dto.ErrorCodeConflict, "setup already done")
	case errors.Is(err, users.ErrBadSetupToken):
		return httpx.NewError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized, "invalid setup token")
	case errors.Is(err, users.ErrInvalidCredentials):
		return httpx.NewError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized, "invalid username or password")
	case errors.Is(err, users.ErrUnauthenticated):
		return errUnauthorized
	case errors.Is(err, users.ErrRateLimited):
		return httpx.NewError(http.StatusTooManyRequests, dto.ErrorCodeRateLimited, "too many failed logins, try again later")
	case errors.Is(err, users.ErrNotFound):
		return httpx.NewError(http.StatusNotFound, dto.ErrorCodeNotFound, "not found")
	case errors.Is(err, ownersecrets.ErrNotFound), errors.Is(err, containeragents.ErrNotFound):
		return httpx.NewError(http.StatusNotFound, dto.ErrorCodeNotFound, "not found")
	case errors.Is(err, users.ErrInviteGone):
		return httpx.NewError(http.StatusGone, dto.ErrorCodeGone, "invite already used, revoked or expired")
	case errors.Is(err, users.ErrUsernameTaken):
		return httpx.NewError(http.StatusConflict, dto.ErrorCodeConflict, "username already taken")
	case errors.Is(err, users.ErrTokenActive):
		return httpx.NewError(http.StatusConflict, dto.ErrorCodeConflict, "revoke the token before deleting it")
	case errors.Is(err, users.ErrSelfAdminChange), errors.Is(err, users.ErrLastAdmin):
		return httpx.NewError(http.StatusConflict, dto.ErrorCodeConflict, err.Error())
	case errors.Is(err, users.ErrLastInstanceAdmin):
		return httpx.NewError(http.StatusConflict, dto.ErrorCodeLastInstanceAdmin, "you are the last active admin of this instance: make another user an admin first")
	case errors.Is(err, orgs.ErrLastAdmin):
		return httpx.NewError(http.StatusConflict, dto.ErrorCodeLastAdmin, "an organization needs at least one admin: make someone else an admin first")
	case errors.Is(err, orgs.ErrAlreadyMember):
		return httpx.NewError(http.StatusConflict, dto.ErrorCodeAlreadyMember, "already a member of the organization")
	case errors.Is(err, orgs.ErrGone):
		return httpx.NewError(http.StatusGone, dto.ErrorCodeGone, "the invitation has expired or no longer stands")
	case errors.Is(err, orgs.ErrRateLimited):
		return httpx.NewError(http.StatusTooManyRequests, dto.ErrorCodeRateLimited, "too many invitations, try again later")
	case errors.Is(err, orgs.ErrContactNotFound):
		return httpx.NewError(http.StatusNotFound, dto.ErrorCodeContactNotFound, "contact not found")
	case errors.Is(err, orgs.ErrHomeOrganization):
		return httpx.NewError(http.StatusConflict, dto.ErrorCodeHomeOrganization, "an AI cannot leave its home organization: ask an admin to delete it")
	case errors.Is(err, agents.ErrNotRegistered):
		return httpx.NewError(http.StatusForbidden, dto.ErrorCodeForbidden, "agent not registered: connect the channel first")
	case errors.As(err, &notEmpty):
		he := httpx.NewError(http.StatusConflict, dto.ErrorCodeOrganizationNotEmpty, "the organization still owns AIs, connections or groups")
		he.Details = map[string]string{
			"agents":     strconv.FormatInt(notEmpty.Agents, 10),
			"connectors": strconv.FormatInt(notEmpty.Connectors, 10),
			"groups":     strconv.FormatInt(notEmpty.Groups, 10),
		}
		return he
	case errors.Is(err, orgs.ErrSlugTaken):
		return httpx.NewError(http.StatusConflict, dto.ErrorCodeConflict, "slug already taken")
	case errors.Is(err, orgs.ErrCycle):
		return httpx.NewError(http.StatusConflict, dto.ErrorCodePositionCycle, "a position cannot be moved below itself or one of its descendants")
	case errors.Is(err, orgs.ErrPositionNameTaken):
		return httpx.NewError(http.StatusConflict, dto.ErrorCodeConflict, "position name already taken")
	case errors.Is(err, access.ErrNotFound):
		return httpx.NewError(http.StatusNotFound, dto.ErrorCodeNotFound, "not found")
	case errors.Is(err, avatars.ErrInvalid):
		return httpx.NewError(http.StatusUnprocessableEntity, dto.ErrorCodeAvatarInvalid, "the avatar must be a PNG, JPEG or WebP image up to 2 MB")
	case errors.Is(err, intelligences.ErrHandleTaken):
		return httpx.NewError(http.StatusConflict, dto.ErrorCodeHandleTaken, "handle already taken")
	case errors.Is(err, access.ErrHistoryOnly):
		return httpx.NewError(http.StatusUnprocessableEntity, dto.ErrorCodeHistoryOnly, "this is history: answer people on their platform, through the connection")
	case errors.Is(err, rooms.ErrReactionLimit):
		return httpx.NewError(http.StatusUnprocessableEntity, dto.ErrorCodeReactionLimit, "a message has at most 12 different reactions")
	case errors.Is(err, rooms.ErrDMMembersFixed):
		return httpx.NewError(http.StatusUnprocessableEntity, dto.ErrorCodeDMMembersFixed, "the members of a direct message cannot be changed")
	case errors.Is(err, rooms.ErrChatsNotMembers):
		return httpx.NewError(http.StatusUnprocessableEntity, dto.ErrorCodeChatsNotMembers, "chats cannot be members of a conversation: a chat reaches the AI that operates its connection")
	case errors.Is(err, rooms.ErrCounterpartDisabled):
		return httpx.NewError(http.StatusForbidden, dto.ErrorCodeCounterpartDisabled, "the other participant of this direct message is disabled")
	case errors.Is(err, conversations.ErrDMSelf):
		return httpx.NewError(http.StatusUnprocessableEntity, dto.ErrorCodeDMSelf, "a direct message needs two different intelligences")
	case errors.Is(err, conversations.ErrContactNotFound):
		return httpx.NewError(http.StatusNotFound, dto.ErrorCodeContactNotFound, "contact not found")
	case errors.Is(err, conversations.ErrNoRouteToPerson):
		return httpx.NewError(http.StatusUnprocessableEntity, dto.ErrorCodeNoRouteToPerson, "no conversation of yours reaches this person")
	case errors.As(err, &refused):
		he := httpx.NewError(http.StatusUnprocessableEntity, dto.ErrorCodeMembersRefused, "some members cannot be added to the group")
		he.Refused = refused.Refused
		return he
	case errors.Is(err, conversations.ErrIdentityOnline):
		return httpx.NewError(http.StatusConflict, dto.ErrorCodeIdentityOnline, "the AI is online: end its session before merging it")
	case errors.Is(err, conversations.ErrRateLimited):
		return httpx.NewError(http.StatusTooManyRequests, dto.ErrorCodeRateLimited, "too many new conversations, try again later")
	case errors.Is(err, rooms.ErrMessageDeleted):
		return httpx.NewError(http.StatusConflict, dto.ErrorCodeConflict, "the message was deleted")
	case errors.Is(err, rooms.ErrAlreadyAnswered):
		return httpx.NewError(http.StatusConflict, dto.ErrorCodeAlreadyAnswered, "the question was already answered by this author")
	case errors.Is(err, rooms.ErrAlreadyMember):
		return httpx.NewError(http.StatusConflict, dto.ErrorCodeConflict, "already a member of the room")
	case errors.Is(err, access.ErrForbidden):
		return httpx.NewError(http.StatusForbidden, dto.ErrorCodeForbidden, "not allowed")
	case errors.Is(err, connectors.ErrNotRunning):
		return httpx.NewError(http.StatusConflict, dto.ErrorCodeConflict, "connector is not running")
	case errors.Is(err, connectors.ErrCannotListChats):
		return httpx.NewError(http.StatusConflict, dto.ErrorCodeConflict, "this connector kind cannot list chats")
	case errors.Is(err, connectors.ErrPlatform):
		return httpx.NewError(http.StatusBadGateway, dto.ErrorCodeUnavailable, "the platform did not answer the request")
	case errors.Is(err, connectors.ErrNotOperator):
		return httpx.NewError(http.StatusForbidden, dto.ErrorCodeNotOperator, "only the AI that operates the connection can send through it")
	case errors.Is(err, connectors.ErrUnknownChat):
		return httpx.NewError(http.StatusNotFound, dto.ErrorCodeUnknownChat, "the chat does not belong to this connection")
	case errors.Is(err, connectors.ErrReactionsUnsupported):
		return httpx.NewError(http.StatusUnprocessableEntity, dto.ErrorCodeUnsupported,
			"the platform of this connection does not take reactions: answer the message with reply")
	case errors.Is(err, connectors.ErrNoReactionTarget):
		return httpx.NewError(http.StatusUnprocessableEntity, dto.ErrorCodeUnsupported,
			"the platform gave this message no id, so it cannot get a reaction: answer it with reply")
	case errors.Is(err, connectors.ErrFilesUnsupported):
		return httpx.NewError(http.StatusUnprocessableEntity, dto.ErrorCodeUnsupported,
			"the platform of this connection cannot be sent files: write to the chat with send_message")
	case errors.Is(err, connectors.ErrNewChatRateLimited):
		return httpx.NewError(http.StatusTooManyRequests, dto.ErrorCodeRateLimited,
			fmt.Sprintf("rate limited: at most %d new chats an hour", agents.NewChatLimit))
	case errors.Is(err, webpush.ErrNoSubscriptions):
		return httpx.NewError(http.StatusConflict, dto.ErrorCodeConflict, "no push subscription for this account")
	case errors.Is(err, webpush.ErrSubscriptionNotFound):
		return httpx.NewError(http.StatusNotFound, dto.ErrorCodeNotFound, "not found")
	case errors.Is(err, webpush.ErrRateLimited):
		return httpx.NewError(http.StatusTooManyRequests, dto.ErrorCodeRateLimited, "too many test notifications, try again later")
	case errors.Is(err, webpush.ErrBusy):
		return httpx.NewError(http.StatusServiceUnavailable, dto.ErrorCodeUnavailable, "try again in a moment")
	case errors.Is(err, webpush.ErrUnavailable):
		return httpx.NewError(http.StatusServiceUnavailable, dto.ErrorCodeUnavailable, "push is unavailable on this server")
	}
	return err
}

// CrashSource counts the connectors that keep crashing; the connectors' Manager implements it.
type CrashSource interface {
	CrashLooping() int
}

// healthz is the liveness check, which the container HEALTHCHECK asks: 200 with the server version
// whenever the process answers. A connector that keeps crashing never fails it, or the platform
// would restart the whole container for one bad connector (CON-15).
func (s *server) healthz(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": s.Version})
}

// readyz is the readiness check: 503 until the migrations have run (OPS-01), then 200. While some
// connectors keep crashing it stays 200, because the rest of the server works and a failing probe
// would take all of it out of rotation, but says so: status "degraded" with their number in
// connectors_crash_looping, for a monitor that reads the body (CON-15).
func (s *server) readyz(w http.ResponseWriter, _ *http.Request) {
	if s.Ready == nil || !s.Ready() {
		httpx.WriteError(w, httpx.NewError(http.StatusServiceUnavailable, dto.ErrorCodeUnavailable, "starting"))
		return
	}
	body := map[string]any{"status": "ok", "version": s.Version}
	if s.Crashes != nil {
		if n := s.Crashes.CrashLooping(); n > 0 {
			body["status"] = "degraded"
			body["connectors_crash_looping"] = n
		}
	}
	httpx.WriteJSON(w, http.StatusOK, body)
}
