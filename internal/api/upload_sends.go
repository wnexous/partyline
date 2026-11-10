package api

import (
	"net/http"
	"sync"
	"time"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
)

// uploadSentMemory is how long a send through a connection remembers the uploads it used: as long as
// an upload lives, so that a send repeated after a lost answer is told what happened.
const uploadSentMemory = 24 * time.Hour

// uploadSends keeps the uploads being sent through a connection and those sent lately, by owner and
// id, so that one upload goes out once (LF-04): a second send while the first runs is 409
// upload_busy, and one after it 409 upload_sent, instead of a second copy or a misleading 404. It lives
// in memory: a restart forgets it, and the uploads a send used are gone by then anyway.
type uploadSends struct {
	mu   sync.Mutex
	now  func() time.Time
	busy map[string]bool
	sent map[string]time.Time
}

func newUploadSends(now func() time.Time) *uploadSends {
	return &uploadSends{now: now, busy: map[string]bool{}, sent: map[string]time.Time{}}
}

// begin claims the upload key for a send, or says why it cannot be sent.
func (u *uploadSends) begin(key string) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.busy[key] {
		return httpx.NewError(http.StatusConflict, dto.ErrorCodeUploadBusy,
			"this upload is being sent through a connection by another request: wait for it, and check the chat before sending the file again")
	}
	if at, ok := u.sent[key]; ok && u.now().Sub(at) < uploadSentMemory {
		return httpx.NewError(http.StatusConflict, dto.ErrorCodeUploadSent,
			"this upload was already sent through a connection at "+at.UTC().Format("15:04 UTC")+
				", whether the platform took it or not: check the chat before sending the file again with a new upload")
	}
	u.busy[key] = true
	return nil
}

// end releases the key; attempted records that the send used the upload.
func (u *uploadSends) end(key string, attempted bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	delete(u.busy, key)
	now := u.now()
	for k, at := range u.sent {
		if now.Sub(at) >= uploadSentMemory {
			delete(u.sent, k)
		}
	}
	if attempted {
		u.sent[key] = now
	}
}
