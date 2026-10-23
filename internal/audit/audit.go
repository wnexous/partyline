// Package audit records security events (logins, invites, tokens, grants, account changes,
// connectors) and lists them for admins (AUD-01, AUD-02). Entries never hold secrets.
package audit

import (
	"context"
	"encoding/json"
	"time"

	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

// Actions recorded in the log.
const (
	ActionSetup          = "auth.setup"
	ActionLogin          = "auth.login"
	ActionLoginFailed    = "auth.login_failed"
	ActionLogout         = "auth.logout"
	ActionSessionRevoke  = "session.revoke"
	ActionPasswordChange = "user.password_change"
	ActionPasswordReset  = "user.password_reset"
	ActionUserCreate     = "user.create"
	ActionUserUpdate     = "user.update"
	ActionInviteCreate   = "invite.create"
	ActionInviteRevoke   = "invite.revoke"
	ActionInviteAccept   = "invite.accept"
	ActionTokenCreate    = "token.create"
	ActionTokenRevoke    = "token.revoke"
	ActionTokenPurge     = "token.purge"
	ActionGrantsSet      = "grants.set"
	// Connector changes (AUD-01); sharing changes are grants.set.
	ActionConnectorCreate  = "connector.create"
	ActionConnectorUpdate  = "connector.update"
	ActionConnectorDelete  = "connector.delete"
	ActionConnectorEnable  = "connector.enable"
	ActionConnectorDisable = "connector.disable"
	// ActionConnectorOperator records a change of the intelligence that operates a connector's
	// platform account (ACC-01).
	ActionConnectorOperator = "connector.operator"
	// ActionConnectorIdentitiesReset records the identities deleted because an endpoint
	// field of the connector changed (S5).
	ActionConnectorIdentitiesReset = "connector.identities_reset"
	// ActionWebhookRejected is a hook request refused for its signature or timestamp.
	ActionWebhookRejected = "webhook.rejected"
	// ActionAgentAdopt records a v0.3 identity linked to the folder of its first session
	// (MIG-02).
	ActionAgentAdopt = "agent.adopt"
	// ActionAgentMerge records an AI identity merged into another of the same owner (MRG-01).
	ActionAgentMerge = "agent.merge"
	// Organizations (organizations feature): the organization itself, its org chart and its members.
	ActionOrgCreate = "org.create"
	ActionOrgUpdate = "org.update"
	ActionOrgDelete = "org.delete"
	// The org chart: positions added, renamed or moved, and deleted (POS-01, POS-09).
	ActionOrgPositionCreate = "org.position_create"
	ActionOrgPositionUpdate = "org.position_update"
	ActionOrgPositionDelete = "org.position_delete"
	// Membership: joined (accepted invitation, started AI), changed, left and removed (OMEM-01..06).
	ActionOrgMemberJoin       = "org.member_join"
	ActionOrgMemberPermission = "org.member_permission"
	ActionOrgMemberPosition   = "org.member_position"
	ActionOrgMemberLeave      = "org.member_leave"
	ActionOrgMemberRemove     = "org.member_remove"
)

// Target kinds.
const (
	TargetUser      = "user"
	TargetSession   = "session"
	TargetInvite    = "invite"
	TargetToken     = "token"
	TargetConnector = "connector"
	TargetAgent     = "agent"
	// TargetOrganization is the organization a change of the org model is about.
	TargetOrganization = "organization"
)

// PageSize is the number of entries per page (AUD-02).
const PageSize = 50

// Entry is one event. ActorID and IP default to the request's principal and client address.
type Entry struct {
	ActorID    string
	Action     string
	TargetKind string
	TargetID   string
	// Detail holds non-secret context (names, roles, levels); never passwords or token secrets.
	Detail map[string]any
	IP     string
}

// Log appends and lists audit entries.
type Log struct {
	store *store.Store
	now   func() time.Time
}

// New returns a log over st; now defaults to time.Now.
func New(st *store.Store, now func() time.Time) *Log {
	if now == nil {
		now = time.Now
	}
	return &Log{store: st, now: now}
}

// Record appends e inside the caller's write transaction, so the entry commits with the change.
func (l *Log) Record(ctx context.Context, q *db.Queries, e Entry) error {
	if e.ActorID == "" {
		if p, ok := authn.PrincipalFrom(ctx); ok {
			e.ActorID = p.UserID
		}
	}
	if e.IP == "" {
		if ip := httpx.ClientIP(ctx); ip.IsValid() {
			e.IP = ip.String()
		}
	}
	detail := []byte("{}")
	if len(e.Detail) > 0 {
		var err error
		if detail, err = json.Marshal(e.Detail); err != nil {
			return err
		}
	}
	return q.CreateAuditEntry(ctx, db.CreateAuditEntryParams{
		ID: store.NewID(), ActorID: e.ActorID, Action: e.Action, TargetKind: e.TargetKind,
		TargetID: e.TargetID, DetailJson: string(detail), Ip: e.IP, CreatedAt: l.now().UnixMilli(),
	})
}

// Append writes e in its own transaction, for events without a write of their own (failed logins).
func (l *Log) Append(ctx context.Context, e Entry) error {
	return l.store.WithTx(ctx, func(q *db.Queries) error { return l.Record(ctx, q, e) })
}

// List returns a page of entries newest first, starting after the entry id before ("" for
// the newest), and whether older entries exist.
func (l *Log) List(ctx context.Context, before string) ([]db.ListAuditEntriesRow, bool, error) {
	if before == "" {
		before = "~" // sorts after every UUID
	}
	rows, err := l.store.R().ListAuditEntries(ctx, db.ListAuditEntriesParams{Before: before, Limit: PageSize + 1})
	if err != nil {
		return nil, false, err
	}
	if len(rows) > PageSize {
		return rows[:PageSize], true, nil
	}
	return rows, false, nil
}
