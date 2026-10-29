package dto

import "time"

// DeviceStatus is the state of a device approval request (DEV-01..03).
type DeviceStatus string

// Device request states. A pending request past its expiry reads as expired.
const (
	DeviceStatusPending  DeviceStatus = "pending"
	DeviceStatusApproved DeviceStatus = "approved"
	DeviceStatusDenied   DeviceStatus = "denied"
	DeviceStatusExpired  DeviceStatus = "expired"
)

// CreateDeviceRequest is the body of POST /devices (anonymous): `partyline setup --browser`
// names the machine (its hostname). Kind is the token the approval creates: agent (the default)
// for setup, runner for `partyline runner` (CA-01).
type CreateDeviceRequest struct {
	Machine string    `json:"machine"`
	Kind    TokenKind `json:"kind,omitempty"`
}

// DeviceRequestCreated answers POST /devices (201). The user opens ApproveURL
// (<instance>/devices, without the code) and types Code there; the CLI polls with PollSecret
// until ExpiresAt (DEV-01).
type DeviceRequestCreated struct {
	Code       string    `json:"code"`
	PollSecret string    `json:"poll_secret"`
	ApproveURL string    `json:"approve_url"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// DevicePollRequest is the body of POST /devices/poll (anonymous, at most one per 2 s).
type DevicePollRequest struct {
	PollSecret string `json:"poll_secret"`
}

// DevicePoll answers POST /devices/poll. The poll that first sees the approval carries the new
// agent token, the instance URL and the approver's username; later polls carry only the
// status (DEV-02, DEV-06).
type DevicePoll struct {
	Status     DeviceStatus `json:"status"`
	Token      string       `json:"token,omitempty"`
	URL        string       `json:"url,omitempty"`
	ApprovedBy string       `json:"approved_by,omitempty"`
}

// DeviceCode is the body of POST /devices/lookup, /devices/approve and /devices/deny: the
// code the user typed from the terminal (WIZ-02). It never travels in a URL.
type DeviceCode struct {
	Code string `json:"code"`
}

// DeviceRequest is a request as the approval page shows it: the answer of POST
// /devices/lookup, /devices/approve and /devices/deny (WIZ-02).
type DeviceRequest struct {
	Code      string       `json:"code"`
	Machine   string       `json:"machine"`
	Kind      TokenKind    `json:"kind"`
	IP        string       `json:"ip"`
	Status    DeviceStatus `json:"status"`
	CreatedAt time.Time    `json:"created_at"`
	ExpiresAt time.Time    `json:"expires_at"`
}
