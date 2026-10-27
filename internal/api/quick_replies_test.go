package api_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

// Questions and answers over the room routes (QR-01, QR-05..QR-07, QR-18): a post carries the
// question and every read of that message carries it back, an answer is a reply with its rendered
// text and its structured copy, and the refusals name the design's status and code. The rules
// themselves are tested in package rooms; here only the contract of the two room routes.

// quickRepliesFixture is a group "ops" of alice with bob in it, and alice's AI "atlas" as a member
// with an agent token of its own, so the person route and the agent route share the same room.
type quickRepliesFixture struct {
	env        *testkit.Env
	alice, bob *testkit.Client
	aiID       string
	ai         *testkit.Client
	room       dto.Room
}

func newQuickRepliesFixture(t *testing.T) *quickRepliesFixture {
	t.Helper()
	env := testkit.New(t)
	alice, bob := env.NewUser("alice"), env.NewUser("bob")
	f := &quickRepliesFixture{env: env, alice: alice, bob: bob}
	f.aiID = env.NewAI(alice, "atlas")
	f.room = createRoom(alice, "ops")
	addToRoom(t, env, alice, f.room.ID, bob)
	alice.Post("/api/v1/rooms/"+f.room.ID+"/members",
		dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: f.aiID}).RequireStatus(http.StatusCreated)
	f.ai = alice.Token(dto.TokenKindAgent, "laptop").AsAgent("atlas")
	return f
}

// messagesPath is the person route that posts a message and lists the room's messages.
func (f *quickRepliesFixture) messagesPath() string {
	return "/api/v1/rooms/" + f.room.ID + "/messages"
}

// post posts body to the room as c and returns the answer.
func (f *quickRepliesFixture) post(t *testing.T, c *testkit.Client, body dto.PostMessageRequest) *testkit.Response {
	t.Helper()
	return c.Post(f.messagesPath(), body)
}

// question posts a quick reply question as c and returns the stored message.
func (f *quickRepliesFixture) question(t *testing.T, c *testkit.Client, labels ...string) dto.Message {
	t.Helper()
	return testkit.Decode[dto.Message](f.post(t, c, dto.PostMessageRequest{Text: "Deploy?", QuickReplies: labels}).
		RequireStatus(http.StatusCreated))
}

// questions builds n wizard questions, the first one single and the rest multiple choice.
func questions(n int) *dto.WizardRequest {
	req := &dto.WizardRequest{Questions: make([]dto.WizardQuestionRequest, n)}
	for i := range req.Questions {
		kind := dto.MessageQuestionKindSingle
		if i > 0 {
			kind = dto.MessageQuestionKindMulti
		}
		req.Questions[i] = dto.WizardQuestionRequest{
			Title:   fmt.Sprintf("Question %d?", i+1),
			Kind:    kind,
			Options: []string{"a", "b"},
		}
	}
	return req
}

// QR-01: the owner posts quick replies and both the answer of the post and every read of the message
// carry the canonical question, with the ids the server assigned in the order given.
func TestPostAQuestionOverTheAPI(t *testing.T) {
	f := newQuickRepliesFixture(t)
	posted := testkit.Decode[dto.Message](f.post(t, f.alice, dto.PostMessageRequest{
		Text: "Deploy?", QuickReplies: []string{"Sim", "Não"},
	}).RequireStatus(http.StatusCreated))
	require.Equal(t, "Deploy?", posted.Text)
	require.NotNil(t, posted.Interactive)
	require.Equal(t, 1, posted.Interactive.V)
	require.Equal(t, []dto.QuickReply{{ID: "r1", Label: "Sim"}, {ID: "r2", Label: "Não"}},
		posted.Interactive.QuickReplies)
	require.Nil(t, posted.Interactive.Wizard)
	require.Nil(t, posted.Answers)

	page := testkit.Decode[dto.Page[dto.Message]](f.alice.Get(f.messagesPath()).RequireStatus(http.StatusOK))
	require.Len(t, page.Items, 1)
	require.Equal(t, posted.ID, page.Items[0].ID)
	require.Equal(t, posted.Interactive, page.Items[0].Interactive)
	require.Equal(t, "Deploy?", page.Items[0].Text)
}

