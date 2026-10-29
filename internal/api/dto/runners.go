package dto

import "time"

// Runner is a server that runs container AIs, as GET /runners lists it (CA-01): what it reported
// about Docker and whether it is connected now.
type Runner struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Machine string `json:"machine"`
	// Online is whether the runner holds a live connection right now.
	Online bool `json:"online"`
	// DockerVersion is the Docker version the last hello reported; empty when Docker is unusable.
	DockerVersion string `json:"docker_version,omitempty"`
	// DockerError is why the last hello said Docker is unusable; empty when it is usable.
	DockerError string `json:"docker_error,omitempty"`
	// LastSeenAt is when the runner last connected; absent until its first connection.
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
	// ContainerCount is how many container AIs the runner runs.
	ContainerCount int       `json:"container_count"`
	CreatedAt      time.Time `json:"created_at"`
}

// RegisterRunnerRequest is the body of POST /runner/register, the first call of a connection
// authenticated by a runner token. Name defaults to Machine and is made unique per owner.
type RegisterRunnerRequest struct {
	Name    string `json:"name,omitempty"`
	Machine string `json:"machine"`
}

// RenameRunnerRequest is the body of PATCH /runners/{id} (CA-01).
type RenameRunnerRequest struct {
	Name string `json:"name"`
}
