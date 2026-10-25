// Package datalock lets one process at a time run a server on a data directory (OPS-05).
//
// Two servers on one directory would each run every connector, so every reply would be sent twice
// and every received message queued twice, and they would fight over the database. A start-first
// update of a container or a stray `partyline serve` does exactly that. The server therefore takes
// an exclusive lock on a file of the directory when it starts, and a second one fails at once. The
// operating system drops the lock when the process ends, however it ends, so a crash leaves nothing
// stale to clean up.
package datalock

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// FileName is the lock file inside the data directory.
const FileName = "partyline.lock"

// ErrLocked is what a *LockedError matches with errors.Is.
var ErrLocked = errors.New("the data directory is in use by another partyline process")

// LockedError says that another process holds the data directory.
type LockedError struct {
	// Dir is the data directory and Path its lock file.
	Dir, Path string
	// Holder describes the process that holds it, as that process wrote it ("pid 42 on host abc");
	// empty when it is unknown.
	Holder string
}

func (e *LockedError) Error() string {
	holder := ""
	if e.Holder != "" {
		holder = " (" + e.Holder + ")"
	}
	return fmt.Sprintf("the data directory %s is already in use by another partyline process%s: stop it first, "+
		"or both would run every connector and send every reply twice (lock file %s)", e.Dir, holder, e.Path)
}

// Is makes every LockedError match ErrLocked.
func (e *LockedError) Is(target error) bool { return target == ErrLocked }

// Lock is the hold of a process on a data directory, until Release.
type Lock struct {
	f *os.File
}

// Acquire takes the exclusive lock of the data directory, creating the directory and the lock file
// when they are missing. It does not wait: when another process holds the directory it fails at once
// with a *LockedError.
func Acquire(dir string) (*Lock, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil { // #nosec G703 -- the data directory the operator configured
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	path := filepath.Join(dir, FileName)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600) // #nosec G304 G703 -- the lock file of the configured data directory
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := lock(f); err != nil {
		holder := readHolder(f)
		_ = f.Close()
		if isLocked(err) {
			return nil, &LockedError{Dir: dir, Path: path, Holder: holder}
		}
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	writeHolder(f)
	return &Lock{f: f}, nil
}

// Release gives the directory up. It does nothing when the lock was released before.
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	f := l.f
	l.f = nil
	// The lock file stays: removing it would let a process lock the old file while another creates a new one.
	return errors.Join(unlock(f), f.Close())
}

// writeHolder records which process holds the lock, for the one that finds it taken. It is
// information only, so a failure to write it is not an error.
func writeHolder(f *os.File) {
	host, _ := os.Hostname()
	if f.Truncate(0) != nil {
		return
	}
	_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())+" "+host+"\n"), 0)
}

// readHolder describes the process that wrote the lock file ("pid 42 on host abc"), or "" when the
// file says nothing usable. The text is limited to what a pid and a host name consist of.
func readHolder(f *os.File) string {
	data, err := io.ReadAll(io.LimitReader(f, 512))
	if err != nil {
		return ""
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return ""
	}
	pid, err := strconv.Atoi(fields[0])
	if err != nil || pid <= 0 {
		return ""
	}
	holder := "pid " + strconv.Itoa(pid)
	if len(fields) > 1 {
		if host := safeHost(fields[1]); host != "" {
			holder += " on " + host
		}
	}
	return holder
}

// safeHost keeps s when it looks like a host name and drops it otherwise.
func safeHost(s string) string {
	if len(s) > 253 {
		return ""
	}
	for _, r := range s {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_'
		if !ok {
			return ""
		}
	}
	return s
}
