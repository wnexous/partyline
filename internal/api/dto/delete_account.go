package dto

// DeleteAccountRequest is the body of DELETE /me: the current password proves it is the person who
// deletes their own account (POS-06).
type DeleteAccountRequest struct {
	Password string `json:"password"`
}
