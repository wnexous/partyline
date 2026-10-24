package api

import (
	"net/http"

	"github.com/wnexous/partyline/internal/api/convert"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
)

// orgChartRoutes serve the org chart page (CHART-01..03).
func (s *server) orgChartRoutes(mux *http.ServeMux) {
	s.handle(mux, "GET /api/v1/orgs/{org}/chart", authed(s.getOrgChart))
}

// getOrgChart returns the organization's positions as a tree with their holders, the members with no position
// under whom they hang, and for each member what the viewer may do to it: delete it (POS-05) with the effect
// that follows ownership (POS-06), and move it (POS-04). Every member sees it, guests included; whoever is not
// a member does not find the organization (CHART-02).
func (s *server) getOrgChart(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	org, err := s.resolveOrg(r)
	if err != nil {
		return err
	}
	view, err := s.Orgs.ChartView(r.Context(), p.IID(), org.ID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, convert.OrgChart(view, s.Agents.Online))
	return nil
}
