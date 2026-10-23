package orgs

import (
	"context"
	"database/sql"
	"errors"
	"unicode/utf8"

	"github.com/wnexous/partyline/internal/audit"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/validation"
)

// MaxPositionNameLength is the longest position name in code points (POS-01).
const MaxPositionNameLength = 60

// Errors of the org chart.
var (
	// ErrCycle means a parent change would make a position its own ancestor (409 position_cycle,
	// POS-02).
	ErrCycle = errors.New("a position cannot be placed below itself or one of its descendants")
	// ErrPositionNameTaken means another position of the organization has the name, ignoring case
	// (409 conflict).
	ErrPositionNameTaken = errors.New("position name already taken")
)

// PositionUpdate holds the changes to a position; nil fields are unchanged. A ParentID that points
// at "" makes the position a root.
type PositionUpdate struct {
	Name     *string
	ParentID *string
}

func validPositionName(name string) (string, error) {
	name = cleanName(name)
	if n := utf8.RuneCountInString(name); n < 1 || n > MaxPositionNameLength {
		return "", &validation.Error{Fields: map[string]string{"name": "must have 1 to 60 characters"}}
	}
	return name, nil
}

// parentOfOrg is a validation error on parent_id unless parentID names a position of the organization.
func requirePositionIn(ctx context.Context, q *db.Queries, orgID, positionID string) error {
	_, err := q.GetPosition(ctx, db.GetPositionParams{ID: positionID, OrgID: orgID})
	if errors.Is(err, sql.ErrNoRows) {
		return &validation.Error{Fields: map[string]string{"parent_id": "must be a position of this organization"}}
	}
	return err
}

// nameFree is ErrPositionNameTaken when a position other than selfID has the name.
func nameFree(ctx context.Context, q *db.Queries, orgID, name, selfID string) error {
	other, err := q.GetPositionByName(ctx, db.GetPositionByNameParams{OrgID: orgID, Name: name})
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if other.ID != selfID {
		return ErrPositionNameTaken
	}
	return nil
}

func position(p db.Position) Position {
	return Position{ID: p.ID, Name: p.Name, ParentID: p.ParentID.String}
}

// CreatePosition adds a position to the organization's chart under parentID ("" for a root); only an
// admin does (OMEM-04). The name has 1-60 characters and is unique in the organization, ignoring
// case (ErrPositionNameTaken); a parent that is not a position of the organization is a validation
// error on parent_id (POS-01).
func (s *Service) CreatePosition(ctx context.Context, actorIID, orgID, name, parentID string) (Position, error) {
	if err := s.z.RequireAdmin(ctx, orgID, actorIID); err != nil {
		return Position{}, err
	}
	name, err := validPositionName(name)
	if err != nil {
		return Position{}, err
	}
	out := Position{ID: store.NewID(), Name: name, ParentID: parentID}
	err = s.store.WithTx(ctx, func(q *db.Queries) error {
		if err := requireAdmin(ctx, q, orgID, actorIID); err != nil {
			return err
		}
		if parentID != "" {
			if err := requirePositionIn(ctx, q, orgID, parentID); err != nil {
				return err
			}
		}
		if err := nameFree(ctx, q, orgID, name, ""); err != nil {
			return err
		}
		if err := q.CreatePosition(ctx, db.CreatePositionParams{
			ID: out.ID, OrgID: orgID, Name: name, ParentID: nullable(parentID), CreatedAt: s.now().UnixMilli(),
		}); err != nil {
			return err
		}
		return s.record(ctx, q, actorIID, audit.ActionOrgPositionCreate, orgID,
			map[string]any{"position_id": out.ID, "name": name, "parent_id": parentID})
	})
	if err != nil {
		return Position{}, err
	}
	s.changed(orgID)
	return out, nil
}

