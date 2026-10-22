package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/convert"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/delivery"
	"github.com/wnexous/partyline/internal/rooms"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/validation"
)

// Agent history page sizes (TOOL-05).
const (
	agentHistoryDefault = 20
	agentHistoryMax     = 100
)

var (
	errAgentRateLimited = httpx.NewError(http.StatusTooManyRequests, dto.ErrorCodeRateLimited, "rate limited: at most 30 messages per minute")
	errAmbiguousRoom    = httpx.NewError(http.StatusConflict, dto.ErrorCodeConflict, "ambiguous room name")
	errMessageNotFound  = httpx.NewError(http.StatusNotFound, dto.ErrorCodeNotFound, "message not found")
	errReactNotChat     = httpx.NewError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid, "can only react to chat messages")
)

// errNotMember answers a room reference the agent is not a member of (TOOL-04).
func errNotMember(room string) error {
	return httpx.NewError(http.StatusForbidden, dto.ErrorCodeForbidden, "not a member of room "+room)
}

// agentAPIRoutes are the endpoints of the Claude Code channel; agent tokens only (TOK-03).
func (s *server) agentAPIRoutes(mux *http.ServeMux) {
	s.handle(mux, "GET /api/v1/agent/rooms", s.agentOnly(s.agentRooms))
	s.handle(mux, "GET /api/v1/agent/rooms/{room}/info", s.agentOnly(s.agentRoomInfo))
	s.handle(mux, "GET /api/v1/agent/rooms/{room}/messages", s.agentOnly(s.agentHistory))
	s.handle(mux, "POST /api/v1/agent/rooms/{room}/messages", s.agentOnly(s.agentPost))
	s.handle(mux, "POST /api/v1/agent/messages/{id}/reply", s.agentOnly(s.agentReply))
	s.handle(mux, "POST /api/v1/agent/messages/{id}/react", s.agentOnly(s.agentReact))
	s.handle(mux, "GET /api/v1/agent/attachments/{id}", s.agentOnly(s.downloadAttachment))
	s.handle(mux, "POST /api/v1/agent/register", s.agentRegister)
	s.handle(mux, "GET /api/v1/agent/pending", s.agentOnly(s.agentPending))
	s.handle(mux, "DELETE /api/v1/agent/token", s.revokeOwnAgentToken)
	s.handle(mux, "GET /api/v1/agent/agents", s.ownerAgents)
	s.handle(mux, "GET /api/v1/agent/session", s.agentSession)
	s.handle(mux, "GET /api/v1/agent/whoami", s.agentWhoAmI)
}

// agentToken returns the principal of a request made with an agent token; no registered agent
// is needed.
func agentToken(r *http.Request) (authn.Principal, error) {
	p, ok := authn.PrincipalFrom(r.Context())
	if !ok {
		return authn.Principal{}, errNotLoggedIn
	}
	if p.Kind != authn.KindAgent {
		return authn.Principal{}, agents.ErrAgentTokenRequired
	}
	return p, nil
}

