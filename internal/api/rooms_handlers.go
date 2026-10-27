package api

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/wnexous/partyline/internal/api/convert"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/attachments"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/rooms"
)

func (s *server) roomRoutes(mux *http.ServeMux) {
	s.handle(mux, "GET /api/v1/activity", authed(s.listActivity))
	s.handle(mux, "GET /api/v1/rooms", authed(s.listRooms))
	s.handle(mux, "POST /api/v1/rooms", authed(s.createRoom))
	s.handle(mux, "GET /api/v1/rooms/{id}", authed(s.getRoom))
	s.handle(mux, "PATCH /api/v1/rooms/{id}", authed(s.updateRoom))
	s.handle(mux, "DELETE /api/v1/rooms/{id}", authed(s.deleteRoom))
	s.handle(mux, "GET /api/v1/rooms/{id}/members", authed(s.listMembers))
	s.handle(mux, "POST /api/v1/rooms/{id}/members", authed(s.addMember))
	s.handle(mux, "PATCH /api/v1/rooms/{id}/members/{mid}", authed(s.updateMember))
	s.handle(mux, "DELETE /api/v1/rooms/{id}/members/{mid}", authed(s.removeMember))
	s.handle(mux, "GET /api/v1/rooms/{id}/messages", authed(s.listMessages))
	s.handle(mux, "POST /api/v1/rooms/{id}/messages", authed(s.postMessage))
	s.handle(mux, "PATCH /api/v1/rooms/{id}/messages/{mid}", authed(s.editMessage))
	s.handle(mux, "DELETE /api/v1/rooms/{id}/messages/{mid}", authed(s.deleteMessage))
	s.handle(mux, "POST /api/v1/rooms/{id}/resume", authed(s.resumeRoom))
	s.handle(mux, "GET /api/v1/rooms/{id}/attachments", authed(s.listRoomFiles))
	s.handle(mux, "GET /api/v1/attachments/{id}", authed(s.downloadAttachment))
}

// toMember renders a member; agent members carry their online state.
func (s *server) toMember(m rooms.Member) dto.RoomMember {
	out := dto.RoomMember{
		ID: m.ID, Kind: dto.MemberKind(m.Kind), MemberID: m.MemberID, Name: m.Name,
		Owner: m.AgentOwner, ConnectorKind: m.ConnectorKind, ChatKind: dto.ChatKind(m.ChatKind),
		DisplayName: m.DisplayName, Delivery: dto.DeliveryMode(m.Delivery),
		Keywords: rooms.Keywords(m.RoomMember), CreatedAt: msTime(m.CreatedAt),
	}
	if out.Kind == dto.MemberKindAgent && s.Agents != nil {
		online := s.Agents.Online(m.MemberID)
		out.Online = &online
	}
	if m.Intelligence.ID != "" && (out.Kind == dto.MemberKindAgent || out.Kind == dto.MemberKindUser) {
		role := dto.IntelligenceRoleUser
		if out.Kind == dto.MemberKindAgent {
			role = dto.IntelligenceRoleAI
		}
		ref := convert.IntelligenceRef(m.Intelligence, role, "")
		out.Intelligence = &ref
	}
	return out
}

