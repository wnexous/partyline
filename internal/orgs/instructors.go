package orgs

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/wnexous/partyline/internal/audit"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/validation"
)

// MaxInstructors is the most instructors one AI has besides its host: the welcome names them all.
const MaxInstructors = 20

// Instructor is one identity that instructs an AI besides its host (SR2-06).
type Instructor struct {
	db.Intelligence
	// CreatedBy is the identity that added the link; CreatedAt when.
	CreatedBy string
	CreatedAt time.Time
}

// Instructors returns the host of the AI and its instructors besides the host, oldest link first (SR2-10). The
// host and the AI itself read them; anyone else gets ErrForbidden, and an id that is no AI ErrNotFound. The
// caller decides beforehand whether the actor may see the AI at all.
func (s *Service) Instructors(ctx context.Context, actorIID, agentID string) (db.Intelligence, []Instructor, error) {
	var (
		host db.Intelligence
		list []Instructor
	)
	err := s.store.ReadTx(ctx, func(q *db.Queries) error {
		hostID, err := q.GetAgentHost(ctx, agentID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if actorIID == "" || (actorIID != hostID && actorIID != agentID) {
			return ErrForbidden
		}
		if host, err = q.GetIntelligence(ctx, hostID); err != nil {
			return err
		}
		list, err = ListInstructors(ctx, q, agentID)
		return err
	})
	return host, list, err
}

// ListInstructors reads the instructors of an AI, oldest link first, without authorization: the welcome of
// the AI and its profile use it. q may belong to a transaction.
func ListInstructors(ctx context.Context, q *db.Queries, agentID string) ([]Instructor, error) {
	rows, err := q.ListAgentInstructors(ctx, agentID)
	if err != nil {
		return nil, err
	}
	out := make([]Instructor, len(rows))
	for i, r := range rows {
		out[i] = Instructor{Intelligence: r.Intelligence, CreatedBy: r.CreatedBy, CreatedAt: time.UnixMilli(r.CreatedAt).UTC()}
	}
	return out, nil
}

// AddInstructor makes the identity named by @handle an instructor of the AI (SR2-10): its messages become the
// AI's trusted ones (SR2-07), and a user instructor also manages the AI (SR2-13). Only the host does it;
// anyone else, the AI itself included, gets ErrForbidden, and an id that is no AI ErrNotFound. The handle must
// be an enabled user or an AI the host can see (one it hosts or shares an organization with), else
// ErrContactNotFound; the AI itself and its host are validation errors on "handle". Adding one that is already
// there changes nothing. It returns the instructor.
func (s *Service) AddInstructor(ctx context.Context, actorIID, agentID, handle string) (Instructor, error) {
	var out Instructor
	var home string
	added := false
	err := s.store.WithTx(ctx, func(q *db.Queries) error {
		added = false
		host, err := s.requireHost(ctx, q, actorIID, agentID)
		if err != nil {
			return err
		}
		if home, err = homeOf(ctx, q, agentID); err != nil {
			return err
		}
		iid, err := s.instructorCandidate(ctx, q, host, handle)
		if err != nil {
			return err
		}
		switch iid {
		case agentID:
			return &validation.Error{Fields: map[string]string{"handle": "an AI does not instruct itself"}}
		case host:
			return &validation.Error{Fields: map[string]string{"handle": "the host already instructs this AI"}}
		}
		current, err := ListInstructors(ctx, q, agentID)
		if err != nil {
			return err
		}
		for _, in := range current {
			if in.ID == iid {
				out = in
				return nil
			}
		}
		if len(current) >= MaxInstructors {
			return &validation.Error{Fields: map[string]string{"handle": "an AI has at most 20 instructors besides its host"}}
		}
		now := s.now()
		if _, err := q.AddAgentInstructor(ctx, db.AddAgentInstructorParams{
			AgentIid: agentID, InstructorIid: iid, CreatedBy: actorIID, CreatedAt: now.UnixMilli(),
		}); err != nil {
			return err
		}
		i, err := q.GetIntelligence(ctx, iid)
		if err != nil {
			return err
		}
		out, added = Instructor{Intelligence: i, CreatedBy: actorIID, CreatedAt: time.UnixMilli(now.UnixMilli()).UTC()}, true
		return s.audit.Record(ctx, q, audit.Entry{
			ActorID: actorIID, Action: audit.ActionAgentInstructorAdd, TargetKind: audit.TargetAgent, TargetID: agentID,
			Detail: map[string]any{"instructor": iid},
		})
	})
	if err != nil {
		return Instructor{}, err
	}
	if added {
		s.linksChanged(agentID, home)
	}
	return out, nil
}

// RemoveInstructor takes an instructor off the AI (SR2-10), with the rules of AddInstructor: only the host,
// else ErrForbidden; an id that is no AI ErrNotFound. Removing one that is not there changes nothing.
func (s *Service) RemoveInstructor(ctx context.Context, actorIID, agentID, instructorIID string) error {
	var home string
	removed := false
	err := s.store.WithTx(ctx, func(q *db.Queries) error {
		removed = false
		if _, err := s.requireHost(ctx, q, actorIID, agentID); err != nil {
			return err
		}
		var err error
		if home, err = homeOf(ctx, q, agentID); err != nil {
			return err
		}
		n, err := q.RemoveAgentInstructor(ctx, db.RemoveAgentInstructorParams{AgentIid: agentID, InstructorIid: instructorIID})
		if err != nil || n == 0 {
			return err
		}
		removed = true
		return s.audit.Record(ctx, q, audit.Entry{
			ActorID: actorIID, Action: audit.ActionAgentInstructorRemove, TargetKind: audit.TargetAgent, TargetID: agentID,
			Detail: map[string]any{"instructor": instructorIID},
		})
	})
	if err == nil && removed {
		s.linksChanged(agentID, home)
	}
	return err
}

// requireHost is the host of the AI when actor is it: ErrNotFound for an id that is no AI, ErrForbidden for
// anyone else, the AI itself included (SR2-10).
func (s *Service) requireHost(ctx context.Context, q *db.Queries, actorIID, agentID string) (string, error) {
	host, err := q.GetAgentHost(ctx, agentID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if actorIID == "" || actorIID != host {
		return "", ErrForbidden
	}
	return host, nil
}

// instructorCandidate resolves the @handle of a new instructor: an enabled user, or an AI the host sees - one
// it hosts or one it shares an organization with. Anything else, a person included, is ErrContactNotFound.
func (s *Service) instructorCandidate(ctx context.Context, q *db.Queries, host, handle string) (string, error) {
	iid, err := s.invitee(ctx, q, handle)
	if err != nil {
		return "", err
	}
	aiHost, err := q.GetAgentHost(ctx, iid)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return iid, nil // a user
	case err != nil:
		return "", err
	case aiHost == host:
		return iid, nil
	}
	n, err := q.CountSharedOrgs(ctx, db.CountSharedOrgsParams{FirstIid: host, SecondIid: iid})
	if err != nil {
		return "", err
	}
	if n == 0 {
		return "", ErrContactNotFound
	}
	return iid, nil
}

// linksChanged announces a change of who instructs an AI after the commit: its live sessions read their
// welcome again (SR2-11, SI-09) and its profile is read again.
func (s *Service) linksChanged(agentID, home string) {
	if s.bus == nil {
		return
	}
	s.bus.Publish(events.IntelligenceChanged{ID: agentID})
	if home != "" {
		s.bus.Publish(events.OrgChanged{OrgID: home})
	}
}