// agentSession reports whether the machine and folder named in the headers have an AI, whether
// it is online, its app and when its live session connected, and changes nothing (SES-01): the
// launcher asks before taking over a live session.
func (s *server) agentSession(w http.ResponseWriter, r *http.Request) error {
	p, err := agentToken(r)
	if err != nil {
		return err
	}
	id, err := agents.FolderIDFromHeader(r.Header)
	if err != nil {
		return err
	}
	a, found, err := s.Agents.LookupFolder(r.Context(), p.UserID, id)
	if err != nil {
		return err
	}
	out := dto.SessionState{}
	if found {
		ref := dto.IntelligenceRef{ID: a.ID, Kind: dto.IntelligenceKindAI, Role: dto.IntelligenceRoleAI, DisplayName: agents.DisplayName(a)}
		rows, err := s.Intelligences.Rows(r.Context(), []string{a.ID})
		if err != nil {
			return err
		}
		if row, ok := rows[a.ID]; ok {
			ref = convert.IntelligenceRef(row, dto.IntelligenceRoleAI, "")
		}
		out = dto.SessionState{Exists: true, Online: s.Agents.Online(a.ID), Intelligence: &ref, App: a.Harness}
		if out.Online {
			out.ConnectedAt = convert.OptTime(a.ConnectedAt)
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// agentWhoAmI returns the intelligence of the token's owner, for `partyline login`; it needs no
// folder and no registered AI.
func (s *server) agentWhoAmI(w http.ResponseWriter, r *http.Request) error {
	p, err := agentToken(r)
	if err != nil {
		return err
	}
	ref := dto.IntelligenceRef{ID: p.UserID, Kind: dto.IntelligenceKindHuman, Role: dto.IntelligenceRoleUser}
	rows, err := s.Intelligences.Rows(r.Context(), []string{p.UserID})
	if err != nil {
		return err
	}
	if row, ok := rows[p.UserID]; ok {
		ref = convert.IntelligenceRef(row, dto.IntelligenceRoleUser, "")
	}
	httpx.WriteJSON(w, http.StatusOK, ref)
	return nil
}

// agentOf finds the AI a request acts as: the one of the machine and folder named in the
// headers, created on first sight (AID-04); a request without a folder is a v0.3 client that
// names its agent.
func (s *server) agentOf(r *http.Request, p authn.Principal) (db.Agent, error) {
	if agents.HasFolder(r.Header) {
		id, err := agents.FolderIDFromHeader(r.Header)
		if err != nil {
			return db.Agent{}, err
		}
		return s.Agents.ResolveFolder(r.Context(), p.UserID, id, false)
	}
	return s.Agents.Lookup(r.Context(), p.UserID, p.AgentName)
}

// ownerAgents lists the agents the token's owner owns, like GET /agents
// (include_archived=true adds archived ones), for `partyline agents` (CLI-02). Agent tokens
// only; no registered agent is needed.
func (s *server) ownerAgents(w http.ResponseWriter, r *http.Request) error {
	p, ok := authn.PrincipalFrom(r.Context())
	if !ok {
		return errNotLoggedIn
	}
	if p.Kind != authn.KindAgent {
		return agents.ErrAgentTokenRequired
	}
	includeArchived, _ := strconv.ParseBool(r.URL.Query().Get("include_archived"))
	list, err := s.Agents.ListVisible(r.Context(), authn.Principal{Kind: authn.KindUser, UserID: p.UserID}, includeArchived)
	if err != nil {
		return err
	}
	out := dto.List[dto.Agent]{Items: []dto.Agent{}}
	for _, a := range list {
		if a.OwnerID == p.UserID {
			out.Items = append(out.Items, toAgent(a))
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// revokeOwnAgentToken revokes the agent token the request is made with, so `partyline setup
// --uninstall` can cut the machine off without a browser (ONB-09). It needs no registered
// agent. Agent tokens only: user tokens are revoked from the web app.
func (s *server) revokeOwnAgentToken(w http.ResponseWriter, r *http.Request) error {
	p, ok := authn.PrincipalFrom(r.Context())
	if !ok {
		return errNotLoggedIn
	}
	if p.Kind != authn.KindAgent || p.TokenID == "" {
		return agents.ErrAgentTokenRequired
	}
	if err := s.Users.RevokeToken(r.Context(), p.UserID, p.TokenID); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// AgentInbox reads an agent's undelivered messages for harnesses without the WebSocket
// (TURN-01, TURN-02); the agent gateway implements it.
type AgentInbox interface {
	Pending(ctx context.Context, a db.Agent, limit int) ([]dto.AgentMessage, error)
	Online(agentID string) bool
}

// MaxPendingWait bounds GET /agent/pending?wait= in seconds (TURN-01).
const MaxPendingWait = 300

// pendingPoll is how often a waiting GET /agent/pending looks for new messages.
var pendingPoll = 500 * time.Millisecond

var errPushedSession = httpx.NewError(http.StatusConflict, dto.ErrorCodeConflict, "messages are pushed to this agent's session; there is nothing to wait for")

// agentPending returns up to 20 undelivered messages, oldest first, and counts them as
// delivered. With wait=N (1-300 s) it waits up to N seconds for the first one and returns an
// empty list on timeout. An agent connected over the WebSocket gets 409: its messages are
// pushed there.
func (s *server) agentPending(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	wait := 0
	if v := r.URL.Query().Get("wait"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > MaxPendingWait {
			c := validation.Check{}
			c.Add("wait", "must be a whole number of seconds from 0 to 300")
			return c.Err()
		}
		wait = n
	}
	a, err := s.Agents.ByID(r.Context(), p.AgentID) // agentOnly resolved it
	if err != nil {
		return err
	}
	if s.Inbox.Online(a.ID) {
		return errPushedSession
	}
	deadline := time.Now().Add(time.Duration(wait) * time.Second)
	for {
		msgs, err := s.Inbox.Pending(r.Context(), a, agents.PendingLimit)
		if err != nil {
			return err
		}
		left := time.Until(deadline)
		if len(msgs) > 0 || left <= 0 {
			httpx.WriteJSON(w, http.StatusOK, dto.List[dto.AgentMessage]{Items: msgs})
			return nil
		}
		select {
		case <-r.Context().Done():
			return r.Context().Err()
		case <-time.After(min(pendingPoll, left)):
		}
	}
}

// agentRegister registers the session's agent (identity headers, as on the WebSocket) without a WebSocket, for
// `partyline channel --tools-only` (PUSH-02); the agent stays offline. Agent tokens only.
func (s *server) agentRegister(w http.ResponseWriter, r *http.Request) error {
	p, err := agentToken(r)
	if err != nil {
		return err
	}
	var row db.GetAgentWithOwnerRow
	if agents.HasFolder(r.Header) {
		var id agents.FolderID
		if id, err = agents.FolderIDFromHeader(r.Header); err != nil {
			return err
		}
		row, err = s.Agents.RegisterFolder(r.Context(), p.UserID, id)
	} else {
		id := agents.IdentityFromHeader(r.Header)
		id.Name = p.AgentName
		row, err = s.Agents.Register(r.Context(), p.UserID, id)
	}
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dto.AgentInfo{
		ID: row.Agent.ID, Name: row.Agent.Name, DisplayName: agents.DisplayName(row.Agent), Owner: row.OwnerUsername,
	})
	return nil
}

// agentOnly requires an agent token and resolves the agent from the token's owner and the
// X-Partyline-Agent name, never from client-supplied ids. Agents register on their first
// WebSocket connection; before that the REST endpoints answer 403.
func (s *server) agentOnly(h principalFunc) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		p, ok := authn.PrincipalFrom(r.Context())
		if !ok {
			return errNotLoggedIn
		}
		if p.Kind != authn.KindAgent {
			return agents.ErrAgentTokenRequired
		}
		a, err := s.agentOf(r, p)
		if err != nil {
			return err
		}
		p.AgentID, p.AgentName = a.ID, a.Name
		return h(w, r.WithContext(authn.WithPrincipal(r.Context(), p)), p)
	}
}

// agentRooms lists the agent's rooms with their members (TOOL-09), including its project's
// rooms while it is the project's active session (PROJ-07).
func (s *server) agentRooms(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	states, err := s.Agents.SessionRooms(r.Context(), p.AgentID)
	if err != nil {
		return err
	}
	out := dto.List[dto.AgentRoom]{Items: make([]dto.AgentRoom, 0, len(states))}
	for _, st := range states {
		room, err := s.Rooms.Get(r.Context(), p, st.ID)
		if err != nil {
			return err
		}
		members, err := s.Rooms.Members(r.Context(), p, st.ID)
		if err != nil {
			return err
		}
		ar := dto.AgentRoom{ID: room.ID, Name: room.Name, Description: room.Description, Members: make([]dto.RoomMember, len(members))}
		for i, m := range members {
			ar.Members[i] = s.toMember(r.Context(), m)
		}
		out.Items = append(out.Items, ar)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// agentRoomInfo returns the room's name, instructions, members and the agent's own
// membership with its settings (INS-02).
func (s *server) agentRoomInfo(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	st, err := s.resolveRoom(r.Context(), p, r.PathValue("room"))
	if err != nil {
		return err
	}
	room, err := s.Rooms.Get(r.Context(), p, st.ID)
	if err != nil {
		return err
	}
	members, err := s.Rooms.Members(r.Context(), p, st.ID)
	if err != nil {
		return err
	}
	out := dto.RoomInfo{
		Room: dto.RoomRef{ID: room.ID, Name: room.Name}, Instructions: room.Instructions,
		InstructionsBy: room.InstructionsByName,
		// INS-04: trusted only when the calling agent's owner saved them.
		InstructionsTrusted: room.Instructions != "" && room.InstructionsBy == p.UserID,
		// INS-06: the name and description are trusted only when the owner last wrote them.
		RoomTextTrusted: room.UpdatedBy == p.UserID,
		Members:         make([]dto.RoomMember, len(members)),
	}
	for i, m := range members {
		out.Members[i] = s.toMember(r.Context(), m)
		if (out.Members[i].Kind == dto.MemberKindAgent && m.MemberID == p.AgentID) ||
			(st.ProjectID != "" && out.Members[i].Kind == dto.MemberKindProject && m.MemberID == st.ProjectID) {
			out.Me = out.Members[i] // a session in the room through its project: the project member
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// resolveRoom finds the agent's room by id, else by name (case-insensitive) among its rooms
// (SessionRooms: its project's rooms count while it is the active session). Unknown rooms and
// rooms without the agent are 403 "not a member of room <room>" (TOOL-04); a name shared by
// several of its rooms is 409.
func (s *server) resolveRoom(ctx context.Context, p authn.Principal, ref string) (agents.RoomState, error) {
	states, err := s.Agents.SessionRooms(ctx, p.AgentID)
	if err != nil {
		return agents.RoomState{}, err
	}
	var byName []agents.RoomState
	for _, st := range states {
		if st.ID == ref {
			return st, nil
		}
		if strings.EqualFold(st.Name, ref) || agents.Slug(st.Name) == ref {
			byName = append(byName, st)
		}
	}
	switch len(byName) {
	case 1:
		return byName[0], nil
	case 0:
		return agents.RoomState{}, errNotMember(ref)
	default:
		return agents.RoomState{}, errAmbiguousRoom
	}
}

// agentHistory returns up to 100 messages (default 20), newest first, before an optional
// message id, each with the trusted flag for this agent (TOOL-05).
func (s *server) agentHistory(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	room, err := s.resolveRoom(r.Context(), p, r.PathValue("room"))
	if err != nil {
		return err
	}
	pg, err := httpx.ParsePage(r, agentHistoryDefault, agentHistoryMax)
	if err != nil {
		return err
	}
	page := rooms.Page{Limit: pg.Limit}
	if pg.Before != "" {
		ref, err := s.Rooms.MessageRef(r.Context(), pg.Before)
		if err != nil || ref.RoomID != room.ID {
			return httpx.Invalid(map[string]string{"before": "must be a message of the room"})
		}
		page.BeforeSeq = ref.Seq
	}
	list, more, err := s.Rooms.Messages(r.Context(), p, room.ID, page)
	if err != nil {
		return err
	}
	out := dto.Page[dto.AgentMessage]{Items: make([]dto.AgentMessage, len(list)), HasMore: more}
	for i, m := range list {
		out.Items[i] = agentMessage(room, m, p.UserID)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

func agentMessage(room agents.RoomState, m rooms.Message, ownerID string) dto.AgentMessage {
	return dto.AgentMessage{
		Room:    dto.RoomRef{ID: room.ID, Name: room.Name},
		Trusted: delivery.Trusted(m.AuthorUserID, ownerID),
		Message: convert.Message(m),
	}
}

// agentPost posts JSON or multipart as the agent (TOOL-03, TOOL-07), 30 per minute (TOOL-08).
func (s *server) agentPost(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	room, err := s.resolveRoom(r.Context(), p, r.PathValue("room"))
	if err != nil {
		return err
	}
	if !s.Agents.AllowPost(p.AgentID) {
		return errAgentRateLimited
	}
	return s.postTo(w, r, p, room.ID)
}

// agentTarget loads a message of one of the agent's rooms; 404 "message not found" for
// unknown messages and messages of other rooms.
func (s *server) agentTarget(ctx context.Context, p authn.Principal, id string) (rooms.Message, error) {
	ref, err := s.Rooms.MessageRef(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return rooms.Message{}, errMessageNotFound
	}
	if err != nil {
		return rooms.Message{}, err
	}
	orig, err := s.Rooms.GetMessage(ctx, p, ref.RoomID, id)
	if err != nil {
		return rooms.Message{}, errMessageNotFound // the agent is not in the message's room
	}
	return orig, nil
}

// agentReply answers a message in its room with reply_to set; a reply to a chat message
// goes to that chat only (TOOL-02).
func (s *server) agentReply(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.ReplyRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	orig, err := s.agentTarget(r.Context(), p, r.PathValue("id"))
	if err != nil {
		return err
	}
	if !s.Agents.AllowPost(p.AgentID) {
		return errAgentRateLimited
	}
	in := rooms.PostInput{Text: req.Text, ReplyTo: orig.ID}
	if orig.AuthorKind == string(dto.AuthorKindChat) {
		in.Recipients = []string{orig.AuthorID}
	}
	m, err := s.Rooms.Post(r.Context(), p, orig.RoomID, in)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, convert.Message(m))
	return nil
}

// agentReact reacts to a chat message with one emoji (REACT-01): the reaction is stored as a
// message of kind reaction (text = emoji, reply_to = target) whose only recipient is the
// target's chat; the connectors' sender turns it into a platform reaction. Non-chat targets
// answer 422 "can only react to chat messages" (REACT-02).
func (s *server) agentReact(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.ReactRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	orig, err := s.agentTarget(r.Context(), p, r.PathValue("id"))
	if err != nil {
		return err
	}
	if orig.AuthorKind != string(dto.AuthorKindChat) {
		return errReactNotChat
	}
	emoji := strings.TrimSpace(req.Emoji)
	if !validation.OneEmoji(emoji) { // REACT-05
		return httpx.Invalid(map[string]string{"emoji": "must be one emoji"})
	}
	if !s.Agents.AllowPost(p.AgentID) {
		return errAgentRateLimited
	}
	m, err := s.Rooms.Post(r.Context(), p, orig.RoomID, rooms.PostInput{
		Kind: dto.MessageKindReaction, Text: emoji, ReplyTo: orig.ID, Recipients: []string{orig.AuthorID},
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, convert.Message(m))
	return nil
}
