package whatsapp

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// LF-04: the inbox gives the frames back in order and holds at most its bytes, the frame being handled
// included: a frame that would go over waits for one to be handled, a frame larger than the whole inbox
// goes alone, and once closed the frames left come out before the end.
func TestInboxKeepsOrderAndBoundsItsBytes(t *testing.T) {
	b := newInbox(10)
	ctx := context.Background()
	require.NoError(t, b.put(ctx, []byte("aaaa")))
	require.NoError(t, b.put(ctx, []byte("bbbbbb")))

	put := make(chan error, 1)
	go func() { put <- b.put(ctx, []byte("c")) }()
	select {
	case err := <-put:
		t.Fatalf("a frame over the bound went in (%v)", err)
	case <-time.After(50 * time.Millisecond):
	}
	frame, ok := b.take(ctx)
	require.True(t, ok)
	require.Equal(t, "aaaa", string(frame))
	select {
	case err := <-put:
		t.Fatalf("a frame went in while the one taken was still being handled (%v)", err)
	case <-time.After(50 * time.Millisecond):
	}
	b.done(frame)
	select {
	case err := <-put:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the frame did not go in once there was room")
	}

	frame, _ = b.take(ctx)
	require.Equal(t, "bbbbbb", string(frame))
	b.done(frame)
	frame, _ = b.take(ctx)
	require.Equal(t, "c", string(frame))
	short, cancelShort := context.WithTimeout(ctx, 20*time.Millisecond)
	require.ErrorIs(t, b.put(short, make([]byte, 64)), context.DeadlineExceeded, "a large frame waits while another is handled")
	cancelShort()
	b.done(frame)
	require.NoError(t, b.put(ctx, make([]byte, 64)), "a large frame goes alone")

	short, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, b.put(short, []byte("d")), context.DeadlineExceeded, "a full inbox waits for the context")

	b.close()
	frame, ok = b.take(ctx)
	require.True(t, ok)
	require.Len(t, frame, 64)
	_, ok = b.take(ctx)
	require.False(t, ok, "closed and empty")
}
