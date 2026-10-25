package webpush

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
)

// The bounded worker of the notifier (PUSH-14, PUSH-27).
const (
	// defaultQueueSize is how many jobs wait for a send goroutine; app.Options.PushQueueSize
	// overrides it in tests.
	defaultQueueSize = 256
	// defaultWorkers is how many jobs run at once.
	defaultWorkers = 4
)

// job is one unit of work: a payload for one subscription.
type job func(context.Context)

// worker runs jobs on a bounded queue. A full queue refuses a job instead of blocking the bus
// consumer, and a panicking job is logged without its value and does not stop the worker.
type worker struct {
	jobs    chan job
	workers int
	log     *slog.Logger
	dropped atomic.Int64
}

// newWorker returns a worker whose queue holds queueSize jobs and that runs count jobs at once.
// Zero or negative values use the package defaults.
func newWorker(queueSize, count int, log *slog.Logger) *worker {
	if queueSize <= 0 {
		queueSize = defaultQueueSize
	}
	if count <= 0 {
		count = defaultWorkers
	}
	if log == nil {
		log = slog.Default()
	}
	return &worker{jobs: make(chan job, queueSize), workers: count, log: log}
}

// submit queues job without blocking; it returns false when the queue is full.
func (w *worker) submit(j job) bool {
	select {
	case w.jobs <- j:
		return true
	default:
		w.dropped.Add(1)
		return false
	}
}

// run starts the send goroutines and returns nil once ctx ends and they have stopped. Queued
// jobs are dropped then, which a notification tolerates (PUSH-14).
func (w *worker) run(ctx context.Context) error {
	var wg sync.WaitGroup
	for range w.workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.consume(ctx)
		}()
	}
	<-ctx.Done()
	wg.Wait()
	return nil
}

// consume runs jobs until ctx ends.
func (w *worker) consume(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-w.jobs:
			guardJob(ctx, w.log, j)
		}
	}
}
