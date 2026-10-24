package api

import (
	"errors"
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
	"github.com/wnexous/partyline/internal/conversations"
	"github.com/wnexous/partyline/internal/delivery"
	"github.com/wnexous/partyline/internal/devices"
	"github.com/wnexous/partyline/internal/identities"
	"github.com/wnexous/partyline/internal/intelligences"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/platform"
	"github.com/wnexous/partyline/internal/rooms"
	"github.com/wnexous/partyline/internal/users"
	"github.com/wnexous/partyline/internal/validation"
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
	// Identities manages pairing codes and verified chat identities.
	Identities *identities.Service
	// Devices approves new machines (partyline setup --browser).
	Devices *devices.Service
	Blobs   *attachments.Store
	Audit   *audit.Log
	// Platform is the delivery queue of the messages that arrive on connections: GET
	// /agent/pending?platform=1 takes from it and the agent attachments route serves its files.
	Platform *platform.Queue
	// Sends sends what an AI answers through a connection it operates: the reply to a message
	// of the queue (CID-15).
	Sends *connectors.Sends
	// Live serves the live WebSocket at GET /api/v1/live.
	Live http.Handler
	// AgentWS serves the agent WebSocket at GET /api/v1/agent/ws.
	AgentWS http.Handler
	// Inbox serves GET /api/v1/agent/pending.
	Inbox AgentInbox
	// Trust opens the trust of each request that tells an AI what it may trust: the history tool and the
	// room's instructions (POS-10). Without it nothing is trusted.
	Trust delivery.TrustSource
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
	s.deviceRoutes(mux)
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
	switch {
	case errors.As(err, &verr):
		return httpx.Invalid(verr.Fields)
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
	case errors.Is(err, access.ErrDMNoSharing):
		return httpx.NewError(http.StatusUnprocessableEntity, dto.ErrorCodeDMNoSharing, "a direct message cannot be shared")
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
	}
	return err
}

// healthz answers 200 with the server version.
func (s *server) healthz(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": s.Version})
}

func (s *server) readyz(w http.ResponseWriter, _ *http.Request) {
	if s.Ready == nil || !s.Ready() {
		httpx.WriteError(w, httpx.NewError(http.StatusServiceUnavailable, dto.ErrorCodeUnavailable, "starting"))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": s.Version})
}
