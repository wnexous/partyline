package api

import (
	"net/http"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/webpush"
)

// pushRoutes serves the Web Push routes, all of them for a logged-in user: an agent token is
// refused by the middleware and an anonymous caller by authed (PUSH-02, PUSH-15).
func (s *server) pushRoutes(mux *http.ServeMux) {
	s.handle(mux, "GET /api/v1/push/key", authed(s.pushKey))
	s.handle(mux, "POST /api/v1/push/subscriptions", authed(s.subscribePush))
	s.handle(mux, "DELETE /api/v1/push/subscriptions", authed(s.unsubscribePush))
	s.handle(mux, "POST /api/v1/push/test", authed(s.testPush))
}

// pushKey answers the instance's VAPID public key (PUSH-02).
func (s *server) pushKey(w http.ResponseWriter, _ *http.Request, _ authn.Principal) error {
	httpx.WriteJSON(w, http.StatusOK, dto.PushKey{PublicKey: s.Push.Key()})
	return nil
}

// subscribePush stores the caller's subscription; the same endpoint again updates its row
// (PUSH-03, PUSH-04).
func (s *server) subscribePush(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.PushSubscriptionRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	sub, err := s.Push.Subscribe(r.Context(), webpush.SubscribeInput{
		UserID: p.UserID, SessionID: p.SessionID,
		Endpoint: req.Endpoint, P256dh: req.P256dh, Auth: req.Auth,
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, toPushSubscription(sub))
	return nil
}

// unsubscribePush removes the caller's row for the endpoint in the body; another account's
// endpoint is 404 and stays (PUSH-05).
func (s *server) unsubscribePush(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.PushUnsubscribeRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	if err := s.Push.Unsubscribe(r.Context(), p.UserID, req.Endpoint); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// testPush queues one test notification per subscription of the caller and answers 202; the
// push itself happens in the worker (PUSH-15).
func (s *server) testPush(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	queued, err := s.Push.Test(r.Context(), p.UserID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusAccepted, dto.PushTest{Queued: queued})
	return nil
}

// toPushSubscription renders one stored row without its key material (PUSH-22).
func toPushSubscription(sub db.PushSubscription) dto.PushSubscription {
	out := dto.PushSubscription{ID: sub.ID, Endpoint: sub.Endpoint, CreatedAt: msTime(sub.CreatedAt)}
	if sub.LastOkAt.Valid {
		last := msTime(sub.LastOkAt.Int64)
		out.LastOkAt = &last
	}
	return out
}