func (s *server) listRooms(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	list, err := s.Rooms.List(r.Context(), p)
	if err != nil {
		return err
	}
	out := dto.List[dto.Room]{Items: make([]dto.Room, len(list))}
	for i, room := range list {
		out.Items[i] = convert.Room(room)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// createRoom makes the caller the owner (ACL-01).
func (s *server) createRoom(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.CreateRoomRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	room, err := s.Rooms.Create(r.Context(), p, rooms.CreateInput{
		Name: req.Name, Description: req.Description, Instructions: req.Instructions, RetentionDays: req.RetentionDays,
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, convert.Room(room))
	return nil
}

func (s *server) getRoom(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	room, err := s.Rooms.Get(r.Context(), p, r.PathValue("id"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, convert.Room(room))
	return nil
}

func (s *server) updateRoom(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.UpdateRoomRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	room, err := s.Rooms.Update(r.Context(), p, r.PathValue("id"), rooms.UpdateInput{
		Name: req.Name, Description: req.Description, Instructions: req.Instructions, RetentionDays: req.RetentionDays,
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, convert.Room(room))
	return nil
}

func (s *server) deleteRoom(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	if err := s.Rooms.Delete(r.Context(), p, r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *server) listMembers(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	list, err := s.Rooms.Members(r.Context(), p, r.PathValue("id"))
	if err != nil {
		return err
	}
	var chatIDs []string
	for _, m := range list {
		if dto.MemberKind(m.Kind) == dto.MemberKindChat {
			chatIDs = append(chatIDs, m.MemberID)
		}
	}
	people, err := s.Intelligences.ChatPeople(r.Context(), chatIDs)
	if err != nil {
		return err
	}
	out := dto.List[dto.RoomMember]{Items: make([]dto.RoomMember, len(list))}
	for i, m := range list {
		out.Items[i] = s.toMember(m)
		for _, person := range people[m.MemberID] {
			out.Items[i].People = append(out.Items[i].People, convert.IntelligenceRef(person.Intelligence, dto.IntelligenceRolePerson, person.Platform))
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// addMember needs manage on the room and use on the agent or the chat's connector (ACL-05/06).
func (s *server) addMember(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.AddMemberRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	m, err := s.Rooms.AddMember(r.Context(), p, r.PathValue("id"), rooms.MemberInput{Kind: req.Kind, MemberID: req.MemberID})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, s.toMember(m))
	return nil
}

func (s *server) updateMember(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.UpdateMemberRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	m, err := s.Rooms.UpdateMember(r.Context(), p, r.PathValue("id"), r.PathValue("mid"), rooms.MemberUpdate{
		DisplayName: req.DisplayName, Delivery: req.Delivery, Keywords: req.Keywords,
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, s.toMember(m))
	return nil
}

// removeMember stops later deliveries to the member (ROOM-01).
func (s *server) removeMember(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	if err := s.Rooms.RemoveMember(r.Context(), p, r.PathValue("id"), r.PathValue("mid")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *server) listMessages(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	pg, err := httpx.ParsePage(r, dto.DefaultPageSize, dto.MaxPageSize)
	if err != nil {
		return err
	}
	list, more, err := s.Rooms.Messages(r.Context(), p, r.PathValue("id"), rooms.Page{BeforeSeq: pg.BeforeSeq, AfterSeq: pg.AfterSeq, Limit: pg.Limit})
	if err != nil {
		return err
	}
	out := dto.Page[dto.Message]{Items: make([]dto.Message, len(list)), HasMore: more}
	for i, m := range list {
		out.Items[i] = convert.Message(m)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

var errUploadTooLarge = httpx.NewError(http.StatusRequestEntityTooLarge, dto.ErrorCodeTooLarge, "attachment too large")

// postMessage accepts a JSON PostMessageRequest, or multipart with a "message" part and
// "file" parts (MSG-02, SEC-03). Posting needs the post level (ACL-04).
func (s *server) postMessage(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	return s.postTo(w, r, p, r.PathValue("id"))
}

// postTo stores a JSON or multipart message from p in the room and answers 201 with it.
func (s *server) postTo(w http.ResponseWriter, r *http.Request, p authn.Principal, roomID string) error {
	if err := s.Rooms.Require(r.Context(), p, roomID, dto.LevelPost); err != nil {
		return err
	}
	var req dto.PostMessageRequest
	var files []rooms.File
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt == "multipart/form-data" {
		var err error
		if req, files, err = s.readMultipartMessage(r); err != nil {
			return err
		}
	} else if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	if req.ReplyTo != "" && p.Kind == authn.KindAgent {
		// A message that arrived on the AI's connection is no message of the room to quote (CID-22).
		if mine, err := s.isPlatformMessage(r.Context(), p.AgentID, req.ReplyTo); err != nil {
			return err
		} else if mine {
			return errUnsupportedQuote
		}
	}
	m, err := s.Rooms.Post(r.Context(), p, roomID, rooms.PostInput{
		Text: req.Text, ReplyTo: req.ReplyTo, Files: files,
		QuickReplies: req.QuickReplies, Wizard: req.Wizard, Answers: req.Answers,
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, convert.Message(m))
	return nil
}

func (s *server) readMultipartMessage(r *http.Request) (dto.PostMessageRequest, []rooms.File, error) {
	var req dto.PostMessageRequest
	var files []rooms.File
	mr, err := r.MultipartReader()
	if err != nil {
		return req, nil, httpx.NewError(http.StatusBadRequest, dto.ErrorCodeInvalid, "malformed multipart body")
	}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return req, files, nil
		}
		if err != nil {
			return req, nil, multipartError(err)
		}
		switch part.FormName() {
		case dto.MultipartMessageField:
			data, err := io.ReadAll(io.LimitReader(part, httpx.MaxJSONBody+1))
			if err != nil {
				return req, nil, multipartError(err)
			}
			if len(data) > httpx.MaxJSONBody {
				return req, nil, httpx.NewError(http.StatusRequestEntityTooLarge, dto.ErrorCodeTooLarge, "message part too large")
			}
			if err := json.Unmarshal(data, &req); err != nil {
				return req, nil, httpx.NewError(http.StatusBadRequest, dto.ErrorCodeInvalid, "malformed message part")
			}
		case dto.MultipartFileField:
			sha, size, err := s.Blobs.Put(part, s.Config.MaxUploadBytes)
			if err != nil {
				return req, nil, multipartError(err)
			}
			ct := part.Header.Get("Content-Type")
			if ct == "" {
				ct = "application/octet-stream"
			}
			name := filepath.Base(part.FileName())
			if name == "." || name == "/" {
				name = "file"
			}
			files = append(files, rooms.File{SHA256: sha, Name: name, Mime: ct, Size: size})
		}
		_ = part.Close()
	}
}

func multipartError(err error) error {
	var mbe *http.MaxBytesError
	if errors.Is(err, attachments.ErrTooLarge) || errors.As(err, &mbe) {
		return errUploadTooLarge
	}
	return httpx.NewError(http.StatusBadRequest, dto.ErrorCodeInvalid, "malformed multipart body")
}

// editMessage replaces the text of a message its author wrote and answers 200 with the message (EDT-01..EDT-05).
func (s *server) editMessage(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.EditMessageRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	m, err := s.Rooms.EditMessage(r.Context(), p, r.PathValue("id"), r.PathValue("mid"), req.Text)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, convert.Message(m))
	return nil
}

// deleteMessage turns a message into its placeholder and answers 200 with it (DLT-01..DLT-04). The message
// stays, so the answer is the message and not 204.
func (s *server) deleteMessage(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	m, err := s.Rooms.DeleteMessage(r.Context(), p, r.PathValue("id"), r.PathValue("mid"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, convert.Message(m))
	return nil
}

func (s *server) resumeRoom(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	room, err := s.Rooms.Resume(r.Context(), p, r.PathValue("id"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, convert.Room(room))
	return nil
}

// inlineMimes are the attachment types the browser may display; everything else downloads.
var inlineMimes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}

// downloadAttachment serves a file of a room the caller can view; others get 404.
func (s *server) downloadAttachment(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	a, err := s.Rooms.Attachment(r.Context(), p, r.PathValue("id"))
	if err != nil {
		return err
	}
	return s.serveBlob(w, r, a.Sha256, a.Name, a.Mime)
}

// serveBlob answers with the stored file sha: images show inline, everything else downloads under
// its name. A file that was not stored (no hash) or whose blob is gone is a 404.
func (s *server) serveBlob(w http.ResponseWriter, r *http.Request, sha, name, mimeType string) error {
	f, err := s.Blobs.Open(sha)
	if errors.Is(err, attachments.ErrNotFound) {
		return httpx.NewError(http.StatusNotFound, dto.ErrorCodeNotFound, "not found")
	}
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	disposition, ct := "attachment", "application/octet-stream"
	if mt, _, err := mime.ParseMediaType(mimeType); err == nil && inlineMimes[strings.ToLower(mt)] {
		disposition, ct = "inline", mt
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": name}))
	http.ServeContent(w, r, "", time.Time{}, f)
	return nil
}
