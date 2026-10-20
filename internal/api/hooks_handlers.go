package api

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/attachments"
	"github.com/wnexous/partyline/internal/audit"
	"github.com/wnexous/partyline/internal/connectors"
)

// HookSource finds running connectors that receive messages over HTTP; the connectors'
// Manager implements it.
type HookSource interface {
	Hook(connectorID string) (connectors.HookReceiver, bool)
}

// Header names of signed hook requests (HOOK-01).
const (
	hookTimestampHeader = "X-Partyline-Timestamp"
	hookSignatureHeader = "X-Partyline-Signature"
)

// hookRoutes are the unauthenticated webhook endpoints: requests prove themselves with the
// connector's signature, links with their sealed token.
func (s *server) hookRoutes(mux *http.ServeMux) {
	s.handle(mux, "POST /api/v1/hooks/{id}", s.receiveHook)
	s.handle(mux, "GET "+connectors.FileLinkPath+"{token}", s.downloadHookFile)
}

var errHookNotFound = httpx.NewError(http.StatusNotFound, dto.ErrorCodeNotFound, "not found")

// receiveHook accepts a signed message for a running webhook connector (HOOK-01): 202 when
// accepted, 401 for a bad signature or timestamp with nothing stored (HOOK-02).
func (s *server) receiveHook(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	if s.Hooks == nil {
		return errHookNotFound
	}
	hook, ok := s.Hooks.Hook(id)
	if !ok {
		return errHookNotFound
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, connectors.MaxHookBody))
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return httpx.NewError(http.StatusRequestEntityTooLarge, dto.ErrorCodeTooLarge, "request body too large")
	}
	if err != nil {
		return httpx.NewError(http.StatusBadRequest, dto.ErrorCodeInvalid, "unreadable body")
	}
	err = hook.ReceiveHook(r.Context(), connectors.HookRequest{
		Timestamp: r.Header.Get(hookTimestampHeader), Signature: r.Header.Get(hookSignatureHeader), Body: body,
	})
	switch {
	case errors.Is(err, connectors.ErrBadSignature):
		if aerr := s.Audit.Append(r.Context(), audit.Entry{
			Action: audit.ActionWebhookRejected, TargetKind: audit.TargetConnector, TargetID: id,
		}); aerr != nil {
			s.Logger.ErrorContext(r.Context(), "audit webhook rejection", "err", aerr)
		}
		return httpx.NewError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized, "invalid signature or timestamp")
	case errors.Is(err, connectors.ErrRateLimited):
		return httpx.NewError(http.StatusTooManyRequests, dto.ErrorCodeRateLimited, "too many requests")
	case errors.Is(err, connectors.ErrNotRunning):
		return errHookNotFound
	case err != nil:
		return err
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
	return nil
}

// downloadHookFile serves an attachment through a signed, expiring link; forged, expired
// or unknown links get 404.
func (s *server) downloadHookFile(w http.ResponseWriter, r *http.Request) error {
	if s.FileLinks == nil {
		return errHookNotFound
	}
	a, err := s.FileLinks.Resolve(r.Context(), r.PathValue("token"))
	if errors.Is(err, connectors.ErrLinkInvalid) {
		return errHookNotFound
	}
	if err != nil {
		return err
	}
	f, err := s.Blobs.Open(a.Sha256)
	if errors.Is(err, attachments.ErrNotFound) {
		return errHookNotFound
	}
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": a.Name}))
	http.ServeContent(w, r, "", time.Time{}, f)
	return nil
}