// QR-02: a wizard comes back with its questions, their kinds and the ids of their options.
func TestPostAWizardOverTheAPI(t *testing.T) {
	f := newQuickRepliesFixture(t)
	posted := testkit.Decode[dto.Message](f.post(t, f.alice, dto.PostMessageRequest{
		Text: "Planejar",
		Wizard: &dto.WizardRequest{Questions: []dto.WizardQuestionRequest{
			{Title: "Quando?", Options: []string{"Amanhã", "Na sexta"}},
			{Title: "O que entra?", Kind: dto.MessageQuestionKindMulti, Options: []string{"API", "Web"}, AllowText: true},
		}},
	}).RequireStatus(http.StatusCreated))
	require.NotNil(t, posted.Interactive)
	require.NotNil(t, posted.Interactive.Wizard)
	wizard := posted.Interactive.Wizard.Questions
	require.Len(t, wizard, 2)
	require.Equal(t, dto.MessageQuestionKindMulti, wizard[1].Kind)
	require.Equal(t, []string{"q2o1", "q2o2"}, []string{wizard[1].Options[0].ID, wizard[1].Options[1].ID})
}

// QR-05, QR-06: another member answers with a quick reply, gets "Sim" as the text and the label as
// the structured answer, may answer a question only once (409 already_answered), and a plain reply
// carries no answer at all.
func TestAnswerOverTheAPI(t *testing.T) {
	f := newQuickRepliesFixture(t)
	question := f.question(t, f.alice, "Sim", "Não", "Depois")
	answerBody := dto.PostMessageRequest{ReplyTo: question.ID, Answers: &dto.AnswerSubmission{QuickReply: "r1"}}
	answer := testkit.Decode[dto.Message](f.post(t, f.bob, answerBody).RequireStatus(http.StatusCreated))
	require.Equal(t, "Sim", answer.Text)
	require.Equal(t, question.ID, answer.ReplyTo)
	require.NotNil(t, answer.Answers)
	require.Equal(t, "Sim", answer.Answers.QuickReply)
	require.Nil(t, answer.Interactive)

	err := f.post(t, f.bob, answerBody).RequireError(http.StatusConflict, dto.ErrorCodeAlreadyAnswered)
	require.Equal(t, "already_answered", string(err.Code))

	var plain map[string]json.RawMessage
	f.post(t, f.bob, dto.PostMessageRequest{ReplyTo: question.ID, Text: "combinado"}).
		RequireStatus(http.StatusCreated).Decode(&plain)
	require.NotContains(t, plain, "answers")
}

