package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/wnexous/partyline/internal/api/convert"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/users"
)

func (s *server) adminRoutes(mux *http.ServeMux) {
	s.handle(mux, "GET /api/v1/invites/{token}", s.getInvite)
	s.handle(mux, "POST /api/v1/invites/{token}/accept", s.acceptInvite)
	s.handle(mux, "GET /api/v1/admin/invites", adminOnly(s.listInvites))
	s.handle(mux, "POST /api/v1/admin/invites", adminOnly(s.createInvite))
	s.handle(mux, "DELETE /api/v1/admin/invites/{id}", adminOnly(s.revokeInvite))
	s.handle(mux, "GET /api/v1/admin/users", adminOnly(s.listAdminUsers))
	s.handle(mux, "PATCH /api/v1/admin/users/{id}", adminOnly(s.updateAdminUser))
	s.handle(mux, "GET /api/v1/admin/audit", adminOnly(s.listAudit))
	s.handle(mux, "GET /api/v1/users", authed(s.listDirectory))
	s.handle(mux, "GET /api/v1/tokens", authed(s.listTokens))
	s.handle(mux, "POST /api/v1/tokens", authed(s.createToken))
	s.handle(mux, "DELETE /api/v1/tokens/{id}", authed(s.revokeToken))
}

// optTime converts an optional unix-ms column to an optional time.
func optTime(v sql.NullInt64) *time.Time { return convert.OptTime(v) }

func toUser(u db.User) dto.User {
	return dto.User{ID: u.ID, Username: u.Username, DisplayName: u.DisplayName}
}

func toAdminUser(u db.User) dto.AdminUser {
	return dto.AdminUser{
		ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, Role: dto.Role(u.Role),
		Disabled: u.DisabledAt.Valid, DisabledAt: optTime(u.DisabledAt), CreatedAt: msTime(u.CreatedAt),
	}
}

func toInvite(inv users.Invite) dto.Invite {
	out := dto.Invite{
		ID: inv.Invite.ID, Role: dto.Role(inv.Invite.Role), Status: inv.Status,
		CreatedBy: dto.User{ID: inv.Invite.CreatedBy, Username: inv.CreatorUsername, DisplayName: inv.CreatorDisplayName},
		CreatedAt: msTime(inv.Invite.CreatedAt), ExpiresAt: msTime(inv.Invite.ExpiresAt),
		UsedAt: optTime(inv.Invite.UsedAt),
	}
	if inv.Invite.UsedBy.Valid {
		out.UsedBy = &dto.User{ID: inv.Invite.UsedBy.String, Username: inv.UsedByUsername, DisplayName: inv.UsedByDisplayName}
	}
	return out
}

func toToken(t db.ApiToken) dto.Token {
	return dto.Token{
		ID: t.ID, Kind: dto.TokenKind(t.Kind), Name: t.Name, Prefix: t.Prefix, CreatedAt: msTime(t.CreatedAt),
		LastUsedAt: optTime(t.LastUsedAt), ExpiresAt: optTime(t.ExpiresAt), RevokedAt: optTime(t.RevokedAt),
	}
}

func (s *server) getInvite(w http.ResponseWriter, r *http.Request) error {
	inv, err := s.Users.GetInvite(r.Context(), r.PathValue("token"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dto.InviteInfo{Role: dto.Role(inv.Role), ExpiresAt: msTime(inv.ExpiresAt)})
	return nil
}

// acceptInvite creates the account and logs it in (AUTH-07); a reused invite is 410.
func (s *server) acceptInvite(w http.ResponseWriter, r *http.Request) error {
	var req dto.AcceptInviteRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	u, err := s.Users.AcceptInvite(r.Context(), r.PathValue("token"), users.AccountInput{
		Username: req.Username, Password: req.Password, DisplayName: req.DisplayName,
	})
	if err != nil {
		return err
	}
	if err := s.startSession(w, r, u.ID); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, toMe(u))
	return nil
}

