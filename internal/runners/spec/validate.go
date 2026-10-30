package spec

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Limits and the default agent image.
const (
	// DefaultImageRepo is the repository of the official agent image (design, "Agent image").
	DefaultImageRepo = "ghcr.io/wnexous/partyline-agent"
	// DefaultTail is how many log lines a logs command returns without a tail.
	DefaultTail = 200
	// MaxTail is the largest accepted log tail.
	MaxTail = 1000
	// MaxContainers is the most containers one hello or state event may report.
	MaxContainers = 64
	// MaxEnvVars is the most environment variables one run may pass.
	MaxEnvVars = 64
	// MaxEnvValue is the longest accepted environment variable value in bytes.
	MaxEnvValue = 8192
	// MaxLabels is the most Docker labels one run may set.
	MaxLabels = 16
	// MaxLabelValue is the longest accepted label value in characters.
	MaxLabelValue = 256
	// MaxMessage is the longest accepted Docker error or result error in characters.
	MaxMessage = 500
	// MaxLogs is the largest accepted log body in bytes.
	MaxLogs = 1 << 20
)

var (
	// ContainerNamePattern matches a container name the protocol accepts (CA-09).
	ContainerNamePattern = regexp.MustCompile(`^pl-[a-z0-9-]{1,60}$`)
	// VolumeNamePattern matches a Docker volume name.
	VolumeNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,127}$`)
	// EnvNamePattern matches an environment variable name (CA-09).
	EnvNamePattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)
	// IDPattern matches a command id.
	IDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

	versionPattern  = regexp.MustCompile(`^[0-9][0-9A-Za-z.+_-]{0,63}$`)
	refPattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$`)
	imageRepoRegexp = regexp.MustCompile(`^[a-z0-9]+([._-][a-z0-9]+)*(:[0-9]{1,5})?(/[a-z0-9]+([._-][a-z0-9]+)*)+$`)
	imageTagRegexp  = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$`)
	labelKeyRegexp  = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,127}$`)
	httpsRepoRegexp = regexp.MustCompile(`^https://[A-Za-z0-9][A-Za-z0-9.-]*(:[0-9]{1,5})?(/[A-Za-z0-9._~%+-]+)+/?$`)
	sshRepoRegexp   = regexp.MustCompile(`^ssh://([A-Za-z0-9._-]+@)?[A-Za-z0-9][A-Za-z0-9.-]*(:[0-9]{1,5})?(/[A-Za-z0-9._~%+-]+)+/?$`)
	scpRepoRegexp   = regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9][A-Za-z0-9.-]*:/?[A-Za-z0-9._~%+-]+(/[A-Za-z0-9._~%+-]+)*$`)
)

// ValidationError names each rejected field of a frame and never echoes its value, so a secret
// (an environment variable value) can never end up in an error message (CA-08).
type ValidationError struct {
	Fields map[string]string
}

// Error lists the rejected fields in order.
func (e *ValidationError) Error() string {
	keys := make([]string, 0, len(e.Fields))
	for k := range e.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + ": " + e.Fields[k]
	}
	return strings.Join(parts, "; ")
}

// check turns a map of fields into an error, or nil when it is empty.
func check(fields map[string]string) error {
	if len(fields) == 0 {
		return nil
	}
	return &ValidationError{Fields: fields}
}

// ValidContainerName reports whether name is an accepted container name.
func ValidContainerName(name string) bool { return ContainerNamePattern.MatchString(name) }

// ValidVolumeName reports whether name is an accepted volume name.
func ValidVolumeName(name string) bool { return VolumeNamePattern.MatchString(name) }

// ValidEnvName reports whether name is an accepted environment variable name.
func ValidEnvName(name string) bool { return EnvNamePattern.MatchString(name) }

// ValidDockerVersion reports whether v is a Docker version as a hello reports it (CA-02).
func ValidDockerVersion(v string) bool { return versionPattern.MatchString(v) }

// ValidState reports whether s is a state Partyline stores.
func ValidState(s State) bool {
	switch s {
	case StateRunning, StateRestarting, StateStopped, StateError, StateUnknown:
		return true
	}
	return false
}

// ValidImageRepo reports whether repo is a repository an image may come from: lowercase path
// segments with an optional registry port, e.g. ghcr.io/wnexous/partyline-agent.
func ValidImageRepo(repo string) bool { return len(repo) <= 256 && imageRepoRegexp.MatchString(repo) }

// ValidImage reports whether image is an agent image: repository:tag, where the repository is the
// default one or one of the configured overrides (allow).
func ValidImage(image string, allow []string) bool {
	repo, tag, ok := splitImage(image)
	if !ok || !ValidImageRepo(repo) || !imageTagRegexp.MatchString(tag) {
		return false
	}
	if repo == DefaultImageRepo {
		return true
	}
	for _, a := range allow {
		if repo == a {
			return true
		}
	}
	return false
}

// splitImage splits image at the last colon after the last slash, so a registry port is part of
// the repository.
func splitImage(image string) (repo, tag string, ok bool) {
	i := strings.LastIndex(image, ":")
	if i < 0 || i < strings.LastIndex(image, "/") {
		return "", "", false
	}
	return image[:i], image[i+1:], true
}

// ValidRepoURL reports whether url is an https or ssh git repository URL (CA-09). http, userinfo
// in an https URL and any path segment ".." are refused.
func ValidRepoURL(url string) bool {
	if url == "" || len(url) > 512 {
		return false
	}
	switch {
	case strings.HasPrefix(url, "https://"):
		return httpsRepoRegexp.MatchString(url) && safeRepoPath(url)
	case strings.HasPrefix(url, "ssh://"):
		return sshRepoRegexp.MatchString(url) && safeRepoPath(url)
	case strings.Contains(url, "://"):
		return false
	}
	return scpRepoRegexp.MatchString(url) && safeRepoPath(url)
}

// safeRepoPath refuses a URL with ".." in it.
func safeRepoPath(url string) bool {
	for _, part := range strings.Split(url, "/") {
		if part == ".." {
			return false
		}
	}
	return true
}

// ValidRef reports whether ref is an accepted git branch, tag or commit: a refname without the
// sequences git refuses ("..", "@{", "//", a leading "-", a trailing "/" or ".", an empty segment
// or a ".lock" segment).
func ValidRef(ref string) bool {
	if !refPattern.MatchString(ref) {
		return false
	}
	if strings.Contains(ref, "..") || strings.Contains(ref, "@{") || strings.HasSuffix(ref, "/") ||
		strings.HasSuffix(ref, ".") || strings.Contains(ref, "//") {
		return false
	}
	for _, part := range strings.Split(ref, "/") {
		if part == "" || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return true
}

// ValidCPUs reports whether cpus is inside the accepted range, 0.1 to 64 cores.
func ValidCPUs(cpus float64) bool { return cpus >= 0.1 && cpus <= 64 }

// ValidMemoryMB reports whether mb is inside the accepted range, 64 to 65536 MiB.
func ValidMemoryMB(mb int64) bool { return mb >= 64 && mb <= 65536 }

// maxRunes reports whether s has at most n characters.
func maxRunes(s string, n int) bool { return utf8.RuneCountInString(s) <= n }

// hasControl reports whether s carries a control character other than the tab and the line feed,
// which Docker error messages may contain.
func hasControl(s string) bool {
	for _, r := range s {
		if r == '\t' || r == '\n' {
			continue
		}
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// ValidMessage reports whether s is an acceptable Docker error or result error: not empty, at most
// MaxMessage characters, no control characters besides tab and line feed.
func ValidMessage(s string) bool {
	return s != "" && maxRunes(s, MaxMessage) && !hasControl(s)
}

// ValidateHello checks a hello frame.
func ValidateHello(h Hello) error {
	fields := map[string]string{}
	if h.Type != FrameHello {
		fields["type"] = "must be hello"
	}
	switch {
	case h.DockerVersion == "" && h.DockerError == "":
		fields["docker_version"] = "one of docker_version and docker_error is required"
	case h.DockerVersion != "" && h.DockerError != "":
		fields["docker_error"] = "only one of docker_version and docker_error is allowed"
	case h.DockerError != "" && !ValidMessage(h.DockerError):
		fields["docker_error"] = fmt.Sprintf("must have 1 to %d characters without control characters", MaxMessage)
	case h.DockerVersion != "" && !ValidDockerVersion(h.DockerVersion):
		fields["docker_version"] = "must be a Docker version"
	}
	if err := checkContainers(h.Containers); err != nil {
		fields["containers"] = err.Error()
	}
	return check(fields)
}

// ValidateContainers checks a list of reported containers.
func ValidateContainers(cs []Container) error { return checkContainers(cs) }

// checkContainers validates the container list of a hello, a state event or a state result.
func checkContainers(cs []Container) error {
	if len(cs) > MaxContainers {
		return fmt.Errorf("at most %d containers", MaxContainers)
	}
	fields := map[string]string{}
	seen := make(map[string]bool, len(cs))
	for i, c := range cs {
		if !ValidContainerName(c.Name) {
			fields[fmt.Sprintf("containers[%d].name", i)] = "must match " + ContainerNamePattern.String()
			continue
		}
		if seen[c.Name] {
			fields[fmt.Sprintf("containers[%d].name", i)] = "is reported twice"
		}
		seen[c.Name] = true
		if !ValidState(c.State) {
			fields[fmt.Sprintf("containers[%d].state", i)] = "must be running, restarting, stopped, error or unknown"
		}
		if c.Error != "" && !ValidMessage(c.Error) {
			fields[fmt.Sprintf("containers[%d].error", i)] = fmt.Sprintf("must have 1 to %d characters without control characters", MaxMessage)
		}
		if c.LastLog != "" && !ValidMessage(c.LastLog) {
			fields[fmt.Sprintf("containers[%d].last_log", i)] = fmt.Sprintf("must have 1 to %d characters without control characters", MaxMessage)
		}
	}
	return check(fields)
}

// ValidateCommand checks a command before it is sent to a runner, and a runner checks it again
// before it executes anything (CA-09). Type is not checked: the server sets it when it sends.
func ValidateCommand(c Command, allow []string) error {
	fields := map[string]string{}
	if !IDPattern.MatchString(c.ID) {
		fields["id"] = "must be 1 to 64 letters, digits, '-' or '_'"
	}
	switch c.Op {
	case OpRun:
		if c.Run == nil {
			fields["run"] = "is required for run"
			break
		}
		if err := ValidateRun(*c.Run, allow); err != nil {
			var verr *ValidationError
			if errors.As(err, &verr) {
				for k, v := range verr.Fields {
					fields["run."+k] = v
				}
			} else {
				fields["run"] = err.Error()
			}
		}
		if c.Name != "" {
			fields["name"] = "is not allowed for run"
		}
		if c.Tail != 0 {
			fields["tail"] = "is only allowed for logs"
		}
		if c.RemoveVolume {
			fields["remove_volume"] = "is only allowed for remove"
		}
	case OpStart, OpStop, OpRestart, OpRemove, OpLogs:
		if !ValidContainerName(c.Name) {
			fields["name"] = "must match " + ContainerNamePattern.String()
		}
		if c.Run != nil {
			fields["run"] = "is only allowed for run"
		}
		if c.Op == OpLogs {
			if c.Tail < 0 || c.Tail > MaxTail {
				fields["tail"] = fmt.Sprintf("must be between 0 and %d", MaxTail)
			}
		} else if c.Tail != 0 {
			fields["tail"] = "is only allowed for logs"
		}
		if c.Op != OpRemove && c.RemoveVolume {
			fields["remove_volume"] = "is only allowed for remove"
		}
	case OpState:
		if c.Name != "" && !ValidContainerName(c.Name) {
			fields["name"] = "must be empty or match " + ContainerNamePattern.String()
		}
		if c.Run != nil {
			fields["run"] = "is only allowed for run"
		}
		if c.Tail != 0 {
			fields["tail"] = "is only allowed for logs"
		}
		if c.RemoveVolume {
			fields["remove_volume"] = "is only allowed for remove"
		}
	default:
		fields["op"] = "must be one of run, start, stop, restart, remove, logs, state"
	}
	return check(fields)
}

// ValidateRun checks the description of a container to create.
func ValidateRun(r RunSpec, allow []string) error {
	fields := map[string]string{}
	if !ValidContainerName(r.Name) {
		fields["name"] = "must match " + ContainerNamePattern.String()
	}
	if !ValidImage(r.Image, allow) {
		fields["image"] = "must be " + DefaultImageRepo + ":<tag> or an allowed image"
	}
	if !ValidVolumeName(r.Volume) {
		fields["volume"] = "must match " + VolumeNamePattern.String()
	}
	if !ValidCPUs(r.CPUs) {
		fields["cpus"] = "must be between 0.1 and 64"
	}
	if !ValidMemoryMB(r.MemoryMB) {
		fields["memory_mb"] = "must be between 64 and 65536"
	}
	if len(r.Labels) > MaxLabels {
		fields["labels"] = fmt.Sprintf("at most %d labels", MaxLabels)
	}
	for k, v := range r.Labels {
		if !labelKeyRegexp.MatchString(k) {
			fields["labels"] = "label keys must be lowercase names of letters, digits, '.', '_' and '-'"
			break
		}
		if v == "" || !maxRunes(v, MaxLabelValue) || hasControl(v) {
			fields["labels"] = fmt.Sprintf("label values must have 1 to %d characters without control characters", MaxLabelValue)
			break
		}
	}
	if len(r.Env) > MaxEnvVars {
		fields["env"] = fmt.Sprintf("at most %d environment variables", MaxEnvVars)
	}
	for name, value := range r.Env {
		if !ValidEnvName(name) {
			fields["env"] = "environment variable names must match " + EnvNamePattern.String()
			break
		}
		if len(value) > MaxEnvValue || strings.ContainsRune(value, 0) {
			// The value never appears in the error: it may be a secret (CA-08).
			fields["env."+name] = fmt.Sprintf("must have at most %d bytes and no NUL", MaxEnvValue)
			break
		}
	}
	return check(fields)
}

// ValidateResult checks a result frame read from a runner.
func ValidateResult(r Result) error {
	fields := map[string]string{}
	if r.Type != FrameResult {
		fields["type"] = "must be result"
	}
	if !IDPattern.MatchString(r.ID) {
		fields["id"] = "must be 1 to 64 letters, digits, '-' or '_'"
	}
	if !r.OK {
		if !ValidMessage(r.Error) {
			fields["error"] = fmt.Sprintf("must have 1 to %d characters without control characters", MaxMessage)
		}
	} else if r.Error != "" {
		fields["error"] = "is only allowed when ok is false"
	}
	if len(r.Logs) > MaxLogs {
		fields["logs"] = fmt.Sprintf("at most %d bytes", MaxLogs)
	}
	if err := checkContainers(r.Containers); err != nil {
		fields["containers"] = err.Error()
	}
	return check(fields)
}

// ValidateStateEvent checks an unsolicited state report.
func ValidateStateEvent(e StateEvent) error {
	fields := map[string]string{}
	if e.Type != FrameState {
		fields["type"] = "must be state"
	}
	if err := checkContainers(e.Containers); err != nil {
		fields["containers"] = err.Error()
	}
	return check(fields)
}
