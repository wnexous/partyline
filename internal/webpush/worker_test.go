package webpush

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// discardLogger drops the worker logs of tests that do not inspect them.
func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// PUSH-27: Submit never blocks. A full queue refuses the job instead.
func TestWorkerQueueIsBounded(t *testing.T) {
	w := newWorker(2, 1, discardLogger())
	job := func(context.Context) {}
	require.True(t, w.submit(job))
	require.True(t, w.submit(job))
	require.False(t, w.submit(job), "the third job does not fit in a queue of two")
}

// PUSH-14: a panicking job is logged and the worker keeps running. The panic value may carry the
// text the job was handling, so it never reaches the log.
func TestWorkerRecoversAPanicAndKeepsRunning(t *testing.T) {
	var log bytes.Buffer
	w := newWorker(4, 1, slog.New(slog.NewTextHandler(&log, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.run(ctx) }()

	ran := make(chan struct{})
	require.True(t, w.submit(func(context.Context) { panic("secret text") }))
	require.True(t, w.submit(func(context.Context) { close(ran) }))

	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		t.Fatal("the worker stopped after a panicking job")
	}
	cancel()
	require.NoError(t, <-done)
	require.Contains(t, log.String(), "panic of type string")
	require.NotContains(t, log.String(), "secret text", "the panic value is never logged")
}

// PUSH-14: Run returns nil when its context ends.
func TestWorkerStopsOnCancel(t *testing.T) {
	w := newWorker(4, 2, discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.run(ctx) }()
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("the worker did not stop on cancel")
	}
}
