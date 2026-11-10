package dto

// InstanceSettings are the instance settings an instance admin changes (GET and PATCH
// /admin/settings, RET-01, RET-02).
type InstanceSettings struct {
	// AttachmentRetentionDays deletes room attachments older than this many days; 0 keeps them
	// forever. It is the admin's value when AttachmentRetentionOverridden, else the default.
	AttachmentRetentionDays int `json:"attachment_retention_days"`
	// AttachmentRetentionDefaultDays is the server's default (PARTYLINE_ATTACHMENT_RETENTION_DAYS).
	AttachmentRetentionDefaultDays int `json:"attachment_retention_default_days"`
	// AttachmentRetentionOverridden tells whether an admin's value applies instead of the default.
	AttachmentRetentionOverridden bool `json:"attachment_retention_overridden"`
}

// UpdateInstanceSettingsRequest is the body of PATCH /admin/settings: AttachmentRetentionDays sets
// the admin's value (0 to 36500), ResetAttachmentRetention goes back to the server's default; at most
// one of them is set, and an empty body changes nothing.
type UpdateInstanceSettingsRequest struct {
	AttachmentRetentionDays  *int `json:"attachment_retention_days,omitempty"`
	ResetAttachmentRetention bool `json:"reset_attachment_retention,omitempty"`
}
