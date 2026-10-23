package api

import (
	"net/http"
	"strings"

	"github.com/wnexous/partyline/internal/api/convert"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/connectors"
)

// connectorRoutes are the connector management endpoints (CON-01..05).
func (s *server) connectorRoutes(mux *http.ServeMux) {
	s.handle(mux, "GET /api/v1/connector-kinds", authed(s.listConnectorKinds))
	s.handle(mux, "GET /api/v1/connectors", authed(s.listConnectors))
	s.handle(mux, "POST /api/v1/connectors", authed(s.createConnector))
	s.handle(mux, "GET /api/v1/connectors/{id}", authed(s.getConnector))
	s.handle(mux, "PATCH /api/v1/connectors/{id}", authed(s.updateConnector))
	s.handle(mux, "DELETE /api/v1/connectors/{id}", authed(s.deleteConnector))
	s.handle(mux, "GET /api/v1/connectors/{id}/sharing", authed(s.getConnectorSharing))
	s.handle(mux, "PUT /api/v1/connectors/{id}/sharing", authed(s.putConnectorSharing))
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
		ID: v.ID, Kind: v.Kind, Name: v.Name,
		Owner:   dto.User{ID: v.OwnerID, Username: v.OwnerUsername, DisplayName: v.OwnerDisplayName},
		MyLevel: v.Level, Enabled: v.Enabled, Status: dto.ConnectorStatus(v.Status), LastError: v.LastError,
		AccountLabel: v.AccountLabel,
		Config:       v.Config, AutoRouteScope: dto.AutoRouteScope(v.AutoRouteScope), AutoRouteRoomID: v.AutoRouteRoomID.String,
		CreatedAt: convert.Time(v.CreatedAt), UpdatedAt: convert.Time(v.UpdatedAt),
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
		Kind: req.Kind, Name: req.Name, Config: req.Config, Enabled: req.Enabled,
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
		Name: req.Name, Config: req.Config, Enabled: req.Enabled,
		AutoRouteScope: req.AutoRouteScope, AutoRouteRoomID: req.AutoRouteRoomID, OperatorID: req.OperatorID,
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

func (s *server) getConnectorSharing(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	v, err := s.Connectors.Sharing(r.Context(), p, r.PathValue("id"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, toSharing(v))
	return nil
}

func (s *server) putConnectorSharing(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.Sharing
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	v, err := s.Connectors.SetSharing(r.Context(), p, r.PathValue("id"), visibilityFrom(req))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, toSharing(v))
	return nil
}
