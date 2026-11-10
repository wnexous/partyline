package dto

import "time"

// CreateUploadRequest starts an upload session for a file larger than one request (LF-03): POST
// /api/v1/uploads for a user, POST /api/v1/agent/uploads for an AI. Name and Mime are those the
// attachment will have; Size is the exact number of bytes the chunks will bring. Connection, for an AI,
// names the connection the file will be sent through (LF-05): a file that connection could not send (over
// its platform's own limit, a platform without files, an AI that does not operate it) is refused before
// any chunk is sent, as the send itself would refuse it.
type CreateUploadRequest struct {
	Name       string `json:"name"`
	Mime       string `json:"mime"`
	Size       int64  `json:"size"`
	Connection string `json:"connection,omitempty"`
}

// Upload is the state of an upload session. The client sends chunks of at most ChunkSize bytes with
// PUT .../uploads/{id}?offset=Received until Complete; a message then attaches it by its ID in
// upload_ids. Sha256 is the hash of the finished file. An upload that no message used is removed at
// ExpiresAt, finished or not: an hour after creation until its first chunk, then 24 hours after its
// last chunk (LF-06).
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
