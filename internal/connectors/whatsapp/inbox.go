package whatsapp

import (
	"context"
	"sync"
)

// maxInboxBytes bounds the frames read from the WebSocket and not handled yet, the one being handled
// included. A frame larger than it (inline media) is still taken alone.
const maxInboxBytes = 32 << 20

// inbox holds, in order, the frames the WebSocket brought while the one before them is being handled:
// a message whose media takes minutes to download (LF-04) must not stop the socket from being read,
// or its pongs would go unread and the connection would drop. The frames waiting and the one being
// handled hold at most maxBytes together (one larger frame alone); a reader that would go over waits
// with the frame it read, which slows the socket down instead of growing the memory. The memory of
// frames is so at most maxBytes, or one larger frame, plus the frame the reader holds.
type inbox struct {
	mu       sync.Mutex
	frames   [][]byte
	bytes    int // of the frames waiting and the one being handled
	maxBytes int
	closed   bool
	ready    chan struct{} // signalled when a frame is added or the inbox is closed
	room     chan struct{} // signalled when a frame is taken
}

func newInbox(maxBytes int) *inbox {
	return &inbox{maxBytes: maxBytes, ready: make(chan struct{}, 1), room: make(chan struct{}, 1)}
}

func signal(c chan struct{}) {
	select {
	case c <- struct{}{}:
	default:
	}
}

// put adds a frame, waiting while the inbox is full; it fails only when ctx is done.
func (b *inbox) put(ctx context.Context, frame []byte) error {
	for {
		b.mu.Lock()
		if b.bytes == 0 || b.bytes+len(frame) <= b.maxBytes {
			b.frames = append(b.frames, frame)
			b.bytes += len(frame)
			b.mu.Unlock()
			signal(b.ready)
			return nil
		}
		b.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-b.room:
		}
	}
}

// close tells take that no frame will be added: it returns the frames left, then false.
func (b *inbox) close() {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
	signal(b.ready)
}

// take returns the oldest frame, waiting for one; false once the inbox is closed and empty, or when
// ctx is done. The frame counts against the bound until done is called with it.
func (b *inbox) take(ctx context.Context) ([]byte, bool) {
	for {
		b.mu.Lock()
		if len(b.frames) > 0 {
			frame := b.frames[0]
			b.frames[0] = nil
			b.frames = b.frames[1:]
			b.mu.Unlock()
			return frame, true
		}
		closed := b.closed
		b.mu.Unlock()
		if closed {
			return nil, false
		}
		select {
		case <-ctx.Done():
			return nil, false
		case <-b.ready:
		}
	}
}

// done tells that a frame take returned was handled: its bytes no longer count.
func (b *inbox) done(frame []byte) {
	b.mu.Lock()
	b.bytes -= len(frame)
	b.mu.Unlock()
	signal(b.room)
}
