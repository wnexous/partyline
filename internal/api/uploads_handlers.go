package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/rooms"
	"github.com/wnexous/partyline/internal/uploads"
)

// maxUploadIDs bounds the uploads one message attaches.
const maxUploadIDs = 20

// Upload routes: a user's under /api/v1/uploads, an AI's under /api/v1/agent/uploads, since an agent
// token reaches the agent routes only (TOK-03). An upload belongs to the identity that created it.
const (
	uploadsPath      = "/api/v1/uploads/"
	agentUploadsPath = agentPathPrefix + "uploads/"
)

func (s *server) uploadRoutes(mux *http.ServeMux) {
	if s.Uploads == nil {
		return
	}
	s.handle(mux, "POST /api/v1/uploads", authed(s.createUpload))
	s.handle(mux, "GET /api/v1/uploads/{id}", authed(s.getUpload))
	s.handle(mux, "PUT /api/v1/uploads/{id}", authed(s.writeUpload))
	s.handle(mux, "POST /api/v1/agent/uploads", s.agentOnly(s.createUpload))
	s.handle(mux, "GET /api/v1/agent/uploads/{id}", s.agentOnly(s.getUpload))
	s.handle(mux, "PUT /api/v1/agent/uploads/{id}", s.agentOnly(s.writeUpload))
}

// uploadOwner is the identity whose uploads p reaches: the AI of an agent request, else the user.
func uploadOwner(p authn.Principal) uploads.Owner {
	if p.Kind == authn.KindAgent {
		return uploads.Owner{Kind: uploads.OwnerAgent, ID: p.AgentID}
	}
	return uploads.Owner{Kind: uploads.OwnerUser, ID: p.UserID}
}

func (s *server) toUpload(u uploads.Upload) dto.Upload {
	return dto.Upload{
		ID: u.ID, Name: u.Name, Mime: u.Mime, Size: u.Size, Received: u.Received, Complete: u.Complete(),
		Sha256: u.SHA256, ChunkSize: s.Uploads.ChunkSize(), MaxSize: s.Uploads.MaxFileBytes(), ExpiresAt: u.ExpiresAt,
	}
}

// createUpload starts an upload session and answers 201 with it (LF-03, LF-07).
func (s *server) createUpload(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.CreateUploadRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	u, err := s.Uploads.Create(r.Context(), uploadOwner(p), uploads.CreateInput{Name: req.Name, Mime: req.Mime, Size: req.Size})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, s.toUpload(u))
	return nil
}

// getUpload answers the state of an upload, for a client that resumes it.
func (s *server) getUpload(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	u, err := s.Uploads.Get(r.Context(), uploadOwner(p), r.PathValue("id"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, s.toUpload(u))
	return nil
}

// writeUpload appends the raw body, one chunk, at ?offset= and answers 200 with the upload's state.
func (s *server) writeUpload(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	offset, err := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	if err != nil || offset < 0 {
		return httpx.NewError(http.StatusBadRequest, dto.ErrorCodeInvalid, "offset must be the number of bytes the upload received")
	}
	u, err := s.Uploads.Write(r.Context(), uploadOwner(p), r.PathValue("id"), offset, r.Body)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, s.toUpload(u))
	return nil
}

// uploadFiles turns the finished uploads of p named by ids into the files of a message (LF-03).
func (s *server) uploadFiles(ctx context.Context, p authn.Principal, ids []string) ([]rooms.File, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if s.Uploads == nil {
		return nil, httpx.NewError(http.StatusBadRequest, dto.ErrorCodeInvalid, "this server takes no upload sessions")
	}
	if len(ids) > maxUploadIDs {
		return nil, httpx.Invalid(map[string]string{"upload_ids": "at most " + strconv.Itoa(maxUploadIDs) + " uploads per message"})
	}
	list, err := s.Uploads.Finished(ctx, uploadOwner(p), ids)
	if err != nil {
		return nil, err
	}
	files := make([]rooms.File, len(list))
	for i, u := range list {
		files[i] = rooms.File{SHA256: u.SHA256, Name: u.Name, Mime: u.Mime, Size: u.Size}
	}
	return files, nil
}

