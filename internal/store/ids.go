package store

import "github.com/google/uuid"

// NewID returns a new UUIDv7 string, the format of every primary key.
func NewID() string {
	return uuid.Must(uuid.NewV7()).String()
}
