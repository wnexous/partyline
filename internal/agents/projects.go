package agents

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/validation"
)

// Project is a project as seen by one user.
type Project struct {
	db.Project
	OwnerUsername    string
	OwnerDisplayName string
	// Level is manage for the owner and use for grantees.
	Level access.Level
}

// authorizeProject requires the user to hold want on the project: no access is
// access.ErrNotFound, use without manage is access.ErrForbidden.
func (r *Registry) authorizeProject(ctx context.Context, p authn.Principal, projectID string, want access.Level) (access.Level, error) {
	if p.Kind != authn.KindUser {
		return access.None, access.ErrForbidden
	}
	l, err := r.az.ProjectLevel(ctx, p.UserID, projectID)
	if err != nil {
		return access.None, err
	}
	if !access.AtLeast(l, dto.LevelUse) {
		return access.None, access.ErrNotFound
	}
	if !access.AtLeast(l, want) {
		return l, access.ErrForbidden
	}
	return l, nil
}

// Projects lists the projects the user owns plus those shared with the user, by name.
func (r *Registry) Projects(ctx context.Context, p authn.Principal) ([]Project, error) {
	if p.Kind != authn.KindUser {
		return nil, access.ErrForbidden
	}
	rows, err := r.store.R().ListProjectsForUser(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	out := make([]Project, len(rows))
	for i, row := range rows {
		l := dto.LevelUse
		if row.Project.OwnerID == p.UserID {
			l = dto.LevelManage
		}
		out[i] = Project{Project: row.Project, OwnerUsername: row.OwnerUsername, OwnerDisplayName: row.OwnerDisplayName, Level: l}
	}
	return out, nil
}

// GetProject returns a project the user owns or may use.
func (r *Registry) GetProject(ctx context.Context, p authn.Principal, projectID string) (Project, error) {
	l, err := r.authorizeProject(ctx, p, projectID, dto.LevelUse)
	if err != nil {
		return Project{}, err
	}
	row, err := r.store.R().GetProjectWithOwner(ctx, projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, access.ErrNotFound
	}
	if err != nil {
		return Project{}, err
	}
	return Project{Project: row.Project, OwnerUsername: row.OwnerUsername, OwnerDisplayName: row.OwnerDisplayName, Level: l}, nil
}

// RenameProject changes the project's name (1 to 64 characters after trimming; the slug
// stays) and the readable names of its temporary agents (PROJ-05); requires ownership.
func (r *Registry) RenameProject(ctx context.Context, p authn.Principal, projectID, name string) (Project, error) {
	if _, err := r.authorizeProject(ctx, p, projectID, dto.LevelManage); err != nil {
		return Project{}, err
	}
	name = strings.TrimSpace(name)
	if n := utf8.RuneCountInString(name); n == 0 || n > dto.MaxProjectName {
		return Project{}, &validation.Error{Fields: map[string]string{"name": "must have 1 to 64 characters"}}
	}
	var roomIDs []string
	err := r.store.WithTx(ctx, func(q *db.Queries) error {
		if err := q.UpdateProjectName(ctx, db.UpdateProjectNameParams{Name: name, ID: projectID}); err != nil {
			return err
		}
		list, err := q.ListTemporaryProjectAgents(ctx, sql.NullString{String: projectID, Valid: true})
		if err != nil {
			return err
		}
		for _, a := range list {
			if err := q.SetAgentAutoName(ctx, db.SetAgentAutoNameParams{AutoName: AutoName(name, a.Harness, a.SessionNo), ID: a.ID}); err != nil {
				return err
			}
			ids, err := q.ListAgentRoomIDs(ctx, a.ID)
			if err != nil {
				return err
			}
			roomIDs = append(roomIDs, ids...)
		}
		return nil
	})
	if err != nil {
		return Project{}, err
	}
	r.membersChanged(roomIDs)
	return r.GetProject(ctx, p, projectID)
}

// ProjectSharing returns the project's visibility preset; requires use.
func (r *Registry) ProjectSharing(ctx context.Context, p authn.Principal, projectID string) (access.Visibility, error) {
	if _, err := r.authorizeProject(ctx, p, projectID, dto.LevelUse); err != nil {
		return access.Visibility{}, err
	}
	return r.az.Sharing(ctx, access.KindProject, projectID)
}

// SetProjectSharing replaces the project's use grants with a visibility preset; requires
// ownership. A use grant lets the grantee add the project to rooms (PROJ-06).
func (r *Registry) SetProjectSharing(ctx context.Context, p authn.Principal, projectID string, v access.Visibility) (access.Visibility, error) {
	if _, err := r.authorizeProject(ctx, p, projectID, dto.LevelManage); err != nil {
		return access.Visibility{}, err
	}
	pr, err := r.store.R().GetProject(ctx, projectID)
	if err != nil {
		return access.Visibility{}, err
	}
	if err := r.az.SetVisibility(ctx, p.UserID, access.KindProject, projectID, pr.OwnerID, v); err != nil {
		return access.Visibility{}, err
	}
	return r.az.Sharing(ctx, access.KindProject, projectID)
}
