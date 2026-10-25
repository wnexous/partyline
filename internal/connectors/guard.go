package connectors

import (
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"runtime/debug"
)

// ErrPanic is what a *PanicError matches with errors.Is: connector code panicked and the panic was
// recovered (CON-15).
var ErrPanic = errors.New("the connector panicked")

// PanicError is a recovered panic of connector code. It describes the panic without any data that
// may have caused it, so it is safe to log, store and show (SEC-05).
type PanicError struct {
	// Where names the call that panicked ("run", "inbound", "send", ...).
	Where string
	// Detail is what panicDetail made of the panic value.
	Detail string
}

func (e *PanicError) Error() string {
	return "the connector crashed in " + e.Where + ": " + e.Detail
}

// Is makes every PanicError match ErrPanic.
func (e *PanicError) Is(target error) bool { return target == ErrPanic }

// Guard calls fn and turns a panic of fn into a *PanicError, which it also logs with the stack. The
// manager guards every call it makes into a connector and every call a connector makes into it, and a
// connector guards the goroutines it starts itself: a panic in a goroutine nobody guards ends the whole
// process, whatever supervises the connector. Guard recovers in the goroutine it runs in, so a goroutine
// fn starts is not covered.
//
// log should be scoped to the connector. The panic value is never logged (panicDetail), only its stack.
func Guard(log *slog.Logger, where string, fn func() error) (err error) {
	defer func() {
		if v := recover(); v != nil {
			pe := &PanicError{Where: where, Detail: panicDetail(v)}
			if log == nil {
				log = slog.Default()
			}
			// The stack lists functions and argument words, never the text behind a string.
			log.Error("connectors: a connector panicked", "where", where, "panic", pe.Detail, "stack", string(debug.Stack()))
			err = pe
		}
	}()
	return fn()
}

// panicDetail describes a panic value without the data it may carry: a panic is often built from what
// the code was handling, and message text never goes to logs or statuses (SEC-05). It keeps what the
// runtime itself says (an index out of range, a nil dereference: numbers and type names) and reduces
// anything else to its type; the stack says where it came from.
func panicDetail(v any) string {
	if err, ok := v.(runtime.Error); ok {
		return err.Error()
	}
	return fmt.Sprintf("panic of type %T", v)
}
