package channel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/channel/mcp"
)

// registerSchedules adds the tools of the AI's scheduled routines (HB-01, HB-02): list_schedules,
// set_schedule and delete_schedule over GET, POST, PATCH and DELETE /agent/schedules.
func (t *tools) registerSchedules(srv *mcp.Server) {
	srv.HandleTool(mcp.ToolDef{
		Name:        "list_schedules",
		Description: "List your scheduled routines (heartbeats): each has a name, a prompt that reaches you as a turn when it is due, a rule (every_minutes, or a 5-field cron in tz), whether it is enabled, its next run (next_run_at, in its own timezone), its last run and status, and who set it (origin self: you; manager: someone who manages you, which you may read but not change; can_edit says which you may change).",
		InputSchema: schema(`{"type":"object","properties":{}}`),
	}, t.guard(t.listSchedules))
	srv.HandleTool(mcp.ToolDef{
		Name:        "set_schedule",
		Description: "Create a scheduled routine of your own, or change one you set (give its id): its prompt reaches you as a turn on the rule. Give exactly one rule: every_minutes (at least 15) or cron (a standard 5-field cron such as \"0 9 * * 1-5\", evaluated in tz, America/Sao_Paulo by default). Your routines together run at most 48 times in 24 hours and you have at most 20; at most 20 changes an hour. catch_up once (default) runs a routine missed while you were offline once when you are back, within the shorter of its period and 12 hours; skip drops it. Before a routine of yours is active your nearest manager is told what it says, and may pause it: if nobody manages you, or they cannot be told, nothing changes (no_manager, or try again later). A routine a manager paused is enabled again only by them. Routines set by someone who manages you are not yours to change.",
		InputSchema: schema(`{"type":"object","properties":{"id":{"type":"string","description":"the id of a routine you set, to change it; leave out to create one"},"name":{"type":"string","description":"1 to 60 characters"},"prompt":{"type":"string","description":"what you are asked to do when it runs, 1 to 4000 characters"},"every_minutes":{"type":"integer","minimum":15,"description":"run every this many minutes (instead of cron)"},"cron":{"type":"string","description":"a 5-field cron: minute hour day-of-month month day-of-week (instead of every_minutes)"},"tz":{"type":"string","description":"IANA timezone of the cron, default America/Sao_Paulo"},"enabled":{"type":"boolean","description":"default true"},"catch_up":{"type":"string","enum":["once","skip"],"description":"default once"}}}`),
	}, t.guard(t.setSchedule))
	srv.HandleTool(mcp.ToolDef{
		Name:        "delete_schedule",
		Description: "Delete a scheduled routine you set, by its id, with its run history.",
		InputSchema: schema(`{"type":"object","properties":{"id":{"type":"string","description":"the id of the routine"}},"required":["id"]}`),
	}, t.guard(t.deleteSchedule))
}

// scheduleListing is one routine as the tools show it.
type scheduleListing struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Prompt       string `json:"prompt"`
	EveryMinutes *int   `json:"every_minutes,omitempty"`
	Cron         string `json:"cron,omitempty"`
	Timezone     string `json:"tz"`
	CatchUp      string `json:"catch_up"`
	Enabled      bool   `json:"enabled"`
	Origin       string `json:"origin"`
	SetBy        string `json:"set_by,omitempty"`
	CanEdit      bool   `json:"can_edit"`
	// PausedByManager: someone who manages you paused it; only they enable it again.
	PausedByManager bool   `json:"paused_by_manager,omitempty"`
	NextRunAt       string `json:"next_run_at,omitempty"`
	LastRunAt       string `json:"last_run_at,omitempty"`
	LastStatus      string `json:"last_status,omitempty"`
}

func listingOf(s dto.Schedule) scheduleListing {
	out := scheduleListing{
		ID: s.ID, Name: s.Name, Prompt: s.Prompt, EveryMinutes: s.EveryMinutes, Cron: s.Cron, Timezone: s.Timezone,
		CatchUp: string(s.CatchUp), Enabled: s.Enabled, Origin: string(s.Origin), CanEdit: s.CanEdit,
		LastStatus: string(s.LastStatus), PausedByManager: s.PausedByManager,
	}
	if s.Origin == dto.ScheduleOriginManager && s.SetBy.Handle != "" {
		out.SetBy = "@" + s.SetBy.Handle
	}
	if s.NextRunAt != nil {
		out.NextRunAt = s.NextRunAt.Format("2006-01-02T15:04:05Z07:00")
	}
	if s.LastRunAt != nil {
		out.LastRunAt = s.LastRunAt.Format("2006-01-02T15:04:05Z07:00")
	}
	return out
}

func (t *tools) listSchedules(ctx context.Context, _ json.RawMessage) (string, error) {
	var list dto.ScheduleList
	if err := t.api.json(ctx, http.MethodGet, "agent/schedules", nil, nil, &list); err != nil {
		return "", err
	}
	out := struct {
		Schedules []scheduleListing `json:"schedules"`
	}{Schedules: make([]scheduleListing, 0, len(list.Items))}
	for _, s := range list.Items {
		out.Schedules = append(out.Schedules, listingOf(s))
	}
	return marshal(out)
}

func (t *tools) setSchedule(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		ID           string  `json:"id"`
		Name         *string `json:"name"`
		Prompt       *string `json:"prompt"`
		EveryMinutes *int    `json:"every_minutes"`
		Cron         *string `json:"cron"`
		Timezone     *string `json:"tz"`
		Enabled      *bool   `json:"enabled"`
		CatchUp      *string `json:"catch_up"`
	}
	if err := decode(args, &a); err != nil {
		return "", err
	}
	req := dto.ScheduleRequest{Name: a.Name, Prompt: a.Prompt, EveryMinutes: a.EveryMinutes, Cron: a.Cron, Timezone: a.Timezone, Enabled: a.Enabled, CatchUp: a.CatchUp}
	var out dto.Schedule
	if id := strings.TrimSpace(a.ID); id != "" {
		if err := t.api.json(ctx, http.MethodPatch, "agent/schedules/"+url.PathEscape(id), nil, req, &out); err != nil {
			return "", err
		}
	} else if err := t.api.json(ctx, http.MethodPost, "agent/schedules", nil, req, &out); err != nil {
		return "", err
	}
	return marshal(listingOf(out))
}

func (t *tools) deleteSchedule(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		ID string `json:"id"`
	}
	if err := decode(args, &a); err != nil {
		return "", err
	}
	if err := required("id", a.ID); err != nil {
		return "", err
	}
	if err := t.api.json(ctx, http.MethodDelete, "agent/schedules/"+url.PathEscape(strings.TrimSpace(a.ID)), nil, nil, nil); err != nil {
		return "", err
	}
	return "deleted", nil
}
