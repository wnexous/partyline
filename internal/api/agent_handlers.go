package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"

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
}

// agentRegister registers the agent named by X-Partyline-Agent without a WebSocket, for
// `partyline channel --tools-only` (PUSH-02); the agent stays offline. Agent tokens only.
func (s *server) agentRegister(w http.ResponseWriter, r *http.Request) error {
	p, ok := authn.PrincipalFrom(r.Context())
	if !ok {
		return errNotLoggedIn
	}
	if p.Kind != authn.KindAgent {
		return agents.ErrAgentTokenRequired
	}
	row, err := s.Agents.Register(r.Context(), p.UserID, p.AgentName)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dto.AgentInfo{ID: row.Agent.ID, Name: row.Agent.Name, Owner: row.OwnerUsername})
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
		a, err := s.Agents.Lookup(r.Context(), p.UserID, p.AgentName)
		if err != nil {
			return err
		}
		p.AgentID, p.AgentName = a.ID, a.Name
		return h(w, r.WithContext(authn.WithPrincipal(r.Context(), p)), p)
	}
}

// agentRooms lists the agent's rooms with their members (TOOL-09).
func (s *server) agentRooms(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	states, err := s.Agents.Rooms(r.Context(), p.AgentID)
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
			ar.Members[i] = s.toMember(m)
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
		Members:             make([]dto.RoomMember, len(members)),
	}
	for i, m := range members {
		out.Members[i] = s.toMember(m)
		if out.Members[i].Kind == dto.MemberKindAgent && m.MemberID == p.AgentID {
			out.Me = out.Members[i]
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// resolveRoom finds the agent's room by id, else by name (case-insensitive) among its rooms.
// Unknown rooms and rooms without the agent are 403 "not a member of room <room>" (TOOL-04);
// a name shared by several of its rooms is 409.
func (s *server) resolveRoom(ctx context.Context, p authn.Principal, ref string) (db.ListAgentRoomStatesRow, error) {
	states, err := s.Agents.Rooms(ctx, p.AgentID)
	if err != nil {
		return db.ListAgentRoomStatesRow{}, err
	}
	var byName []db.ListAgentRoomStatesRow
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
		return db.ListAgentRoomStatesRow{}, errNotMember(ref)
	default:
		return db.ListAgentRoomStatesRow{}, errAmbiguousRoom
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

func agentMessage(room db.ListAgentRoomStatesRow, m rooms.Message, ownerID string) dto.AgentMessage {
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
