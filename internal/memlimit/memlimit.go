// Package memlimit sets the soft memory limit of the Go runtime, so that a container with a memory
// limit never meets its OOM killer before the garbage collector has tried to stay below it (OPS-06).
//
// The limit comes from, in this order: PARTYLINE_MEMORY_LIMIT; GOMEMLIMIT, which the runtime applies
// by itself; the memory limit of the cgroup the process runs in, less some headroom; and otherwise
// there is none.
package memlimit

import (
	"fmt"
	"strconv"
	"strings"
)

// Sources of an applied limit.
const (
	SourceConfig     = "PARTYLINE_MEMORY_LIMIT"
	SourceGoMemLimit = "GOMEMLIMIT"
	SourceCgroup     = "cgroup"
)

// Headroom is the share of the cgroup's memory limit that becomes the Go limit, in percent. The
// runtime keeps only what it manages under its limit (heap, stacks, its own tables); the rest of the
// container's memory (the page cache of the database, the binary itself) needs the remaining tenth.
const Headroom = 90

// MinLimit is the smallest limit PARTYLINE_MEMORY_LIMIT accepts: below it the process cannot do its
// work, and the garbage collector would run all the time.
const MinLimit = 16 << 20

// Limit is a memory limit an operator configured: a number of bytes, or none at all.
type Limit struct {
	// Bytes is the limit; it is zero when Off.
	Bytes int64
	// Off asks for no limit, whatever the container says.
	Off bool
}

// units are the suffixes of a size, as GOMEMLIMIT spells them.
var units = []struct {
	suffix string
	factor int64
}{
	{"TiB", 1 << 40}, {"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}, {"B", 1},
}

// Parse reads a limit in the syntax of GOMEMLIMIT: a number of bytes, optionally followed by B, KiB,
// MiB, GiB or TiB ("300MiB"), or "off" for no limit.
func Parse(s string) (Limit, error) {
	s = strings.TrimSpace(s)
	if s == "off" {
		return Limit{Off: true}, nil
	}
	digits, factor := s, int64(1)
	for _, u := range units {
		if rest, ok := strings.CutSuffix(s, u.suffix); ok {
			digits, factor = rest, u.factor
			break
		}
	}
	n, err := strconv.ParseInt(strings.TrimSpace(digits), 10, 64)
	if err != nil || n <= 0 {
		return Limit{}, fmt.Errorf("invalid size %q: use a number of bytes or a number with B, KiB, MiB, GiB or TiB (for example 300MiB), or off", s)
	}
	if n > (1<<63-1)/factor {
		return Limit{}, fmt.Errorf("invalid size %q: too large", s)
	}
	if n*factor < MinLimit {
		return Limit{}, fmt.Errorf("invalid size %q: at least %dMiB, or off", s, MinLimit>>20)
	}
	return Limit{Bytes: n * factor}, nil
}

// Result is the limit Apply left in force.
type Result struct {
	// Bytes is the limit; zero when there is none.
	Bytes int64
	// Source says where it comes from (SourceConfig, SourceGoMemLimit or SourceCgroup); empty when
	// there is no limit.
	Source string
}

// unlimited is what the Go runtime reports, and takes, for no limit.
const unlimited = 1<<63 - 1

// Apply chooses the soft memory limit of the Go runtime and sets it with set (debug.SetMemoryLimit,
// which also reads the limit in force when given a negative number).
//
// configured is PARTYLINE_MEMORY_LIMIT, nil when it is not set, and wins over everything. Next comes
// goMemLimit, the value of GOMEMLIMIT: the runtime applied it when the process started, so Apply
// leaves it alone. Last, the memory limit of the process's cgroup, found under cgroupRoot (the root
// of the filesystem; tests give another), less the headroom. Without any of them there is no limit.
func Apply(configured *Limit, goMemLimit, cgroupRoot string, set func(int64) int64) Result {
	switch {
	case configured != nil && configured.Off:
		set(unlimited)
		return Result{}
	case configured != nil:
		set(configured.Bytes)
		return Result{Bytes: configured.Bytes, Source: SourceConfig}
	case strings.TrimSpace(goMemLimit) != "":
		if current := set(-1); current > 0 && current != unlimited {
			return Result{Bytes: current, Source: SourceGoMemLimit}
		}
		return Result{}
	}
	limit, ok := cgroupLimit(cgroupRoot)
	if !ok {
		return Result{}
	}
	limit = limit / 100 * Headroom
	if limit < MinLimit {
		// A container so small that the limit would only keep the collector busy: let the OOM killer decide.
		return Result{}
	}
	set(limit)
	return Result{Bytes: limit, Source: SourceCgroup}
}
