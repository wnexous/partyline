package rooms

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/validation"
)

// messageJSON returns the interactive_json and answers_json columns of a message.
func (f *fixture) messageJSON(t *testing.T, id string) (interactive, answers string) {
	t.Helper()
	require.NoError(t, f.raw.QueryRowContext(context.Background(),
		"SELECT interactive_json, answers_json FROM messages WHERE id = ?", id).Scan(&interactive, &answers))
	return interactive, answers
}

// messageReplyTo returns the reply_to column of a message.
func (f *fixture) messageReplyTo(t *testing.T, id string) string {
	t.Helper()
	var replyTo string
	require.NoError(t, f.raw.QueryRowContext(context.Background(),
		"SELECT reply_to FROM messages WHERE id = ?", id).Scan(&replyTo))
	return replyTo
}

// QR-01: a question is stored as one canonical document and read back with the message.
func TestPostAQuestionStoresTheCanonicalDocument(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	r := f.room(t, alice, "ops")
	sub, cancel := f.bus.Subscribe(8)
	defer cancel()

	m, err := f.svc.Post(ctx, alice, r.ID, PostInput{Text: "Deploy?", QuickReplies: []string{"Sim", "Não"}})
	require.NoError(t, err)
	require.NotNil(t, m.Interactive)
	require.Len(t, m.Interactive.QuickReplies, 2)
	require.Equal(t, "r1", m.Interactive.QuickReplies[0].ID)
	require.Equal(t, "Sim", m.Interactive.QuickReplies[0].Label)
	require.Equal(t, "r2", m.Interactive.QuickReplies[1].ID)
	require.Equal(t, "Não", m.Interactive.QuickReplies[1].Label)

	interactive, answers := f.messageJSON(t, m.ID)
	require.Equal(t, `{"v":1,"quick_replies":[{"id":"r1","label":"Sim"},{"id":"r2","label":"Não"}]}`, interactive)
	require.Empty(t, answers)

	var created int
	for _, e := range drain(sub) {
		if _, ok := e.(events.MessageCreated); ok {
			created++
		}
	}
	require.Equal(t, 1, created, "one MessageCreated")
}

// QR-01, QR-02, QR-05: a post that carries more than one shape is refused on answers (or the first
// question field when both question shapes are set) and writes nothing.
func TestOneShapePerPost(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	r := f.room(t, alice, "ops")
	q, err := f.svc.Post(ctx, alice, r.ID, PostInput{Text: "Deploy?", QuickReplies: []string{"Sim", "Não"}})
	require.NoError(t, err)
	before := f.count(t, "SELECT COUNT(*) FROM messages")

	_, err = f.svc.Post(ctx, alice, r.ID, PostInput{
		ReplyTo: q.ID, QuickReplies: []string{"Sim"},
		Answers: &dto.AnswerSubmission{QuickReply: "r1"},
	})
	requireInvalidField(t, err, "answers")
	require.Equal(t, before, f.count(t, "SELECT COUNT(*) FROM messages"), "nothing is stored")

	_, err = f.svc.Post(ctx, alice, r.ID, PostInput{
		Text: "Planning", QuickReplies: []string{"Sim"},
		Wizard: &dto.WizardRequest{Questions: []dto.WizardQuestionRequest{{Title: "Quando?", Options: []string{"Amanhã"}}}},
	})
	requireInvalidField(t, err, "quick_replies")
	require.Equal(t, before, f.count(t, "SELECT COUNT(*) FROM messages"), "nothing is stored")
}

// QR-05: an answer checks that reply_to names a message of the room before it reads the question,
// so a message of another room is the same reply_to error a plain reply gets, never a shape error.
func TestAnAnswerChecksTheRoomFirst(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	bob := f.colleague(t, f.orgOf(t, alice), "bob", orgs.PermissionMember)
	ops := f.room(t, alice, "ops")
	other := f.room(t, alice, "other")
	f.addMember(t, alice, ops.ID, dto.MemberKindUser, bob.UserID)
	foreign, err := f.svc.Post(ctx, alice, other.ID, PostInput{Text: "Deploy?", QuickReplies: []string{"Sim"}})
	require.NoError(t, err)

	_, plainErr := f.svc.Post(ctx, bob, ops.ID, PostInput{ReplyTo: foreign.ID, Text: "quoted"})
	_, answerErr := f.svc.Post(ctx, bob, ops.ID, PostInput{
		ReplyTo: foreign.ID, Answers: &dto.AnswerSubmission{QuickReply: "r1"},
	})
	requireInvalidField(t, plainErr, "reply_to")
	requireInvalidField(t, answerErr, "reply_to")
	require.Equal(t, plainErr.Error(), answerErr.Error(), "the same room error as a plain reply")
}