// QR-03, QR-05: the question limits answer 422 invalid naming their field, an answer that does not
// match the stored question answers 422 on answers, and the asker cannot answer their own question.
func TestQuestionValidationOverTheAPI(t *testing.T) {
	f := newQuickRepliesFixture(t)
	post := func(body dto.PostMessageRequest) *testkit.Response { return f.post(t, f.alice, body) }

	err := post(dto.PostMessageRequest{Text: "Deploy?", QuickReplies: []string{"a", "b", "c", "d", "e"}}).
		RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Contains(t, err.Fields, "quick_replies")

	err = post(dto.PostMessageRequest{Text: "Planejar", Wizard: questions(dto.MaxWizardQuestions + 1)}).
		RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Contains(t, err.Fields, "wizard")

	err = post(dto.PostMessageRequest{Text: "Deploy?", QuickReplies: []string{"Sim"}, Wizard: questions(1)}).
		RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Contains(t, err.Fields, "quick_replies")

	question := f.question(t, f.alice, "Sim", "Não")
	err = f.post(t, f.bob, dto.PostMessageRequest{
		ReplyTo: question.ID, Answers: &dto.AnswerSubmission{QuickReply: "r9"},
	}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Contains(t, err.Fields, "answers")

	f.post(t, f.alice, dto.PostMessageRequest{
		ReplyTo: question.ID, Answers: &dto.AnswerSubmission{QuickReply: "r1"},
	}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)

	// Nothing of the refused questions was stored: the room holds the one question and no answer.
	page := testkit.Decode[dto.Page[dto.Message]](f.alice.Get(f.messagesPath()).RequireStatus(http.StatusOK))
	require.Len(t, page.Items, 1)
}

// QR-09: an answer to a deleted question is 409 conflict, not a stale answer.
func TestAnswersToADeletedQuestionAreRefused(t *testing.T) {
	f := newQuickRepliesFixture(t)
	question := f.question(t, f.alice, "Sim", "Não")
	f.alice.Delete(f.messagesPath() + "/" + question.ID).RequireStatus(http.StatusOK)
	f.post(t, f.bob, dto.PostMessageRequest{
		ReplyTo: question.ID, Answers: &dto.AnswerSubmission{QuickReply: "r1"},
	}).RequireError(http.StatusConflict, dto.ErrorCodeConflict)
}

// QR-18: the agent room route takes the same fields, so an AI asks the question and the asking AI is
// the one member that may not answer it.
func TestTheAgentRouteTakesTheSameFields(t *testing.T) {
	f := newQuickRepliesFixture(t)
	posted := testkit.Decode[dto.Message](f.ai.Post("/api/v1/agent/rooms/"+f.room.ID+"/messages",
		dto.PostMessageRequest{Text: "Deploy?", QuickReplies: []string{"Sim", "Não"}}).RequireStatus(http.StatusCreated))
	require.NotNil(t, posted.Interactive)
	require.Equal(t, []dto.QuickReply{{ID: "r1", Label: "Sim"}, {ID: "r2", Label: "Não"}},
		posted.Interactive.QuickReplies)

	f.ai.Post("/api/v1/agent/rooms/"+f.room.ID+"/messages",
		dto.PostMessageRequest{ReplyTo: posted.ID, Answers: &dto.AnswerSubmission{QuickReply: "r1"}}).
		RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
}

// QR-20: a send to a chat of a connection numbers the options after the text, because a chat renders
// no buttons, and the send keeps what was sent, so a retry sends the same numbered text. The label
// rules are the question's own: a set the server refuses is 422 on quick_replies and sends nothing.
func TestConnectionSendNumbersTheOptions(t *testing.T) {
	f := newReplyFixture(t)
	send := func(body dto.SendToChatRequest) *testkit.Response {
		return f.token.AsAgent("Atlas").Post(connectionChatPath(f.conn.ID, f.group.ID), body)
	}

	for name, labels := range map[string][]string{
		"five labels":    {"a", "b", "c", "d", "e"},
		"a long label":   {strings.Repeat("a", dto.MaxQuestionLabel+1)},
		"the same twice": {"a", "A"},
		"an empty label": {""},
	} {
		err := send(dto.SendToChatRequest{Text: "Deploy?", QuickReplies: labels}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
		require.Contains(t, err.Fields, "quick_replies", name)
	}
	require.Empty(t, f.inst.Sent(), "nothing was sent")
	require.Zero(t, countRows(t, f.env, "SELECT COUNT(*) FROM platform_sends"), "nothing was stored")

	got := testkit.Decode[dto.PlatformSendResult](send(dto.SendToChatRequest{
		Text: "Deploy?", QuickReplies: []string{"Sim", "Não"},
	}).RequireStatus(http.StatusCreated))
	require.Equal(t, dto.PlatformSendStatusSent, got.Status)
	sent := f.inst.WaitSend(t)
	require.Equal(t, "Deploy?\n\n1. Sim\n2. Não", sent.Text)
	require.Empty(t, f.sendRow(t, got.ID).Text, "the text is cleared once the send is over")

	// A platform that does not take it now keeps the numbered text, so the retry sends the same.
	f.inst.SetSendError(errors.New("platform unavailable"))
	pending := testkit.Decode[dto.PlatformSendResult](send(dto.SendToChatRequest{
		Text: "Deploy?", QuickReplies: []string{"Sim", "Não"},
	}).RequireStatus(http.StatusAccepted))
	require.Equal(t, dto.PlatformSendStatusPending, pending.Status)
	require.Equal(t, "Deploy?\n\n1. Sim\n2. Não", f.sendRow(t, pending.ID).Text)
}
