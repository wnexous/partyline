package api

import (
	"net/http"
	"time"

	"github.com/wnexous/partyline/internal/api/convert"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/users"
)

func (s *server) authRoutes(mux *http.ServeMux) {
	s.handle(mux, "GET /api/v1/setup/status", s.setupStatus)
	s.handle(mux, "POST /api/v1/setup", s.setup)
	s.handle(mux, "POST /api/v1/auth/login", s.login)
	s.handle(mux, "POST /api/v1/auth/logout", authed(s.logout))
	s.handle(mux, "GET /api/v1/me", authed(s.getMe))
	s.handle(mux, "PATCH /api/v1/me", authed(s.updateMe))
	s.handle(mux, "DELETE /api/v1/me", authed(s.deleteMe))
	s.handle(mux, "POST /api/v1/me/password", authed(s.changePassword))
	s.handle(mux, "GET /api/v1/me/sessions", authed(s.listSessions))
	s.handle(mux, "DELETE /api/v1/me/sessions/{id}", authed(s.deleteSession))
}

func msTime(v int64) time.Time { return convert.Time(v) }

func toMe(u db.User, displayName string) dto.Me {
	return dto.Me{
		ID: u.ID, Username: u.Username, DisplayName: displayName,
		Role: dto.Role(u.Role), Locale: u.Locale, CreatedAt: msTime(u.CreatedAt),
	}
}

// setSessionCookie stores a new session in the browser (AUTH-03).
func (s *server) setSessionCookie(w http.ResponseWriter, sess users.NewSession) {
	http.SetCookie(w, sessionCookie(sess.Secret, s.Config.SecureCookies()))
}

func (s *server) clearSessionCookie(w http.ResponseWriter) {
	// #nosec G124 -- see setSessionCookie.
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.Config.SecureCookies(),
	})
}

func (s *server) startSession(w http.ResponseWriter, r *http.Request, userID string) error {
	sess, err := s.Users.CreateSession(r.Context(), userID, httpx.ClientIP(r.Context()).String(), r.UserAgent())
	if err != nil {
		return err
	}
	s.setSessionCookie(w, sess)
	return nil
}

func (s *server) setupStatus(w http.ResponseWriter, r *http.Request) error {
	needs, err := s.Users.NeedsSetup(r.Context())
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dto.SetupStatus{NeedsSetup: needs})
	return nil
}

// setup creates the first admin with the logged setup token and logs it in (AUTH-01, AUTH-02).
func (s *server) setup(w http.ResponseWriter, r *http.Request) error {
	var req dto.SetupRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	u, err := s.Users.Setup(r.Context(), req.Token, users.AccountInput{
		Username: req.Username, Password: req.Password, DisplayName: req.DisplayName,
	})
	if err != nil {
		return err
	}
	if err := s.startSession(w, r, u.ID); err != nil {
		return err
	}
	return s.writeMe(w, r, http.StatusCreated, u)
}

func (s *server) login(w http.ResponseWriter, r *http.Request) error {
	var req dto.LoginRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	u, sess, err := s.Users.Login(r.Context(), users.LoginInput{
		Username: req.Username, Password: req.Password,
		IP: httpx.ClientIP(r.Context()).String(), UserAgent: r.UserAgent(),
	})
	if err != nil {
		return err
	}
	s.setSessionCookie(w, sess)
	return s.writeMe(w, r, http.StatusOK, u)
}

// logout revokes the current session (AUTH-09). Token callers have no session to end.
func (s *server) logout(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	if p.SessionID != "" {
		if err := s.Users.RevokeSession(r.Context(), p.UserID, p.SessionID); err != nil {
			return err
		}
	}
	s.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *server) getMe(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	u, err := s.Users.GetUser(r.Context(), p.UserID)
	if err != nil {
		return err
	}
	return s.writeMe(w, r, http.StatusOK, u)
}

func (s *server) updateMe(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.UpdateMeRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	u, err := s.Users.UpdateProfile(r.Context(), p.UserID, users.ProfileUpdate{DisplayName: req.DisplayName, Locale: req.Locale})
	if err != nil {
		return err
	}
	return s.writeMe(w, r, http.StatusOK, u)
}

// deleteMe deletes the caller's own account after checking their password (POS-06). Nobody deletes another
// account: there is no id in the path.
func (s *server) deleteMe(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.DeleteAccountRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	if err := s.Users.DeleteAccount(r.Context(), p.UserID, req.Password); err != nil {
		return err
	}
	s.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *server) changePassword(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.ChangePasswordRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	if err := s.Users.ChangePassword(r.Context(), p.UserID, req.CurrentPassword, req.NewPassword); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *server) listSessions(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	rows, err := s.Users.ListSessions(r.Context(), p.UserID)
	if err != nil {
		return err
	}
	out := dto.List[dto.Session]{Items: make([]dto.Session, len(rows))}
	for i, row := range rows {
		out.Items[i] = dto.Session{
			ID: row.ID, CreatedAt: msTime(row.CreatedAt), LastSeenAt: msTime(row.LastSeenAt),
			ExpiresAt: msTime(row.ExpiresAt), UserAgent: row.UserAgent, IP: row.Ip,
			Current: row.ID == p.SessionID,
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

func (s *server) deleteSession(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	if err := s.Users.RevokeSession(r.Context(), p.UserID, r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
