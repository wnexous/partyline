package dto

// Secret requests (SR-01..SR-13): an AI asks for a named secret with a message the web app shows as a
// card with a password field; a person who manages the AI pastes the value, which the server hands once,
// in memory, to the live session that asked. The value is never stored, logged, published or returned.

// SecretNamePattern is the regular expression a secret's name matches (SR-01): an environment variable
// name in upper case, at most 64 characters. It is also the name of the file the session writes, so it
// can never be a path.
const SecretNamePattern = "^[A-Z][A-Z0-9_]{0,63}$" // #nosec G101 -- a pattern of names, no credential

// MaxSecretReason is the longest text of a secret request in characters: why the AI needs it (SR-01).
const MaxSecretReason = 500

// MaxSecretValue is the largest secret value in bytes (SR-05); any characters, newlines included.
const MaxSecretValue = 16384

// MessageSecret is the secret a message asks for, the interactive part of a secret request
// (`{"v":1,"secret":{"name":NAME}}`, SR-01). Only an AI posts one (SR-03).
type MessageSecret struct {
	Name string `json:"name"`
}

// MessageSecretAnswer is the structured answer of a saved secret (`{"v":1,"secret":{"name":NAME,
// "saved":true}}`, SR-06): the name and that the session saved it, never the value. The answer is a
// reply to the request whose author is the person who gave it.
type MessageSecretAnswer struct {
	Name  string `json:"name"`
	Saved bool   `json:"saved"`
}

// GiveSecretRequest is the body of POST /rooms/{id}/messages/{mid}/secret (SR-05): the value, 1 to
// MaxSecretValue bytes. Only a person who manages the asking AI may send it; the answer is the reply
// "🔑 NAME saved", never the value (SR-06, SR-07).
type GiveSecretRequest struct {
	Value string `json:"value"`
}

// AgentSecretFrame hands a secret to the live session that asked for it (SR-06): once, in memory. Only
// a client of agents.MinSecretClientVersion or later is sent one (SR-10). The session writes Value to
// <user config dir>/partyline/secrets/<ai id>/<Name> and answers with an AgentSecretAckFrame carrying
// the same RequestID (SR-08). It is never acknowledged as a message, never replayed and never shown to
// the harness.
type AgentSecretFrame struct {
	Type AgentFrameType `json:"type" tstype:"typeof AgentFrameTypeSecret"`
	// RequestID pairs the frame with its acknowledgement.
	RequestID string `json:"request_id"`
	// MessageID is the secret request the value answers; RoomID its room.
	MessageID string `json:"message_id"`
	RoomID    string `json:"room_id"`
	Name      string `json:"name"`
	Value     string `json:"value"`
}

// AgentSecretAckFrame answers an AgentSecretFrame (SR-08): OK with the absolute Path of the file the
// session wrote, or not OK with the reason in Error, which never holds the value.
type AgentSecretAckFrame struct {
	Type      AgentFrameType `json:"type" tstype:"typeof AgentFrameTypeSecretAck"`
	RequestID string         `json:"request_id"`
	OK        bool           `json:"ok"`
	Path      string         `json:"path,omitempty"`
	Error     string         `json:"error,omitempty"`
}