func (s *server) listInvites(w http.ResponseWriter, r *http.Request, _ authn.Principal) error {
	list, err := s.Users.ListInvites(r.Context())
	if err != nil {
		return err
	}
	out := dto.List[dto.Invite]{Items: make([]dto.Invite, len(list))}
	for i, inv := range list {
		out.Items[i] = toInvite(inv)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// createInvite returns the invite and its link once (AUTH-06).
func (s *server) createInvite(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.CreateInviteRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	creator, err := s.Users.GetUser(r.Context(), p.UserID)
	if err != nil {
		return err
	}
	inv, secret, err := s.Users.CreateInvite(r.Context(), p.UserID, req.Role)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, dto.CreatedInvite{
		Invite: toInvite(users.Invite{
			ListInvitesRow: db.ListInvitesRow{Invite: inv, CreatorUsername: creator.Username, CreatorDisplayName: creator.DisplayName},
			Status:         dto.InviteStatusPending,
		}),
		URL: strings.TrimRight(s.Config.PublicURL.String(), "/") + "/invite/" + secret,
	})
	return nil
}

func (s *server) revokeInvite(w http.ResponseWriter, r *http.Request, _ authn.Principal) error {
	if err := s.Users.RevokeInvite(r.Context(), r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *server) listAdminUsers(w http.ResponseWriter, r *http.Request, _ authn.Principal) error {
	list, err := s.Users.ListUsers(r.Context())
	if err != nil {
		return err
	}
	out := dto.List[dto.AdminUser]{Items: make([]dto.AdminUser, len(list))}
	for i, u := range list {
		out.Items[i] = toAdminUser(u)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// updateAdminUser changes a role or disables an account (AUTH-10).
func (s *server) updateAdminUser(w http.ResponseWriter, r *http.Request, _ authn.Principal) error {
	var req dto.UpdateUserRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	u, err := s.Users.UpdateUser(r.Context(), r.PathValue("id"), users.UserUpdate{Role: req.Role, Disabled: req.Disabled})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, toAdminUser(u))
	return nil
}

// listAudit returns audit entries newest first, 50 per page; ?before=<id> continues (AUD-02).
func (s *server) listAudit(w http.ResponseWriter, r *http.Request, _ authn.Principal) error {
	rows, more, err := s.Audit.List(r.Context(), r.URL.Query().Get("before"))
	if err != nil {
		return err
	}
	out := dto.Page[dto.AuditEntry]{Items: make([]dto.AuditEntry, len(rows)), HasMore: more}
	for i, row := range rows {
		e := dto.AuditEntry{
			ID: row.ID, Action: row.Action, TargetKind: row.TargetKind, TargetID: row.TargetID,
			IP: row.Ip, CreatedAt: msTime(row.CreatedAt),
		}
		if row.ActorID != "" {
			e.Actor = &dto.User{ID: row.ActorID, Username: row.ActorUsername, DisplayName: row.ActorDisplayName}
		}
		if err := json.Unmarshal([]byte(row.DetailJson), &e.Detail); err != nil {
			return err
		}
		out.Items[i] = e
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// listDirectory returns the active accounts, for sharing pickers.
func (s *server) listDirectory(w http.ResponseWriter, r *http.Request, _ authn.Principal) error {
	list, err := s.Users.ListUsers(r.Context())
	if err != nil {
		return err
	}
	out := dto.List[dto.User]{Items: make([]dto.User, 0, len(list))}
	for _, u := range list {
		if !u.DisabledAt.Valid {
			out.Items = append(out.Items, toUser(u))
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

func (s *server) listTokens(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	list, err := s.Users.ListTokens(r.Context(), p.UserID)
	if err != nil {
		return err
	}
	out := dto.List[dto.Token]{Items: make([]dto.Token, len(list))}
	for i, t := range list {
		out.Items[i] = toToken(t)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// createToken returns the secret once (TOK-01).
func (s *server) createToken(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.CreateTokenRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	tok, secret, err := s.Users.CreateToken(r.Context(), p.UserID, users.TokenInput{Kind: req.Kind, Name: req.Name, ExpiresAt: req.ExpiresAt})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, dto.CreatedToken{Token: toToken(tok), Secret: secret})
	return nil
}

func (s *server) revokeToken(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	if err := s.Users.RevokeToken(r.Context(), p.UserID, r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