// QR-05, QR-09: the asker comes before deletion, so the asker cannot answer their own deleted
// question; a deleted target answered by anyone else stays a conflict. A deleted target keeps no
// interactive document (QR-04), so a deleted question and a deleted non-question are identical;
// both stay 409 (see the feature's contract notes).
func TestAnAnswerChecksTheAskerBeforeDeletion(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	bob := f.colleague(t, f.orgOf(t, alice), "bob", orgs.PermissionMember)
	r := f.room(t, alice, "ops")
	f.addMember(t, alice, r.ID, dto.MemberKindUser, bob.UserID)

	question, err := f.svc.Post(ctx, alice, r.ID, PostInput{Text: "Deploy?", QuickReplies: []string{"Sim", "Não"}})
	require.NoError(t, err)
	_, err = f.svc.DeleteMessage(ctx, alice, r.ID, question.ID)
	require.NoError(t, err)
	_, err = f.svc.Post(ctx, alice, r.ID, PostInput{ReplyTo: question.ID, Answers: &dto.AnswerSubmission{QuickReply: "r1"}})
	require.ErrorIs(t, err, access.ErrForbidden, "the asker's own deleted question is 403")

	plain, err := f.svc.Post(ctx, alice, r.ID, PostInput{Text: "plain"})
	require.NoError(t, err)
	_, err = f.svc.DeleteMessage(ctx, alice, r.ID, plain.ID)
	require.NoError(t, err)
	_, err = f.svc.Post(ctx, bob, r.ID, PostInput{ReplyTo: plain.ID, Answers: &dto.AnswerSubmission{QuickReply: "r1"}})
	require.ErrorIs(t, err, ErrMessageDeleted, "a deleted non-question stays 409")
}

// QR-05: an answer is a reply that carries the structured answer and its rendered text.
func TestAnswerAQuickReply(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	bob := f.colleague(t, f.orgOf(t, alice), "bob", orgs.PermissionMember)
	r := f.room(t, alice, "ops")
	f.addMember(t, alice, r.ID, dto.MemberKindUser, bob.UserID)
	q, err := f.svc.Post(ctx, alice, r.ID, PostInput{Text: "Deploy?", QuickReplies: []string{"Sim", "Não"}})
	require.NoError(t, err)
	sub, cancel := f.bus.Subscribe(8)
	defer cancel()

	m, err := f.svc.Post(ctx, bob, r.ID, PostInput{ReplyTo: q.ID, Answers: &dto.AnswerSubmission{QuickReply: "r1"}})
	require.NoError(t, err)
	require.Equal(t, "Sim", m.Text)
	_, answers := f.messageJSON(t, m.ID)
	require.Equal(t, `{"v":1,"quick_reply":"Sim"}`, answers)
	require.Equal(t, q.ID, f.messageReplyTo(t, m.ID))

	var created int
	for _, e := range drain(sub) {
		if _, ok := e.(events.MessageCreated); ok {
			created++
		}
	}
	require.Equal(t, 1, created, "one MessageCreated")
}

