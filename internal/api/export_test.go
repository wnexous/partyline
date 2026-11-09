package api

import "time"

// SetChunkIdleTimeout shortens how long a chunk may stall, for a test; restore puts it back.
func SetChunkIdleTimeout(d time.Duration) (restore func()) {
	old := chunkIdle.Swap(int64(d))
	return func() { chunkIdle.Store(old) }
}
