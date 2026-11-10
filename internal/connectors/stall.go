package connectors

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"
)

// StallTimeout is how long a media transfer may go without a byte before it fails (LF-04): a slow
// but steady transfer of gigabytes goes on for as long as it takes, a stalled one gives its
// connection back.
const StallTimeout = time.Minute

// ErrStalled is the cause of the context of a transfer that stalled.
var ErrStalled = errors.New("the transfer stalled")

// Stall watches a transfer under the context WatchStall returns: the context is cancelled, with
// ErrStalled as its cause, when the first byte takes longer than its wait or two reads that bring
// bytes are further apart than its stall time. A request/response call bounded by a client timeout
// would fail a large file that is still arriving; this bounds only the silence.
type Stall struct {
	mu      sync.Mutex
	timer   *time.Timer
	stall   time.Duration
	cancel  context.CancelCauseFunc
	stopped bool
}

// WatchStall starts watching a transfer under ctx: wait bounds the time to its first byte (the
// platform may prepare the file before it answers), stall the silence after it.
func WatchStall(ctx context.Context, wait, stall time.Duration) (context.Context, *Stall) {
	ctx, cancel := context.WithCancelCause(ctx)
	s := &Stall{stall: stall, cancel: cancel}
	s.timer = time.AfterFunc(wait, func() { cancel(ErrStalled) })
	return ctx, s
}

// touch postpones the stall: bytes arrived.
func (s *Stall) touch() {
	s.mu.Lock()
	if !s.stopped {
		s.timer.Reset(s.stall)
	}
	s.mu.Unlock()
}

// Stop ends the watch and releases the context; the transfer is over.
func (s *Stall) Stop() {
	s.mu.Lock()
	s.stopped = true
	s.timer.Stop()
	s.mu.Unlock()
	s.cancel(nil)
}

// Body is rc, each read that brings bytes postponing the stall, and its Close ending the watch.
func (s *Stall) Body(rc io.ReadCloser) io.ReadCloser { return &stallBody{rc: rc, s: s} }

type stallBody struct {
	rc   io.ReadCloser
	s    *Stall
	once sync.Once
}

func (b *stallBody) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	if n > 0 {
		b.s.touch()
	}
	return n, err
}

func (b *stallBody) Close() error {
	err := b.rc.Close()
	b.once.Do(b.s.Stop)
	return err
}