// QR-05: a wizard's answers are stored in the question's order and rendered one line each.
func TestAnswerAWizard(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	bob := f.colleague(t, f.orgOf(t, alice), "bob", orgs.PermissionMember)
	r := f.room(t, alice, "ops")
	f.addMember(t, alice, r.ID, dto.MemberKindUser, bob.UserID)
	q, err := f.svc.Post(ctx, alice, r.ID, PostInput{
		Text: "Planning",
		Wizard: &dto.WizardRequest{
			Questions: []dto.WizardQuestionRequest{
				{Title: "Quando?", Options: []string{"Amanhã", "Na sexta"}},
				{Title: "O que entra?", Kind: dto.MessageQuestionKindMulti, Options: []string{"API", "Web"}},
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, q.Interactive)
	require.NotNil(t, q.Interactive.Wizard)
	require.Len(t, q.Interactive.Wizard.Questions, 2)

	m, err := f.svc.Post(ctx, bob, r.ID, PostInput{
		ReplyTo: q.ID,
		Answers: &dto.AnswerSubmission{
			Items: []dto.AnswerItem{
				{QuestionID: "q2", OptionIDs: []string{"q2o1", "q2o2"}},
				{QuestionID: "q1", OptionIDs: []string{"q1o2"}},
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, "Quando?: Na sexta\nO que entra?: API, Web", m.Text)
	_, answers := f.messageJSON(t, m.ID)
	require.JSONEq(t, `{"v":1,"answers":[
		{"question":"Quando?","values":["Na sexta"],"text":""},
		{"question":"O que entra?","values":["API","Web"],"text":""}
	]}`, answers)
}

// QR-05, QR-08, QR-09: the service refuses every answer that breaks a rule.
func TestTheRefusals(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	bob := f.colleague(t, f.orgOf(t, alice), "bob", orgs.PermissionMember)
	r := f.room(t, alice, "ops")
	f.addMember(t, alice, r.ID, dto.MemberKindUser, bob.UserID)
	q, err := f.svc.Post(ctx, alice, r.ID, PostInput{Text: "Deploy?", QuickReplies: []string{"Sim", "Não"}})
	require.NoError(t, err)

	// Alice answers her own question.
	_, err = f.svc.Post(ctx, alice, r.ID, PostInput{ReplyTo: q.ID, Answers: &dto.AnswerSubmission{QuickReply: "r1"}})
	require.ErrorIs(t, err, access.ErrForbidden)

	// Answers without reply_to.
	_, err = f.svc.Post(ctx, bob, r.ID, PostInput{Answers: &dto.AnswerSubmission{QuickReply: "r1"}})
	var verr *validation.Error
	require.ErrorAs(t, err, &verr)
	require.Contains(t, verr.Fields, "answers")

	// Answers to a plain message.
	plain, err := f.svc.Post(ctx, alice, r.ID, PostInput{Text: "plain"})
	require.NoError(t, err)
	_, err = f.svc.Post(ctx, bob, r.ID, PostInput{ReplyTo: plain.ID, Answers: &dto.AnswerSubmission{QuickReply: "r1"}})
	require.ErrorAs(t, err, &verr)
	require.Contains(t, verr.Fields, "answers")

	// Answers with a file.
	_, err = f.svc.Post(ctx, bob, r.ID, PostInput{
		ReplyTo: q.ID, Files: []File{{SHA256: "sha", Name: "f", Mime: "text/plain", Size: 1}},
		Answers: &dto.AnswerSubmission{QuickReply: "r1"},
	})
	require.ErrorAs(t, err, &verr)
	require.Contains(t, verr.Fields, "answers")

	// Answers with a non-empty text.
	_, err = f.svc.Post(ctx, bob, r.ID, PostInput{ReplyTo: q.ID, Text: "extra", Answers: &dto.AnswerSubmission{QuickReply: "r1"}})
	require.ErrorAs(t, err, &verr)
	require.Contains(t, verr.Fields, "text")

	// A deleted question.
	deleted, err := f.svc.Post(ctx, alice, r.ID, PostInput{Text: "Gone?", QuickReplies: []string{"Yes", "No"}})
	require.NoError(t, err)
	_, err = f.svc.DeleteMessage(ctx, alice, r.ID, deleted.ID)
	require.NoError(t, err)
	_, err = f.svc.Post(ctx, bob, r.ID, PostInput{ReplyTo: deleted.ID, Answers: &dto.AnswerSubmission{QuickReply: "r1"}})
	require.ErrorIs(t, err, ErrMessageDeleted)

	// Bob answers twice.
	_, err = f.svc.Post(ctx, bob, r.ID, PostInput{ReplyTo: q.ID, Answers: &dto.AnswerSubmission{QuickReply: "r1"}})
	require.NoError(t, err)
	_, err = f.svc.Post(ctx, bob, r.ID, PostInput{ReplyTo: q.ID, Answers: &dto.AnswerSubmission{QuickReply: "r2"}})
	require.ErrorIs(t, err, ErrAlreadyAnswered)

	// After Bob deletes his answer he answers again.
	var answerID string
	require.NoError(t, f.raw.QueryRowContext(ctx, "SELECT id FROM messages WHERE reply_to = ? AND author_iid = ? AND answers_json <> ''", q.ID, bob.UserID).Scan(&answerID))
	_, err = f.svc.DeleteMessage(ctx, bob, r.ID, answerID)
	require.NoError(t, err)
	_, err = f.svc.Post(ctx, bob, r.ID, PostInput{ReplyTo: q.ID, Answers: &dto.AnswerSubmission{QuickReply: "r2"}})
	require.NoError(t, err)

	// A reaction post with quick replies.
	_, err = f.svc.Post(ctx, alice, r.ID, PostInput{Kind: dto.MessageKindReaction, QuickReplies: []string{"Sim"}})
	require.ErrorAs(t, err, &verr)
	require.Contains(t, verr.Fields, "quick_replies")
}

// QR-04: a question's text changes but its interactive part stays; a delete clears both.
func TestEditAndDeleteAQuestion(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	r := f.room(t, alice, "ops")
	q, err := f.svc.Post(ctx, alice, r.ID, PostInput{Text: "Deploy?", QuickReplies: []string{"Sim", "Não"}})
	require.NoError(t, err)

	// The text changes and edited_at is set while interactive_json is unchanged.
	f.clock.now = t0.Add(time.Hour)
	edited, err := f.svc.EditMessage(ctx, alice, r.ID, q.ID, "Deploy now?")
	require.NoError(t, err)
	require.Equal(t, "Deploy now?", edited.Text)
	require.True(t, edited.EditedAt.Valid)
	interactive, _ := f.messageJSON(t, q.ID)
	require.Equal(t, `{"v":1,"quick_replies":[{"id":"r1","label":"Sim"},{"id":"r2","label":"Não"}]}`, interactive)

	// The delete leaves text "" and interactive_json "".
	deleted, err := f.svc.DeleteMessage(ctx, alice, r.ID, q.ID)
	require.NoError(t, err)
	require.Empty(t, deleted.Text)
	interactive, _ = f.messageJSON(t, q.ID)
	require.Empty(t, interactive)

	// An edit after the delete is ErrMessageDeleted.
	_, err = f.svc.EditMessage(ctx, alice, r.ID, q.ID, "again")
	require.ErrorIs(t, err, ErrMessageDeleted)
}

// QR-04: an answer cannot be edited; deleting it clears its text and structured copy, so the
// author may answer again.
func TestAnAnswerCannotBeEdited(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	bob := f.colleague(t, f.orgOf(t, alice), "bob", orgs.PermissionMember)
	r := f.room(t, alice, "ops")
	f.addMember(t, alice, r.ID, dto.MemberKindUser, bob.UserID)
	q, err := f.svc.Post(ctx, alice, r.ID, PostInput{Text: "Deploy?", QuickReplies: []string{"Sim", "Não"}})
	require.NoError(t, err)
	a, err := f.svc.Post(ctx, bob, r.ID, PostInput{ReplyTo: q.ID, Answers: &dto.AnswerSubmission{QuickReply: "r1"}})
	require.NoError(t, err)

	// Bob's edit is 422 on text.
	_, err = f.svc.EditMessage(ctx, bob, r.ID, a.ID, "No")
	var verr *validation.Error
	require.ErrorAs(t, err, &verr)
	require.Contains(t, verr.Fields, "text")

	// His delete clears the text and answers_json.
	deleted, err := f.svc.DeleteMessage(ctx, bob, r.ID, a.ID)
	require.NoError(t, err)
	require.Empty(t, deleted.Text)
	_, answers := f.messageJSON(t, a.ID)
	require.Empty(t, answers)

	// Answering again succeeds.
	_, err = f.svc.Post(ctx, bob, r.ID, PostInput{ReplyTo: q.ID, Answers: &dto.AnswerSubmission{QuickReply: "r2"}})
	require.NoError(t, err)
}

// QR-09: a DM that holds an external person is history; posting there is refused.
func TestAHistoryOnlyDMRefusesAnAnswer(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	atlas := f.agent(t, alice, "atlas")
	as := agentPrincipal(alice, atlas)
	conn, _ := f.chat(t, alice, "Pat")
	person := store.NewID()
	f.intelligence(t, person, "human", "Pat", "pat")
	f.exec(t, "INSERT INTO people (id, connector_id, external_id, created_at) VALUES (?, ?, 'pat-phone', ?)", person, conn, t0.UnixMilli())
	x, y := atlas, person
	if x > y {
		x, y = y, x
	}
	history := store.NewID()
	f.exec(t, "INSERT INTO rooms (id, owner_id, name, kind, dm_a, dm_b, created_at, updated_at) VALUES (?, ?, '', 'dm', ?, ?, ?, ?)",
		history, alice.UserID, x, y, t0.UnixMilli(), t0.UnixMilli())
	f.exec(t, "INSERT INTO room_members (id, room_id, kind, member_id, added_by, created_at) VALUES (?, ?, 'agent', ?, ?, ?)", store.NewID(), history, atlas, alice.UserID, t0.UnixMilli())

	q := f.plant(t, history, "agent", atlas, atlas, "text", "Deploy?")
	f.exec(t, "UPDATE messages SET interactive_json = ? WHERE id = ?", `{"v":1,"quick_replies":[{"id":"r1","label":"Sim"}]}`, q)

	_, err := f.svc.Post(ctx, as, history, PostInput{ReplyTo: q, Answers: &dto.AnswerSubmission{QuickReply: "r1"}})
	require.ErrorIs(t, err, access.ErrHistoryOnly)
}
