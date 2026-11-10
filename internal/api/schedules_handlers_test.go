package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

// Auth matrix of the schedule routes (HB-01, HB-02, HB-12) over the organization roles: whoever manages the AI
// reads and changes its schedules, whoever only sees it is refused (and its list shows only how many there
// are), whoever cannot see it does not learn it exists.
func TestScheduleRoutesAuthMatrix(t *testing.T) {
	env := testkit.New(t)
	roles := env.OrgRoles("acme")
	base := "/api/v1/agents/" + roles.TargetAI + "/schedules"
	name, prompt, cron, off := "summary", "send the summary", "0 9 * * *", false
	setup := func(t *testing.T) map[string]string {
		// A fresh schedule per case, and never more than one: the limits of HB-03 stay out of the way.
		ctx := context.Background()
		rows, err := env.App.Store.R().ListSchedulesByAgent(ctx, roles.TargetAI)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			roles.Manager.Do(http.MethodDelete, base+"/"+r.ID, nil).RequireStatus(http.StatusNoContent)
		}
		s := testkit.Decode[dto.Schedule](roles.Manager.Post(base, dto.ScheduleRequest{Name: &name, Prompt: &prompt, Cron: &cron, Enabled: &off}).RequireStatus(http.StatusCreated))
		return map[string]string{"schedule": s.ID}
	}
	one := func(res map[string]string, suffix string) string { return base + "/" + res["schedule"] + suffix }
	runRoleRoutes(t, roles, setup, []roleRoute{
		{"list schedules", func(_ *testing.T, c *testkit.Client, _ map[string]string) *testkit.Response {
			return c.Get(base)
		}, agentWant(200, 200, 404)},
		{"create schedule", func(_ *testing.T, c *testkit.Client, _ map[string]string) *testkit.Response {
			other := "other"
			return c.Post(base, dto.ScheduleRequest{Name: &other, Prompt: &prompt, Cron: &cron, Enabled: &off})
		}, agentWant(201, 403, 404)},
		{"get schedule", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Get(one(res, ""))
		}, agentWant(200, 403, 404)},
		{"update schedule", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Patch(one(res, ""), dto.ScheduleRequest{Prompt: &prompt})
		}, agentWant(200, 403, 404)},
		{"delete schedule", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Do(http.MethodDelete, one(res, ""), nil)
		}, agentWant(204, 403, 404)},
		{"run schedule now", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Post(one(res, "/run"), nil)
		}, agentWant(202, 403, 404)},
		{"list runs", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Get(one(res, "/runs"))
		}, agentWant(200, 403, 404)},
	})

	// The AI's own routes act on the AI itself, never on another one; an agent token is no manager, and
	// anonymous callers are 401.
	ai := roles.Manager.Token(dto.TokenKindAgent, "tools")
	ai.Get(base).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	ai.AsAgent("acme-ai").Get("/api/v1/agent/schedules").RequireStatus(http.StatusOK)
	ai.Get("/api/v1/agent/schedules").RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeFolderRequired)
	env.Anon().Get(base).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	env.Anon().Get("/api/v1/agent/schedules").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
}
