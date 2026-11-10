package connectors_test

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/connectors"
)

// LF-04: a transfer fails when its first byte takes longer than its wait, or when it brings nothing for
// its stall time; a steady one goes on for longer than both, and closing its body ends the watch.
func TestStallWatch(t *testing.T) {
	ctx, stall := connectors.WatchStall(context.Background(), 20*time.Millisecond, time.Hour)
	defer stall.Stop()
	select {
	case <-ctx.Done():
		require.ErrorIs(t, context.Cause(ctx), connectors.ErrStalled)
	case <-time.After(5 * time.Second):
		t.Fatal("a transfer whose first byte never came was not stopped")
	}

	ctx, stall = connectors.WatchStall(context.Background(), 400*time.Millisecond, 400*time.Millisecond)
	body := stall.Body(io.NopCloser(strings.NewReader(strings.Repeat("x", 12))))
	one := make([]byte, 1)
	for range 12 { // a byte every 100 ms: 1.2 s in all, longer than the wait and the stall
		_, err := body.Read(one)
		require.NoError(t, err)
		require.NoError(t, ctx.Err(), "a steady transfer goes on")
		time.Sleep(100 * time.Millisecond)
	}
	require.NoError(t, body.Close())
	require.ErrorIs(t, ctx.Err(), context.Canceled, "closing the body releases the context")
	require.NotErrorIs(t, context.Cause(ctx), connectors.ErrStalled)

	ctx, stall = connectors.WatchStall(context.Background(), time.Hour, 20*time.Millisecond)
	body = stall.Body(io.NopCloser(strings.NewReader("xy")))
	_, err := body.Read(one)
	require.NoError(t, err)
	select {
	case <-ctx.Done():
		require.ErrorIs(t, context.Cause(ctx), connectors.ErrStalled, "silence after the first byte")
	case <-time.After(5 * time.Second):
		t.Fatal("a stalled transfer was not stopped")
	}
	require.NoError(t, body.Close())
}
