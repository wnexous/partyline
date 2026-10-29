package dto

import "time"

// Secret is one of the owner's named secrets for container AIs (CA-03): the name and its dates,
// never the value. A value goes in and is never returned (CA-08).
type Secret struct {
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// PutSecretRequest is the body of PUT /secrets/{name}. The value is write-only: it is sealed at
// rest and never returned by any route (CA-03, CA-08).
type PutSecretRequest struct {
	Value string `json:"value"`
}
