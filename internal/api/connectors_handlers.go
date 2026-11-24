package api

import (
	"net/http"
	"strings"

	"github.com/wnexous/partyline/internal/api/convert"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/platform"
)

// connectorRoutes are the connector management endpoints (CON-01..05).
func (s *server) connectorRoutes(mux *http.ServeMux) {
	s.handle(mux, "GET /api/v1/connector-kinds", authed(s.listConnectorKinds))
	s.handle(mux, "GET /api/v1/connectors", authed(s.listConnectors))
	s.handle(mux, "POST /api/v1/connectors", authed(s.createConnector))
	s.handle(mux, "GET /api/v1/connectors/{id}", authed(s.getConnector))
	s.handle(mux, "PATCH /api/v1/connectors/{id}", authed(s.updateConnector))
	s.handle(mux, "DELETE /api/v1/connectors/{id}", authed(s.deleteConnector))
	s.handle(mux, "GET /api/v1/connectors/{id}/chats/{chat}/history", authed(s.chatHistory))
}

func toConnectorKind(k connectors.Kind) dto.ConnectorKind {
	out := dto.ConnectorKind{Name: k.Name, Label: k.Label, CanListChats: k.CanListChats, Fields: make([]dto.ConnectorField, len(k.Fields))}
	for i, f := range k.Fields {
		out.Fields[i] = dto.ConnectorField{
			Name: f.Name, Label: f.Label, Type: f.Type, Required: f.Required, Secret: f.Secret(), Help: f.Help, Default: f.Default, Generated: f.Generated,
		}
	}
	return out
}

func (s *server) toConnector(v connectors.View) dto.Connector {
	out := dto.Connector{
		ID: v.ID, Kind: v.Kind, Name: v.Name, OrgID: v.OrgID.String,
		Owner:   dto.User{ID: v.OwnerID, Username: v.OwnerUsername, DisplayName: v.OwnerDisplayName},
		MyLevel: v.Level, Enabled: v.Enabled, Status: dto.ConnectorStatus(v.Status), LastError: v.LastError,
		AccountLabel: v.AccountLabel, Deliver: dto.ConnectorDeliver(v.Deliver), GroupsMentionsOnly: v.GroupsMentionsOnly, Queued: v.Queued,
		HistoryDays: v.HistoryDays,
		Config:      v.Config,
		CreatedAt:   convert.Time(v.CreatedAt), UpdatedAt: convert.Time(v.UpdatedAt),
	}
	if v.HasOperator {
		operator := convert.IntelligenceRef(v.Operator, dto.IntelligenceRoleAI, "")
		out.Operator = &operator
	}
	if len(v.Generated) > 0 {
		out.GeneratedSecrets = v.Generated
	}
	if v.InboundHook {
		out.InboundURL = strings.TrimRight(s.Config.PublicURL.String(), "/") + "/api/v1/hooks/" + v.ID
	}
	return out
}

func (s *server) listConnectorKinds(w http.ResponseWriter, _ *http.Request, _ authn.Principal) error {
	kinds := s.Connectors.Kinds()
	out := dto.List[dto.ConnectorKind]{Items: make([]dto.ConnectorKind, len(kinds))}
	for i, k := range kinds {
		out.Items[i] = toConnectorKind(k)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

func (s *server) listConnectors(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	list, err := s.Connectors.List(r.Context(), p)
	if err != nil {
		return err
	}
	out := dto.List[dto.Connector]{Items: make([]dto.Connector, len(list))}
	for i, v := range list {
		out.Items[i] = s.toConnector(v)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

func (s *server) createConnector(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.CreateConnectorRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	v, err := s.Connectors.Create(r.Context(), p, connectors.CreateInput{
		Org: req.OrgID, Kind: req.Kind, Name: req.Name, Config: req.Config, Enabled: req.Enabled,
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, s.toConnector(v))
	return nil
}

func (s *server) getConnector(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	v, err := s.Connectors.Get(r.Context(), p, r.PathValue("id"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, s.toConnector(v))
	return nil
}

func (s *server) updateConnector(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.UpdateConnectorRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	v, err := s.Connectors.Update(r.Context(), p, r.PathValue("id"), connectors.UpdateInput{
		Name: req.Name, Config: req.Config, Enabled: req.Enabled, OperatorID: req.OperatorID,
		Deliver: req.Deliver, GroupsMentionsOnly: req.GroupsMentionsOnly, HistoryDays: req.HistoryDays,
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, s.toConnector(v))
	return nil
}

func (s *server) deleteConnector(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	if err := s.Connectors.Delete(r.Context(), p, r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// chatHistory serves a page of the history of a chat of a connection (CH-05, CH-07): to the AI that
// operates it (the agent route) and to the users who manage it; anyone else gets 404 (CH-06).
func (s *server) chatHistory(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	pg, err := httpx.ParsePage(r, platform.HistoryPageDefault, platform.HistoryPageMax)
	if err != nil {
		return err
	}
	page, err := s.Connectors.ChatHistory(r.Context(), p, r.PathValue("id"), r.PathValue("chat"), pg.Before, pg.Limit)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, toChatHistory(page))
	return nil
}

// fetchChatHistory asks the platform for older messages of a chat (CH-08); agent route only.
func (s *server) fetchChatHistory(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.FetchHistoryRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	n, err := s.Connectors.FetchHistory(r.Context(), p, r.PathValue("id"), r.PathValue("chat"), req.Count)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusAccepted, dto.FetchHistoryResult{Requested: n})
	return nil
}

// historyYou is how the account itself is named in a chat's history.
const historyYou = "you"

func toChatHistory(page connectors.HistoryPage) dto.ChatHistory {
	out := dto.ChatHistory{
		ConnectionID: page.Connector.ID, Platform: page.Connector.Kind, ChatID: page.Chat.ID,
		ChatTitle: page.Chat.Title, ChatKind: dto.ChatKind(page.Chat.Kind),
		Messages: make([]dto.ChatHistoryMessage, len(page.Messages)), HasMore: page.HasMore,
	}
	for i, m := range page.Messages {
		msg := dto.ChatHistoryMessage{
			MessageID: m.ID, From: historyFrom(m.FromSelf, m.SenderName, m.SenderID), FromSelf: m.FromSelf,
			Trusted: m.Trusted && !m.FromSelf, Time: m.At, Kind: m.Kind, Text: m.Text,
			ReplyTo: m.ReplyTo, Quoted: m.Quoted, QuotedFromSelf: m.QuotedFromSelf, Edited: m.Edited, Deleted: m.Deleted,
		}
		if !m.FromSelf {
			msg.SenderID = m.SenderID
		}
		for _, a := range m.Attachments {
			att := dto.ChatHistoryAttachment{Name: a.Name, Mime: a.Mime, Size: a.Size, Status: a.Status}
			if a.Kept {
				att.ID = a.ID
			}
			msg.Attachments = append(msg.Attachments, att)
		}
		for _, r := range m.Reactions {
			re := dto.ChatHistoryReaction{Emoji: r.Emoji, From: historyFrom(r.FromSelf, r.SenderName, r.SenderID), FromSelf: r.FromSelf}
			if !r.FromSelf {
				re.SenderID = r.SenderID
			}
			msg.Reactions = append(msg.Reactions, re)
		}
		out.Messages[i] = msg
	}
	return out
}

// historyFrom names the sender of a kept message: "you" for the account, else its name, else its id.
func historyFrom(self bool, name, id string) string {
	switch {
	case self:
		return historyYou
	case name != "":
		return name
	}
	return id
}
