// Package settings holds the instance settings an instance admin changes in the web app (RET-01): each
// one has a default from the server's configuration, and a value the admin set, kept in
// instance_settings, applies instead until the admin goes back to the default. Callers check that the
// caller is an instance admin; this package only validates and stores.
package settings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/wnexous/partyline/internal/audit"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/validation"
)

// MaxAttachmentRetentionDays bounds the attachment retention (RET-02): about a hundred years.
const MaxAttachmentRetentionDays = 36500

// KeyAttachmentRetentionDays is the row of the attachment retention in instance_settings.
const KeyAttachmentRetentionDays = "attachment_retention_days"

// AttachmentRetention is the effective attachment retention: Days is what applies (0 keeps attachments
// forever), DefaultDays the configuration's value and Overridden whether an admin's value applies.
type AttachmentRetention struct {
	Days        int
	DefaultDays int
	Overridden  bool
}

// Deps are the service's dependencies.
type Deps struct {
	Store *store.Store
	Audit *audit.Log
	// AttachmentRetentionDays is the default retention (PARTYLINE_ATTACHMENT_RETENTION_DAYS).
	AttachmentRetentionDays int
	// Changed is called after a setting changed, outside the transaction (the app wakes the retention
	// job); nil calls nothing.
	Changed func()
	Now     func() time.Time
}

// Service reads and changes the instance settings.
type Service struct {
	d Deps
}

// New returns the service.
func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{d: d}
}

// AttachmentRetention returns the retention that applies now.
func (s *Service) AttachmentRetention(ctx context.Context) (AttachmentRetention, error) {
	return s.attachmentRetention(ctx, s.d.Store.R())
}

// AttachmentRetentionDays is AttachmentRetention's Days, for the retention job.
func (s *Service) AttachmentRetentionDays(ctx context.Context) (int, error) {
	r, err := s.AttachmentRetention(ctx)
	return r.Days, err
}

func (s *Service) attachmentRetention(ctx context.Context, q *db.Queries) (AttachmentRetention, error) {
	out := AttachmentRetention{Days: s.d.AttachmentRetentionDays, DefaultDays: s.d.AttachmentRetentionDays}
	row, err := q.GetInstanceSetting(ctx, KeyAttachmentRetentionDays)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	days, err := strconv.Atoi(row.Value)
	if err != nil || days < 0 || days > MaxAttachmentRetentionDays {
		return out, fmt.Errorf("instance setting %s: invalid stored value", KeyAttachmentRetentionDays)
	}
	out.Days, out.Overridden = days, true
	return out, nil
}

// SetAttachmentRetention sets the admin's retention (days != nil) or goes back to the default (days
// nil), records the change in the audit log in the same transaction and returns what applies now. A
// value outside 0..MaxAttachmentRetentionDays is a *validation.Error.
func (s *Service) SetAttachmentRetention(ctx context.Context, days *int) (AttachmentRetention, error) {
	if days != nil && (*days < 0 || *days > MaxAttachmentRetentionDays) {
		return AttachmentRetention{}, &validation.Error{Fields: map[string]string{
			"attachment_retention_days": fmt.Sprintf("must be 0 (keep forever) to %d days", MaxAttachmentRetentionDays),
		}}
	}
	var out AttachmentRetention
	changed := false
	err := s.d.Store.WithTx(ctx, func(q *db.Queries) error {
		before, err := s.attachmentRetention(ctx, q)
		if err != nil {
			return err
		}
		if days == nil {
			_, err = q.DeleteInstanceSetting(ctx, KeyAttachmentRetentionDays)
		} else {
			err = q.UpsertInstanceSetting(ctx, db.UpsertInstanceSettingParams{
				Key: KeyAttachmentRetentionDays, Value: strconv.Itoa(*days), UpdatedAt: s.d.Now().UnixMilli(),
			})
		}
		if err != nil {
			return err
		}
		if out, err = s.attachmentRetention(ctx, q); err != nil {
			return err
		}
		if before == out {
			return nil
		}
		changed = true
		if s.d.Audit == nil {
			return nil
		}
		return s.d.Audit.Record(ctx, q, audit.Entry{
			Action: audit.ActionInstanceSettingsUpdate, TargetKind: audit.TargetInstance, TargetID: KeyAttachmentRetentionDays,
			Detail: map[string]any{
				"setting": KeyAttachmentRetentionDays, "from": before.Days, "to": out.Days, "default": out.DefaultDays,
				"overridden": out.Overridden,
			},
		})
	})
	if err != nil {
		return AttachmentRetention{}, err
	}
	if changed && s.d.Changed != nil {
		s.d.Changed()
	}
	return out, nil
}
