package memlimit

import (
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// v1Unlimited is the smallest value with which cgroup v1 says "no limit" (it reports a page-aligned
// 2^63 - 1, the same as a limit nobody could ever reach).
const v1Unlimited = 1 << 62

// cgroupLimit returns the memory limit of the cgroup the process runs in, in bytes, looking under
// root (the root of the filesystem). ok is false when there is none: no cgroup, "max" all the way up,
// or files that cannot be read.
//
// The process's own cgroup says where to look, and the limit that counts is the smallest on the way up
// to the root: a service inside a slice is held by the slice's limit too. In a container with its own
// cgroup namespace (the Docker default) the path is just "/".
func cgroupLimit(root string) (int64, bool) {
	self, err := os.ReadFile(filepath.Join(root, "proc/self/cgroup")) // #nosec G304 G703 -- a fixed path under the root
	if err != nil {
		return 0, false
	}
	var v1Path, v2Path string
	for line := range strings.SplitSeq(string(self), "\n") {
		parts := strings.SplitN(line, ":", 3) // hierarchy id : controllers : path
		if len(parts) != 3 {
			continue
		}
		switch {
		case parts[0] == "0" && parts[1] == "":
			v2Path = parts[2]
		case slices.Contains(strings.Split(parts[1], ","), "memory"):
			v1Path = parts[2]
		}
	}
	// A host that mixes both has the memory controller on v1; "0::" is then only systemd's own tree.
	if v1Path != "" {
		return smallestUpward(root, "sys/fs/cgroup/memory", v1Path, "memory.limit_in_bytes", v1Unlimited)
	}
	if v2Path != "" {
		return smallestUpward(root, "sys/fs/cgroup", v2Path, "memory.max", 1<<63-1)
	}
	return 0, false
}

// smallestUpward reads file in the cgroup at cgroupPath, under mount, and in each of its parents, and
// returns the smallest limit found. A value of unlimited or more, "max" and unreadable files are no
// limit.
func smallestUpward(root, mount, cgroupPath, file string, unlimited int64) (int64, bool) {
	var smallest int64
	for dir := path.Clean("/" + cgroupPath); ; dir = path.Dir(dir) {
		data, err := os.ReadFile(filepath.Join(root, mount, filepath.FromSlash(dir), file)) // #nosec G304 G703 -- the process's own cgroup under the root
		if err == nil {
			if n, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64); err == nil && n > 0 && n < unlimited && (smallest == 0 || n < smallest) {
				smallest = n
			}
		}
		if dir == "/" {
			break
		}
	}
	return smallest, smallest > 0
}
