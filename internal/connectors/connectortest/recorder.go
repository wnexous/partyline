package connectortest

import (
	"context"
	"testing"

	"github.com/wnexous/partyline/internal/connectors"
)

// Migration is a chat migration reported to a Recorder.
type Migration struct{ OldID, NewID string }

// Recorder is a connectors.Handler for platform adapter tests: it records what a running
// connector reports, without a manager or a database.
type Recorder struct {
	inbound    chan connectors.Inbound
	statuses   chan connectors.Status
	details    chan string
	migrations chan Migration
	accounts   chan string
}

// NewRecorder returns an empty Recorder.
func NewRecorder() *Recorder {
	return &Recorder{
		inbound: make(chan connectors.Inbound, 64), statuses: make(chan connectors.Status, 64), details: make(chan string, 64),
		migrations: make(chan Migration, 64), accounts: make(chan string, 64),
	}
}

var _ connectors.Handler = (*Recorder)(nil)

// Inbound implements connectors.Handler.
func (r *Recorder) Inbound(_ context.Context, m connectors.Inbound) { r.inbound <- m }

// Status implements connectors.Handler.
func (r *Recorder) Status(s connectors.Status, detail string) {
	r.statuses <- s
	r.details <- detail
}

// MigrateChat implements connectors.Handler.
func (r *Recorder) MigrateChat(_ context.Context, oldID, newID string) {
	r.migrations <- Migration{OldID: oldID, NewID: newID}
}

// Account implements connectors.Handler.
func (r *Recorder) Account(label string) { r.accounts <- label }

// NextAccount waits for the next reported account label.
func (r *Recorder) NextAccount(t testing.TB) string {
	t.Helper()
	return next(t, r.accounts, "account label")
}

// NoAccount reports whether no account label is waiting.
func (r *Recorder) NoAccount() bool { return len(r.accounts) == 0 }

// NextInbound waits for the next inbound message.
func (r *Recorder) NextInbound(t testing.TB) connectors.Inbound {
	t.Helper()
	return next(t, r.inbound, "inbound message")
}

// NextStatusDetail waits for the detail text of the next reported status (read alongside
// NextStatus, one per status).
func (r *Recorder) NextStatusDetail(t testing.TB) string {
	t.Helper()
	return next(t, r.details, "status detail")
}

// NextStatus waits for the next reported status.
func (r *Recorder) NextStatus(t testing.TB) connectors.Status {
	t.Helper()
	return next(t, r.statuses, "status")
}

// NextMigration waits for the next chat migration.
func (r *Recorder) NextMigration(t testing.TB) Migration {
	t.Helper()
	return next(t, r.migrations, "chat migration")
}

// NoInbound reports whether no inbound message is waiting.
func (r *Recorder) NoInbound() bool { return len(r.inbound) == 0 }

func next[T any](t testing.TB, ch chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-afterTimeout():
		t.Fatalf("no %s", what)
		var zero T
		return zero
	}
}
