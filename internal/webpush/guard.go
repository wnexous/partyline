package webpush

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"runtime/debug"
)

// guardJob runs one worker job and turns a panic into a log line with the stack, so the worker
// survives and keeps processing (PUSH-14). It copies the connectors' Guard rule
// (internal/connectors/guard.go): the panic value is never logged, because a panic is often built
// from what the job was handling and message text never goes to a log (PUSH-22).
func guardJob(ctx context.Context, log *slog.Logger, j job) {
	defer func() {
		if v := recover(); v != nil {
			if log == nil {
				log = slog.Default()
			}
			log.Error("webpush: a push job panicked", "panic", panicDetail(v), "stack", string(debug.Stack()))
		}
	}()
	j(ctx)
}

// panicDetail describes a panic value without the data it may carry: it keeps what the runtime
// itself says and reduces anything else to its type; the stack says where it came from.
func panicDetail(v any) string {
	if err, ok := v.(runtime.Error); ok {
		return err.Error()
	}
	return fmt.Sprintf("panic of type %T", v)
}
