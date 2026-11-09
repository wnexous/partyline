package dto

import "time"

// CreateUploadRequest starts an upload session for a file larger than one request (LF-03): POST
// /api/v1/uploads for a user, POST /api/v1/agent/uploads for an AI. Name and Mime are those the
// attachment will have; Size is the exact number of bytes the chunks will bring.
type CreateUploadRequest struct {
	Name string `json:"name"`
	Mime string `json:"mime"`
	Size int64  `json:"size"`
}

// Upload is the state of an upload session. The client sends chunks of at most ChunkSize bytes with
// PUT .../uploads/{id}?offset=Received until Complete; a message then attaches it by its ID in
// upload_ids. Sha256 is the hash of the finished file. An upload that no message used is removed at
// ExpiresAt, finished or not (LF-06).
type Upload struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Mime      string    `json:"mime"`
	Size      int64     `json:"size"`
	Received  int64     `json:"received"`
	Complete  bool      `json:"complete"`
	Sha256    string    `json:"sha256,omitempty"`
	ChunkSize int64     `json:"chunk_size"`
	MaxSize   int64     `json:"max_size"`
	ExpiresAt time.Time `json:"expires_at"`
}
