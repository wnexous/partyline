package dto

import "time"

// FileKind groups attachments by MIME family for the room files list (FILES-01, FILES-03).
type FileKind string

// File kinds: image/*, audio/*, video/*, and document for every other type.
const (
	FileKindImage    FileKind = "image"
	FileKindAudio    FileKind = "audio"
	FileKindVideo    FileKind = "video"
	FileKindDocument FileKind = "document"
)

// RoomFile is one attachment of GET /rooms/{id}/attachments?kind=&before=&limit= (newest first,
// limit default DefaultPageSize, max MaxPageSize; the next page starts before=<last id>).
// Download it with GET /attachments/{id} unless its status is too_large or unavailable.
type RoomFile struct {
	ID         string           `json:"id"`
	Name       string           `json:"name"`
	Mime       string           `json:"mime"`
	Size       int64            `json:"size"`
	Status     AttachmentStatus `json:"status"`
	Kind       FileKind         `json:"kind"`
	MessageID  string           `json:"message_id"`
	MessageSeq int64            `json:"message_seq"`
	AuthorKind AuthorKind       `json:"author_kind"`
	AuthorName string           `json:"author_name"`
	// Author is the intelligence that wrote the message, rendered as Message.Author is (PROF-09):
	// it carries the kind, role and platform the badge next to the name needs. It has no id when
	// the sender is a chat account that resolved to nobody.
	Author    IntelligenceRef `json:"author"`
	CreatedAt time.Time       `json:"created_at"`
}
