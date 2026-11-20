package channel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
)

// SI-09, SI-10: the notice says the other organizations the AI belongs to (with a permission other than member),
// who created it and the AIs it created, after who manages it and before what it operates. A creator that is the
// AI's host, who starts most AIs, or the AI itself (a row that names itself) is not mentioned.
func TestIdentityNoticeSaysCreatorCreatedAndMemberships(t *testing.T) {
	w := worker1()
	w.Memberships = []dto.AgentMembership{
		{Organization: dto.ContactOrg{ID: "org-2", Name: "Globex", Slug: "globex"}, Permission: "member"},
		{Organization: dto.ContactOrg{ID: "org-3", Name: "Initech", Slug: "initech"}, Permission: "member"},
		{Organization: dto.ContactOrg{ID: "org-4", Name: "Umbrella", Slug: "umbrella"}, Permission: "admin"},
	}
	w.Creator = &dto.IntelligenceRef{ID: "ai-lead", Kind: dto.IntelligenceKindAI, DisplayName: "Lead", Handle: "lead"}
	w.Created = []dto.IntelligenceRef{
		{ID: "ai-2", Kind: dto.IntelligenceKindAI, DisplayName: "Worker 2", Handle: "w2"},
		{ID: "ai-3", Kind: dto.IntelligenceKindAI, DisplayName: "Worker 3", Handle: "w3"},
	}
	w.Connections = []dto.AgentConnection{{ID: "01HCONN2", Platform: "telegram"}}
	require.Equal(t, wantWorker1+
		` You also belong to Globex (globex), Initech (initech), Umbrella (umbrella) as an admin.`+
		` You were created by Lead (@lead). You created: Worker 2 (@w2), Worker 3 (@w3).`+
		` You operate: Telegram (connection 01HCONN2).`, identityText(w))

	w.Memberships, w.Created, w.Connections = nil, []dto.IntelligenceRef{}, nil
	plain := wantWorker1
	w.Creator = &dto.IntelligenceRef{ID: "ai-1", DisplayName: "Worker 1", Handle: "w1"}
	require.Equal(t, plain, identityText(w), "nothing to say, nothing said")
	host := w.Owner
	w.Creator = &host
	require.Equal(t, plain, identityText(w), "the host started it: nothing worth saying")
}

// welcomesServer is an agent endpoint that sends the welcomes, one after the other, on the one connection it
// accepts, and then keeps it open.
func welcomesServer(t *testing.T, welcomes ...dto.AgentWelcomeFrame) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = ws.CloseNow() }()
		for _, f := range welcomes {
			data, _ := json.Marshal(f)
			if ws.Write(r.Context(), websocket.MessageText, data) != nil {
				return
			}
		}
		for {
			if _, _, err := ws.Read(r.Context()); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// identityNotices runs a link against the server and returns the identity notices it sent once no more come.
func identityNotices(t *testing.T, base string, setup func(l *link)) []string {
	t.Helper()
	notes := &noticeLog{}
	l := newLink(mustURL(t, base), "pl_agent_x", builderIdentity, http.DefaultClient, notes, &syncBuffer{}, fastTiming)
	l.announce = true
	if setup != nil {
		setup(l)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = l.run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	time.Sleep(300 * time.Millisecond)
	var out []string
	for _, n := range notes.all() {
		if n.Meta["reason"] == reasonIdentity {
			out = append(out, n.Content)
		}
	}
	return out
}

// SI-09, SR2-11: a fresh welcome on the same connection announces the identity again only when its text differs
// from the last one announced (a new instructor does); a channel that became the updated program announces only a
// change of what it was told.
func TestAFreshWelcomeAnnouncesOnlyAChangedIdentity(t *testing.T) {
	first := worker1()
	same := worker1()
	same.Session.ConnectedAt = time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC) // not part of the notice
	promoted := worker1()
	promoted.Instructors = append(append([]dto.IntelligenceRef{}, promoted.Instructors...), dto.IntelligenceRef{ID: "id-dora", DisplayName: "Dora", Handle: "dora"})
	withChild := promoted
	withChild.Created = []dto.IntelligenceRef{{ID: "ai-2", DisplayName: "Worker 2", Handle: "w2"}}

	got := identityNotices(t, welcomesServer(t, first, same, promoted, promoted, withChild), nil)
	require.Equal(t, []string{identityText(first), identityText(promoted), identityText(withChild)}, got)
	require.Contains(t, got[1], "Alice (@alice) and Dora (@dora) are marked trusted")
	require.Contains(t, got[2], "You created: Worker 2 (@w2).")

	got = identityNotices(t, welcomesServer(t, first, same, promoted), func(l *link) { l.resumed = true })
	require.Equal(t, []string{identityText(promoted)}, got, "a resumed channel was told the first one already")
}