// isUploadChunk reports whether r sends a chunk of an upload session.
func isUploadChunk(r *http.Request) bool {
	if r.Method != http.MethodPut {
		return false
	}
	return hasIDAfter(r.URL.Path, uploadsPath) || hasIDAfter(r.URL.Path, agentUploadsPath)
}

func hasIDAfter(path, prefix string) bool {
	if len(path) <= len(prefix) || path[:len(prefix)] != prefix {
		return false
	}
	for _, c := range path[len(prefix):] {
		if c == '/' {
			return false
		}
	}
	return true
}

// uploadError maps the errors of upload sessions; nil for any other error.
func uploadError(err error) error {
	var fileTooLarge *uploads.FileTooLargeError
	var chunkTooLarge *uploads.ChunkTooLargeError
	var storage *uploads.InsufficientStorageError
	var offset *uploads.OffsetError
	switch {
	case errors.Is(err, uploads.ErrNotFound):
		return httpx.NewError(http.StatusNotFound, dto.ErrorCodeNotFound, "upload not found")
	case errors.Is(err, uploads.ErrInvalidSize):
		he := httpx.NewError(http.StatusBadRequest, dto.ErrorCodeInvalid, "size must be a positive number of bytes")
		he.Fields = map[string]string{"size": "must be a positive number of bytes"}
		return he
	case errors.As(err, &fileTooLarge):
		he := httpx.NewError(http.StatusRequestEntityTooLarge, dto.ErrorCodeFileTooLarge,
			"the file is larger than this server takes ("+humanMB(fileTooLarge.Max)+")")
		he.Details = map[string]string{"max_size": strconv.FormatInt(fileTooLarge.Max, 10)}
		return he
	case errors.As(err, &chunkTooLarge):
		he := httpx.NewError(http.StatusRequestEntityTooLarge, dto.ErrorCodeTooLarge,
			"a chunk holds at most chunk_size bytes and ends at the size of the file")
		he.Details = map[string]string{"max": strconv.FormatInt(chunkTooLarge.Max, 10)}
		return he
	case errors.As(err, &storage):
		return httpx.NewError(http.StatusInsufficientStorage, dto.ErrorCodeInsufficientStorage,
			"the server does not have enough free disk space for this file")
	case errors.As(err, &offset):
		he := httpx.NewError(http.StatusConflict, dto.ErrorCodeUploadOffset, offset.Error())
		he.Details = map[string]string{"received": strconv.FormatInt(offset.Received, 10), "complete": strconv.FormatBool(offset.Complete)}
		return he
	case errors.Is(err, uploads.ErrBusy):
		return httpx.NewError(http.StatusConflict, dto.ErrorCodeUploadBusy, "another chunk of this upload is being written")
	case errors.Is(err, uploads.ErrIncomplete):
		return httpx.NewError(http.StatusConflict, dto.ErrorCodeUploadIncomplete, "the upload is not complete: send its remaining chunks first")
	case errors.Is(err, uploads.ErrTooMany):
		return httpx.NewError(http.StatusTooManyRequests, dto.ErrorCodeRateLimited,
			"too many uploads: post or wait for the ones you have before starting another")
	case errors.Is(err, uploads.ErrOwnerBytes):
		return httpx.NewError(http.StatusTooManyRequests, dto.ErrorCodeRateLimited,
			"your uploads would hold more than the largest file: post or wait for the ones you have first")
	case errors.Is(err, uploads.ErrEmptyChunk):
		return httpx.NewError(http.StatusBadRequest, dto.ErrorCodeInvalid, "the chunk is empty")
	}
	return nil
}

// humanMB renders a byte count in whole MB.
func humanMB(n int64) string { return strconv.FormatInt(n>>20, 10) + " MB" }