// UpdatePosition renames a position or moves it under another; only an admin does. A move that would
// close a loop is ErrCycle: the chart is read in the transaction that moves, so two moves that are
// fine alone cannot both commit (POS-02). A change that alters nothing writes, audits and announces
// nothing. A position of another organization is ErrNotFound.
func (s *Service) UpdatePosition(ctx context.Context, actorIID, orgID, positionID string, in PositionUpdate) (Position, error) {
	if err := s.z.RequireAdmin(ctx, orgID, actorIID); err != nil {
		return Position{}, err
	}
	var name string
	if in.Name != nil {
		var err error
		if name, err = validPositionName(*in.Name); err != nil {
			return Position{}, err
		}
	}
	var out Position
	var changed bool
	err := s.store.WithTx(ctx, func(q *db.Queries) error {
		if err := requireAdmin(ctx, q, orgID, actorIID); err != nil {
			return err
		}
		cur, err := q.GetPosition(ctx, db.GetPositionParams{ID: positionID, OrgID: orgID})
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		next := cur
		detail := map[string]any{"position_id": positionID}
		if in.Name != nil && name != cur.Name {
			if err := nameFree(ctx, q, orgID, name, cur.ID); err != nil {
				return err
			}
			next.Name = name
			detail["name"] = name
		}
		if in.ParentID != nil && *in.ParentID != cur.ParentID.String {
			parent := *in.ParentID
			if parent != "" {
				if err := requirePositionIn(ctx, q, orgID, parent); err != nil {
					return err
				}
				chart, err := Load(ctx, q, orgID)
				if err != nil {
					return err
				}
				if chart.HasCycle(positionID, parent) {
					return ErrCycle
				}
			}
			next.ParentID = nullable(parent)
			detail["parent_id"] = parent
		}
		out = position(next)
		if len(detail) == 1 {
			return nil
		}
		changed = true
		if err := q.UpdatePosition(ctx, db.UpdatePositionParams{Name: next.Name, ParentID: next.ParentID, ID: positionID, OrgID: orgID}); err != nil {
			return err
		}
		return s.record(ctx, q, actorIID, audit.ActionOrgPositionUpdate, orgID, detail)
	})
	if err != nil {
		return Position{}, err
	}
	if changed {
		s.changed(orgID)
	}
	return out, nil
}

// DeletePosition removes a position; only an admin does. Its holders, its child positions and the
// pending invitations that carry it move to its parent position, or have no position (and become roots)
// when it was a root (POS-09).
func (s *Service) DeletePosition(ctx context.Context, actorIID, orgID, positionID string) error {
	if err := s.z.RequireAdmin(ctx, orgID, actorIID); err != nil {
		return err
	}
	err := s.store.WithTx(ctx, func(q *db.Queries) error {
		if err := requireAdmin(ctx, q, orgID, actorIID); err != nil {
			return err
		}
		cur, err := q.GetPosition(ctx, db.GetPositionParams{ID: positionID, OrgID: orgID})
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		chart, err := Load(ctx, q, orgID)
		if err != nil {
			return err
		}
		holders, children := 0, 0
		for _, m := range chart.Members {
			if m.PositionID == positionID {
				holders++
			}
		}
		for _, p := range chart.Positions {
			if p.ParentID == positionID {
				children++
			}
		}
		if err := q.MovePositionHolders(ctx, db.MovePositionHoldersParams{ToPosition: cur.ParentID, OrgID: orgID, FromPosition: nullable(positionID)}); err != nil {
			return err
		}
		if err := q.MovePositionChildren(ctx, db.MovePositionChildrenParams{ToPosition: cur.ParentID, OrgID: orgID, FromPosition: nullable(positionID)}); err != nil {
			return err
		}
		if err := q.MovePositionInvitations(ctx, db.MovePositionInvitationsParams{ToPosition: cur.ParentID, OrgID: orgID, FromPosition: nullable(positionID)}); err != nil {
			return err
		}
		if err := q.DeletePosition(ctx, db.DeletePositionParams{ID: positionID, OrgID: orgID}); err != nil {
			return err
		}
		return s.record(ctx, q, actorIID, audit.ActionOrgPositionDelete, orgID, map[string]any{
			"position_id": positionID, "name": cur.Name, "moved_to": cur.ParentID.String, "holders": holders, "children": children,
		})
	})
	if err != nil {
		return err
	}
	s.changed(orgID)
	return nil
}
