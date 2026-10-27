package dto

import "encoding/json"

// MessageQuestionKind is how a wizard question is answered: one option (single) or one or more
// options (multi).
type MessageQuestionKind string

// Wizard question kinds.
const (
	MessageQuestionKindSingle MessageQuestionKind = "single"
	MessageQuestionKindMulti  MessageQuestionKind = "multi"
)

// The limits of a message's interactive part and of its structured answer (QR-03, QR-07). The web
// app imports them through the generated types, so its forms cannot drift from the server.

// MaxQuickReplies is the most quick replies one message carries.
const MaxQuickReplies = 4

// MaxWizardQuestions is the most questions one wizard carries.
const MaxWizardQuestions = 8

// MaxQuestionOptions is the most options one wizard question carries.
const MaxQuestionOptions = 8

// MaxQuestionLabel is the longest quick reply or option label in characters.
const MaxQuestionLabel = 48

// MaxQuestionTitle is the longest wizard question title in characters.
const MaxQuestionTitle = 200

// MaxAnswerText is the longest free-text note of a wizard answer in characters.
const MaxAnswerText = 280

// quickRepliesVersion is the only version of the question and answer documents this build reads
// and writes; a document with another version parses as none (QR-01).
const quickRepliesVersion = 1

// QuickReply is one button of a question: the label the author wrote and the id the server
// assigned in its order (r1..r4, QR-01).
type QuickReply struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// WizardOption is one option of a wizard question, with the id the server assigned in its
// question's order (q1o1..q8o8, QR-02).
type WizardOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// WizardQuestion is one question of a stored wizard: its server id, title, kind, whether it takes
// a free text and its options (QR-02).
type WizardQuestion struct {
	ID        string              `json:"id"`
	Title     string              `json:"title"`
	Kind      MessageQuestionKind `json:"kind"`
	AllowText bool                `json:"allow_text"`
	Options   []WizardOption      `json:"options"`
}

// Wizard is the questions of a stored wizard.
type Wizard struct {
	Questions []WizardQuestion `json:"questions"`
}

// MessageInteractive is the canonical question a message carries, stored in
// messages.interactive_json and returned as Message.Interactive (QR-01, QR-02): at most 4 quick
// replies, or one wizard. V is the version of the document.
type MessageInteractive struct {
	V            int          `json:"v"`
	QuickReplies []QuickReply `json:"quick_replies,omitempty"`
	Wizard       *Wizard      `json:"wizard,omitempty"`
}

// MessageAnswerItem is one answered wizard question in the structured answer: the question's
// title, the chosen labels and the optional text note (QR-07).
type MessageAnswerItem struct {
	Question string   `json:"question"`
	Values   []string `json:"values"`
	Text     string   `json:"text"`
}

// MessageAnswers is the canonical structured answer a reply carries, stored in
// messages.answers_json and returned as Message.Answers (QR-06, QR-07): one quick reply label, or
// one item per answered wizard question. V is the version of the document.
type MessageAnswers struct {
	V          int                 `json:"v"`
	QuickReply string              `json:"quick_reply,omitempty"`
	Answers    []MessageAnswerItem `json:"answers,omitempty"`
}

// WizardRequest is the body part of a post that asks a wizard: the questions and their labels;
// the server assigns every id (QR-03).
type WizardRequest struct {
	Questions []WizardQuestionRequest `json:"questions"`
}

// WizardQuestionRequest is one question of a WizardRequest.
type WizardQuestionRequest struct {
	Title string `json:"title"`
	// Kind defaults to single.
	Kind    MessageQuestionKind `json:"kind,omitempty"`
	Options []string            `json:"options"`
	// AllowText offers an optional free text with the answer.
	AllowText bool `json:"allow_text,omitempty"`
}

// AnswerSubmission is the body part of a reply that answers a question (QR-05..QR-07): the id of
// a quick reply, or one item per wizard question.
type AnswerSubmission struct {
	QuickReply string       `json:"quick_reply,omitempty"`
	Items      []AnswerItem `json:"items,omitempty"`
}

// AnswerItem is the answer to one wizard question, given by the question's id and the ids of the
// options chosen, with an optional text note.
type AnswerItem struct {
	QuestionID string   `json:"question_id"`
	OptionIDs  []string `json:"option_ids,omitempty"`
	Text       string   `json:"text,omitempty"`
}

// ParseMessageInteractive decodes the question stored with a message. It returns nil when the
// message carries none, when the JSON is corrupt and when the document's version is not the one
// this build reads, so a message stored by a future version is read as a plain message (QR-01).
func ParseMessageInteractive(raw string) *MessageInteractive {
	if raw == "" {
		return nil
	}
	var doc MessageInteractive
	if err := json.Unmarshal([]byte(raw), &doc); err != nil || doc.V != quickRepliesVersion {
		return nil
	}
	return &doc
}

// ParseMessageAnswers decodes the structured answer stored with a reply. It returns nil when the
// message carries none, when the JSON is corrupt and when the document's version is not the one
// this build reads (QR-06).
func ParseMessageAnswers(raw string) *MessageAnswers {
	if raw == "" {
		return nil
	}
	var doc MessageAnswers
	if err := json.Unmarshal([]byte(raw), &doc); err != nil || doc.V != quickRepliesVersion {
		return nil
	}
	return &doc
}
