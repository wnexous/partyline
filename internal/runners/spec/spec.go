// Package spec is the protocol between Partyline and `partyline runner`, and the validators both
// sides apply to every value of it (CA-09). It is pure data and pattern matching: the server and
// the CLI import the same types, and nothing here reaches the store, the API or the app.
//
// Every connection starts with a Hello. The server then sends Commands; the runner answers each
// with a Result, and reports container states without being asked with StateEvents.
package spec

// FrameType is the type field of every frame of the protocol.
type FrameType string

// Frame types.
const (
	// FrameHello is the first frame of a connection: Docker and the existing containers (CA-02).
	FrameHello FrameType = "hello"
	// FrameCommand is a server to runner request.
	FrameCommand FrameType = "command"
	// FrameResult is the runner's answer to one Command.
	FrameResult FrameType = "result"
	// FrameState is an unsolicited container state report.
	FrameState FrameType = "state"
)

// Op is one container operation of the protocol.
type Op string

// Operations. The runner executes nothing else (CA-09).
const (
	// OpRun creates and starts a container from a RunSpec.
	OpRun Op = "run"
	// OpStart starts a stopped container.
	OpStart Op = "start"
	// OpStop stops a running container.
	OpStop Op = "stop"
	// OpRestart restarts a container.
	OpRestart Op = "restart"
	// OpRemove removes a container, and with RemoveVolume its workspace volume too.
	OpRemove Op = "remove"
	// OpLogs returns the last log lines of a container.
	OpLogs Op = "logs"
	// OpState returns the state of the runner's container(s).
	OpState Op = "state"
)

// State is a container state as Partyline stores it (container_agents.state, CA-06).
type State string

// Container states; the runner maps Docker's own states onto these four.
const (
	StateRunning State = "running"
	StateStopped State = "stopped"
	StateError   State = "error"
	StateUnknown State = "unknown"
)

// Container is one container the runner reports: its name (always a valid container name) and the
// state Partyline shows.
type Container struct {
	Name  string `json:"name"`
	State State  `json:"state"`
	// Error is why the container is in the error state; empty otherwise.
	Error string `json:"error,omitempty"`
}

// Hello is the first frame of a connection (CA-02). Exactly one of DockerVersion and DockerError
// is set: the version when Docker is usable, the reason when it is not. Containers lists the
// containers the runner already has.
type Hello struct {
	Type          FrameType   `json:"type"`
	DockerVersion string      `json:"docker_version,omitempty"`
	DockerError   string      `json:"docker_error,omitempty"`
	Containers    []Container `json:"containers,omitempty"`
}

// RunSpec is the validated description of a container to create (CA-04, CA-05). Every value here
// has passed ValidateRun on the server and is validated again by the runner.
type RunSpec struct {
	// Name is the container name (^pl-[a-z0-9-]{1,60}$).
	Name string `json:"name"`
	// Image is an agent image, from the default repository or the configured allowlist.
	Image string `json:"image"`
	// Volume is the named volume mounted at /workspace.
	Volume string `json:"volume"`
	// CPUs is the CPU limit in cores, 0.1 to 64.
	CPUs float64 `json:"cpus"`
	// MemoryMB is the memory limit in MiB, 64 to 65536.
	MemoryMB int64 `json:"memory_mb"`
	// Labels are the Docker labels of the container (partyline.agent=<agent id>).
	Labels map[string]string `json:"labels,omitempty"`
	// Env is written to a 0600 env file and passed with --env-file; the values are secrets and are
	// never logged (CA-08).
	Env map[string]string `json:"env,omitempty"`
}

// Command is one request of the server. ID is unique per connection and identifies the Result.
// The fields a command uses depend on Op: Run for run, Name for every per-container operation,
// Tail for logs and RemoveVolume for remove.
type Command struct {
	Type FrameType `json:"type"`
	ID   string    `json:"id"`
	Op   Op        `json:"op"`
	// Run carries the container to create; only OpRun uses it.
	Run *RunSpec `json:"run,omitempty"`
	// Name is the container of the operation; empty on state lists every container.
	Name string `json:"name,omitempty"`
	// Tail is how many log lines OpLogs returns (1 to MaxTail, 0 means DefaultTail).
	Tail int `json:"tail,omitempty"`
	// RemoveVolume asks OpRemove to remove the workspace volume too (CA-06).
	RemoveVolume bool `json:"remove_volume,omitempty"`
}

// Result is the runner's answer to one Command. OK false carries Error and no data; OK true may
// carry Logs (logs) or Containers (state). Every other operation carries nothing.
type Result struct {
	Type       FrameType   `json:"type"`
	ID         string      `json:"id"`
	OK         bool        `json:"ok"`
	Error      string      `json:"error,omitempty"`
	Logs       string      `json:"logs,omitempty"`
	Containers []Container `json:"containers,omitempty"`
}

// StateEvent is an unsolicited report of container states, from `docker events` or the runner's
// poll. The server writes the matching container_agents.state (CA-06).
type StateEvent struct {
	Type       FrameType   `json:"type"`
	Containers []Container `json:"containers"`
}
