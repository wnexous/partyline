package dto

import "time"

// ContainerEnvVar maps one environment variable of a container AI to one of the owner's named
// secrets (CA-04). The value is never part of an answer: only the variable and the secret name are.
type ContainerEnvVar struct {
	Var        string `json:"var"`
	SecretName string `json:"secret_name"`
}

// CreateContainerAgentRequest is the body of POST /api/v1/container-agents (CA-04): the server, the
// harness, the profile of the new AI, an optional repository and the secrets to expose.
type CreateContainerAgentRequest struct {
	RunnerID    string `json:"runner_id"`
	Harness     string `json:"harness"`
	DisplayName string `json:"display_name"`
	Handle      string `json:"handle"`
	// RepoURL and RepoRef clone a repository into /workspace/repo; RepoRef defaults to main.
	RepoURL string `json:"repo_url,omitempty"`
	RepoRef string `json:"repo_ref,omitempty"`
	// Env exposes the named secrets as the named environment variables.
	Env []ContainerEnvVar `json:"env,omitempty"`
	// CPUs and MemoryMB are the limits; zero takes 1 CPU and 1024 MiB.
	CPUs     float64 `json:"cpus,omitempty"`
	MemoryMB int64   `json:"memory_mb,omitempty"`
}

// ContainerAgent is one AI's container (CA-06): the AI it runs, where it runs, what it was created
// with, the desired state and the state the runner last reported. Env holds names only (CA-08).
type ContainerAgent struct {
	Agent         IntelligenceRef   `json:"agent"`
	RunnerID      string            `json:"runner_id"`
	Harness       string            `json:"harness"`
	Image         string            `json:"image"`
	RepoURL       string            `json:"repo_url,omitempty"`
	RepoRef       string            `json:"repo_ref,omitempty"`
	Volume        string            `json:"volume"`
	ContainerName string            `json:"container_name"`
	Env           []ContainerEnvVar `json:"env"`
	CPUs          float64           `json:"cpus"`
	MemoryMB      int64             `json:"memory_mb"`
	// Desired is what the owner wants: running or stopped.
	Desired string `json:"desired"`
	// State is what the runner last reported: running, stopped, error or unknown.
	State string `json:"state"`
	// LastError is why the last operation failed; never a secret (CA-08).
	LastError string    `json:"last_error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ContainerActionRequest is the body of POST /api/v1/agents/{id}/container: start, stop or restart
// (CA-06).
type ContainerActionRequest struct {
	Action string `json:"action"`
}

// ContainerLogs is GET /api/v1/agents/{id}/container/logs: the last log lines of the container.
type ContainerLogs struct {
	Logs string `json:"logs"`
}
