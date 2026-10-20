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
// names the machine (its hostname).
type CreateDeviceRequest struct {
	Machine string `json:"machine"`
}

// DeviceRequestCreated answers POST /devices (201). The user opens ApproveURL
// (<instance>/devices/<code>); the CLI polls with PollSecret until ExpiresAt (DEV-01).
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
// agent token and the instance URL; later polls carry only the status (DEV-02).
type DevicePoll struct {
	Status DeviceStatus `json:"status"`
	Token  string       `json:"token,omitempty"`
	URL    string       `json:"url,omitempty"`
}

// DeviceRequest is a request as the approval page shows it: GET /devices/{code} and the answer
// of POST /devices/{code}/approve and /deny (WIZ-02).
type DeviceRequest struct {
	Code      string       `json:"code"`
	Machine   string       `json:"machine"`
	IP        string       `json:"ip"`
	Status    DeviceStatus `json:"status"`
	CreatedAt time.Time    `json:"created_at"`
	ExpiresAt time.Time    `json:"expires_at"`
}
