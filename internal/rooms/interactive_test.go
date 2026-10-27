package rooms

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/validation"
)

// requireInvalidField asserts err is a validation error that names field.
func requireInvalidField(t *testing.T, err error, field string) {
	t.Helper()
	var verr *validation.Error
	require.ErrorAs(t, err, &verr)
	require.Contains(t, verr.Fields, field)
}

// QR-01, QR-03: quick replies get server ids in order, trimmed labels, the design's limits, and one
// field error per rejected set.
func TestQuestionQuickReplies(t *testing.T) {
	q, err := questionFrom([]string{" Sim ", "Não"}, nil)
	require.NoError(t, err)
	require.Equal(t, 1, q.V)
	require.Equal(t, []dto.QuickReply{{ID: "r1", Label: "Sim"}, {ID: "r2", Label: "Não"}}, q.QuickReplies)
	require.Nil(t, q.Wizard)
	raw, err := json.Marshal(q)
	require.NoError(t, err)
	require.Equal(t, `{"v":1,"quick_replies":[{"id":"r1","label":"Sim"},{"id":"r2","label":"Não"}]}`, string(raw))

	most := make([]string, dto.MaxQuickReplies)
	for i := range most {
		most[i] = fmt.Sprintf("Opção %d", i+1)
	}
	q, err = questionFrom(most, nil)
	require.NoError(t, err)
	require.Len(t, q.QuickReplies, dto.MaxQuickReplies)
	require.Equal(t, "r4", q.QuickReplies[3].ID)

	// The longest accepted label is exactly MaxQuestionLabel characters.
	longest := strings.Repeat("a", dto.MaxQuestionLabel)
	q, err = questionFrom([]string{longest}, nil)
	require.NoError(t, err)
	require.Equal(t, longest, q.QuickReplies[0].Label)

	_, err = questionFrom(append(most, "Mais"), nil)
	requireInvalidField(t, err, "quick_replies")

	refused := map[string][]string{
		"no labels":  {},
		"empty":      {""},
		"blank":      {"   "},
		"too long":   {strings.Repeat("a", dto.MaxQuestionLabel+1)},
		"two lines":  {"a\nb"},
		"duplicated": {"sim", "SIM"},
	}
	for name, labels := range refused {
		t.Run(name, func(t *testing.T) {
			_, err := questionFrom(labels, nil)
			requireInvalidField(t, err, "quick_replies")
		})
	}
}

// QR-02, QR-03: a wizard gets q1..q8 and q1o1..q1o8, single and allow_text defaults, and the
// design's limits.
func TestQuestionWizard(t *testing.T) {
	request := func() *dto.WizardRequest {
		return &dto.WizardRequest{Questions: []dto.WizardQuestionRequest{
			{Title: " Quando? ", Options: []string{" Amanhã ", "Na sexta"}},
			{Title: "O que entra?", Kind: dto.MessageQuestionKindMulti, AllowText: true, Options: []string{"API", "Web"}},
		}}
	}
	q, err := questionFrom(nil, request())
	require.NoError(t, err)
	require.Equal(t, 1, q.V)
	require.Nil(t, q.QuickReplies)
	require.Len(t, q.Wizard.Questions, 2)
	first := q.Wizard.Questions[0]
	require.Equal(t, "q1", first.ID)
	require.Equal(t, "Quando?", first.Title)
	require.Equal(t, dto.MessageQuestionKindSingle, first.Kind)
	require.False(t, first.AllowText)
	require.Equal(t, []dto.WizardOption{{ID: "q1o1", Label: "Amanhã"}, {ID: "q1o2", Label: "Na sexta"}}, first.Options)
	second := q.Wizard.Questions[1]
	require.Equal(t, "q2", second.ID)
	require.Equal(t, dto.MessageQuestionKindMulti, second.Kind)
	require.True(t, second.AllowText)
	require.Equal(t, []dto.WizardOption{{ID: "q2o1", Label: "API"}, {ID: "q2o2", Label: "Web"}}, second.Options)

	maxQuestions := make([]dto.WizardQuestionRequest, dto.MaxWizardQuestions)
	for i := range maxQuestions {
		maxQuestions[i] = dto.WizardQuestionRequest{Title: fmt.Sprintf("Pergunta %d", i+1), Options: []string{"Sim"}}
	}
	q, err = questionFrom(nil, &dto.WizardRequest{Questions: maxQuestions})
	require.NoError(t, err)
	require.Len(t, q.Wizard.Questions, dto.MaxWizardQuestions)
	require.Equal(t, "q8", q.Wizard.Questions[7].ID)

	_, err = questionFrom(nil, &dto.WizardRequest{Questions: append(maxQuestions, dto.WizardQuestionRequest{Title: "Mais", Options: []string{"Sim"}})})
	requireInvalidField(t, err, "wizard")

	maxOptions := make([]string, dto.MaxQuestionOptions)
	for i := range maxOptions {
		maxOptions[i] = fmt.Sprintf("Opção %d", i+1)
	}
	q, err = questionFrom(nil, &dto.WizardRequest{Questions: []dto.WizardQuestionRequest{{Title: "Escolha", Options: maxOptions}}})
	require.NoError(t, err)
	require.Len(t, q.Wizard.Questions[0].Options, dto.MaxQuestionOptions)
	require.Equal(t, "q1o8", q.Wizard.Questions[0].Options[7].ID)

	// Exactly MaxQuestionTitle characters and exactly MaxQuestionLabel characters pass.
	longTitle := strings.Repeat("t", dto.MaxQuestionTitle)
	longOption := strings.Repeat("o", dto.MaxQuestionLabel)
	q, err = questionFrom(nil, &dto.WizardRequest{Questions: []dto.WizardQuestionRequest{{Title: longTitle, Options: []string{longOption}}}})
	require.NoError(t, err)
	require.Equal(t, longTitle, q.Wizard.Questions[0].Title)
	require.Equal(t, longOption, q.Wizard.Questions[0].Options[0].Label)

	_, err = questionFrom(nil, &dto.WizardRequest{Questions: []dto.WizardQuestionRequest{{Title: "Escolha", Options: append(maxOptions, "Mais")}}})
	requireInvalidField(t, err, "wizard")

	refused := map[string]*dto.WizardRequest{
		"no questions":     {},
		"empty title":      {Questions: []dto.WizardQuestionRequest{{Title: "   ", Options: []string{"Sim"}}}},
		"long title":       {Questions: []dto.WizardQuestionRequest{{Title: strings.Repeat("a", dto.MaxQuestionTitle+1), Options: []string{"Sim"}}}},
		"two-line title":   {Questions: []dto.WizardQuestionRequest{{Title: "a\nb", Options: []string{"Sim"}}}},
		"no options":       {Questions: []dto.WizardQuestionRequest{{Title: "Escolha", Options: []string{}}}},
		"long option":      {Questions: []dto.WizardQuestionRequest{{Title: "Escolha", Options: []string{strings.Repeat("a", dto.MaxQuestionLabel+1)}}}},
		"two-line option":  {Questions: []dto.WizardQuestionRequest{{Title: "Escolha", Options: []string{"a\nb"}}}},
		"duplicate option": {Questions: []dto.WizardQuestionRequest{{Title: "Escolha", Options: []string{"API", "api"}}}},
		"unknown kind":     {Questions: []dto.WizardQuestionRequest{{Title: "Escolha", Kind: "many", Options: []string{"Sim"}}}},
	}
	for name, wizard := range refused {
		t.Run(name, func(t *testing.T) {
			_, err := questionFrom(nil, wizard)
			requireInvalidField(t, err, "wizard")
		})
	}

	_, err = questionFrom([]string{"Sim"}, request())
	requireInvalidField(t, err, "quick_replies")
}

// QR-01, QR-02, QR-05: a post carries at most one of the question and answer shapes; a second
// shape names answers, or the first question field when both question shapes are set.
func TestOnePostCarriesOneShape(t *testing.T) {
	wizard := &dto.WizardRequest{Questions: []dto.WizardQuestionRequest{{Title: "Quando?", Options: []string{"Sim"}}}}
	answers := &dto.AnswerSubmission{QuickReply: "r1"}

	requireInvalidField(t, checkPostShapes([]string{"Sim"}, nil, answers), "answers")
	requireInvalidField(t, checkPostShapes(nil, wizard, answers), "answers")
	requireInvalidField(t, checkPostShapes([]string{"Sim"}, wizard, nil), "quick_replies")
	requireInvalidField(t, checkPostShapes([]string{"Sim"}, wizard, answers), "answers")

	require.NoError(t, checkPostShapes([]string{"Sim"}, nil, nil))
	require.NoError(t, checkPostShapes(nil, wizard, nil))
	require.NoError(t, checkPostShapes(nil, nil, answers))
	require.NoError(t, checkPostShapes(nil, nil, nil))
}

// QR-06: a quick reply answer stores the chosen label, not its id, and refuses an id the question
// does not offer or an answer of the other shape.
func TestAnswersQuickReply(t *testing.T) {
	q, err := questionFrom([]string{"Sim", "Não", "Depois"}, nil)
	require.NoError(t, err)

	a, err := answersFor(q, &dto.AnswerSubmission{QuickReply: "r2"})
	require.NoError(t, err)
	require.Equal(t, &dto.MessageAnswers{V: 1, QuickReply: "Não"}, a)
	require.Equal(t, "Não", answerText(a))
	raw, err := json.Marshal(a)
	require.NoError(t, err)
	require.Equal(t, `{"v":1,"quick_reply":"Não"}`, string(raw))

	_, err = answersFor(q, &dto.AnswerSubmission{QuickReply: "r9"})
	requireInvalidField(t, err, "answers")

	_, err = answersFor(q, &dto.AnswerSubmission{Items: []dto.AnswerItem{{QuestionID: "r1"}}})
	requireInvalidField(t, err, "answers")

	_, err = answersFor(q, &dto.AnswerSubmission{})
	requireInvalidField(t, err, "answers")
}

// wizardDocument is the stored question the answer tests validate against: q1 is single without a
// text, q2 is multi and allows one.
func wizardDocument() *dto.MessageInteractive {
	return &dto.MessageInteractive{V: 1, Wizard: &dto.Wizard{Questions: []dto.WizardQuestion{
		{
			ID: "q1", Title: "Quando?", Kind: dto.MessageQuestionKindSingle,
			Options: []dto.WizardOption{{ID: "q1o1", Label: "Amanhã"}, {ID: "q1o2", Label: "Na sexta"}},
		},
		{
			ID: "q2", Title: "O que entra?", Kind: dto.MessageQuestionKindMulti, AllowText: true,
			Options: []dto.WizardOption{{ID: "q2o1", Label: "API"}, {ID: "q2o2", Label: "Web"}},
		},
	}}}
}

// answersForWizard asserts one wizard answer is refused on the answers field.
func requireRefusedWizardAnswer(t *testing.T, question *dto.MessageInteractive, items ...dto.AnswerItem) {
	t.Helper()
	_, err := answersFor(question, &dto.AnswerSubmission{Items: items})
	requireInvalidField(t, err, "answers")
}

// QR-07: the wizard answers are validated against the stored question, matched by id in any order
// and stored in the wizard's order with labels, not ids.
func TestAnswersWizard(t *testing.T) {
	q := wizardDocument()

	// Answered in reverse order; stored in the wizard's order.
	a, err := answersFor(q, &dto.AnswerSubmission{Items: []dto.AnswerItem{
		{QuestionID: "q2", OptionIDs: []string{"q2o1", "q2o2"}, Text: "antes do almoço"},
		{QuestionID: "q1", OptionIDs: []string{"q1o2"}},
	}})
	require.NoError(t, err)
	require.Equal(t, &dto.MessageAnswers{V: 1, Answers: []dto.MessageAnswerItem{
		{Question: "Quando?", Values: []string{"Na sexta"}, Text: ""},
		{Question: "O que entra?", Values: []string{"API", "Web"}, Text: "antes do almoço"},
	}}, a)
	require.Equal(t, "Quando?: Na sexta\nO que entra?: API, Web (antes do almoço)", answerText(a))
	raw, err := json.Marshal(a)
	require.NoError(t, err)
	require.Equal(t, `{"v":1,"answers":[{"question":"Quando?","values":["Na sexta"],"text":""},{"question":"O que entra?","values":["API","Web"],"text":"antes do almoço"}]}`, string(raw))

	// Two options on a single question, no option and no text, and a text without allow_text.
	requireRefusedWizardAnswer(t, q,
		dto.AnswerItem{QuestionID: "q1", OptionIDs: []string{"q1o1", "q1o2"}},
		dto.AnswerItem{QuestionID: "q2", OptionIDs: []string{"q2o1"}})
	requireRefusedWizardAnswer(t, q,
		dto.AnswerItem{QuestionID: "q1"},
		dto.AnswerItem{QuestionID: "q2", OptionIDs: []string{"q2o1"}})
	requireRefusedWizardAnswer(t, q,
		dto.AnswerItem{QuestionID: "q1", OptionIDs: []string{"q1o1"}, Text: "depois das 10h"},
		dto.AnswerItem{QuestionID: "q2", OptionIDs: []string{"q2o1"}})

	// A single question that allows text may be answered by its text alone.
	textQuestion := &dto.MessageInteractive{V: 1, Wizard: &dto.Wizard{Questions: []dto.WizardQuestion{{
		ID: "q1", Title: "Quando?", Kind: dto.MessageQuestionKindSingle, AllowText: true,
		Options: []dto.WizardOption{{ID: "q1o1", Label: "Amanhã"}, {ID: "q1o2", Label: "Na sexta"}},
	}}}}
	a, err = answersFor(textQuestion, &dto.AnswerSubmission{Items: []dto.AnswerItem{{QuestionID: "q1", Text: "  depois das 10h  "}}})
	require.NoError(t, err)
	require.Equal(t, []string{}, a.Answers[0].Values)
	require.Equal(t, "depois das 10h", a.Answers[0].Text)
	require.Equal(t, "Quando?: depois das 10h", answerText(a))

	// One option plus a note when the question allows text.
	a, err = answersFor(textQuestion, &dto.AnswerSubmission{Items: []dto.AnswerItem{{QuestionID: "q1", OptionIDs: []string{"q1o2"}, Text: "depois das 10h"}}})
	require.NoError(t, err)
	require.Equal(t, []string{"Na sexta"}, a.Answers[0].Values)
	require.Equal(t, "depois das 10h", a.Answers[0].Text)
	require.Equal(t, "Quando?: Na sexta (depois das 10h)", answerText(a))

	// A text note is one line, like a label or a title; a space is not a line break.
	requireRefusedWizardAnswer(t, textQuestion, dto.AnswerItem{QuestionID: "q1", Text: "a\nb"})
	requireRefusedWizardAnswer(t, textQuestion, dto.AnswerItem{QuestionID: "q1", Text: "a\rb"})
	a, err = answersFor(textQuestion, &dto.AnswerSubmission{Items: []dto.AnswerItem{{QuestionID: "q1", Text: "a b"}}})
	require.NoError(t, err)
	require.Equal(t, "a b", a.Answers[0].Text)

	// A multi question needs at least one option, even with a text.
	requireRefusedWizardAnswer(t, q,
		dto.AnswerItem{QuestionID: "q1", OptionIDs: []string{"q1o1"}},
		dto.AnswerItem{QuestionID: "q2", Text: "API"})
	requireRefusedWizardAnswer(t, q,
		dto.AnswerItem{QuestionID: "q1", OptionIDs: []string{"q1o1"}},
		dto.AnswerItem{QuestionID: "q2"})

	// The free text is bounded by MaxAnswerText.
	requireRefusedWizardAnswer(t, q,
		dto.AnswerItem{QuestionID: "q1", OptionIDs: []string{"q1o1"}},
		dto.AnswerItem{QuestionID: "q2", OptionIDs: []string{"q2o1"}, Text: strings.Repeat("a", dto.MaxAnswerText+1)})

	// Missing, duplicated and unknown questions, and an option of another question.
	requireRefusedWizardAnswer(t, q, dto.AnswerItem{QuestionID: "q1", OptionIDs: []string{"q1o1"}})
	requireRefusedWizardAnswer(t, q,
		dto.AnswerItem{QuestionID: "q1", OptionIDs: []string{"q1o1"}},
		dto.AnswerItem{QuestionID: "q1", OptionIDs: []string{"q1o2"}},
		dto.AnswerItem{QuestionID: "q2", OptionIDs: []string{"q2o1"}})
	requireRefusedWizardAnswer(t, q,
		dto.AnswerItem{QuestionID: "q1", OptionIDs: []string{"q1o1"}},
		dto.AnswerItem{QuestionID: "q2", OptionIDs: []string{"q2o1"}},
		dto.AnswerItem{QuestionID: "q9", OptionIDs: []string{"q9o1"}})
	requireRefusedWizardAnswer(t, q,
		dto.AnswerItem{QuestionID: "q1", OptionIDs: []string{"q2o1"}},
		dto.AnswerItem{QuestionID: "q2", OptionIDs: []string{"q2o1"}})

	// A wizard answer never takes the quick reply shape.
	_, err = answersFor(q, &dto.AnswerSubmission{QuickReply: "r1"})
	requireInvalidField(t, err, "answers")
	_, err = answersFor(q, &dto.AnswerSubmission{})
	requireInvalidField(t, err, "answers")
}

// QR-06, QR-07: the answer text is one line per wizard question, with the note in parentheses.
func TestAnswerText(t *testing.T) {
	require.Equal(t, "", answerText(nil))
	require.Equal(t, "Sim", answerText(&dto.MessageAnswers{V: 1, QuickReply: "Sim"}))
	require.Equal(t, "Quando?: Na sexta\nO que entra?: API, Web (antes do almoço)", answerText(&dto.MessageAnswers{V: 1, Answers: []dto.MessageAnswerItem{
		{Question: "Quando?", Values: []string{"Na sexta"}},
		{Question: "O que entra?", Values: []string{"API", "Web"}, Text: "antes do almoço"},
	}}))
	require.Equal(t, "Quando?: depois das 10h", answerText(&dto.MessageAnswers{V: 1, Answers: []dto.MessageAnswerItem{
		{Question: "Quando?", Values: []string{}, Text: "depois das 10h"},
	}}))
	require.Equal(t, "Quando?: Na sexta (depois das 10h)", answerText(&dto.MessageAnswers{V: 1, Answers: []dto.MessageAnswerItem{
		{Question: "Quando?", Values: []string{"Na sexta"}, Text: "depois das 10h"},
	}}))
	require.Equal(t, "O que entra?: API, Web", answerText(&dto.MessageAnswers{V: 1, Answers: []dto.MessageAnswerItem{
		{Question: "O que entra?", Values: []string{"API", "Web"}},
	}}))
}

// QR-19, QR-20: the platform fallback numbers the labels under the question text.
func TestNumberedOptions(t *testing.T) {
	require.Equal(t, "Deploy?\n\n1. Sim\n2. Não\n3. Depois", NumberedOptions("Deploy?", []string{"Sim", "Não", "Depois"}))
	require.Equal(t, "Deploy?\n\n1. Sim", NumberedOptions("Deploy?", []string{"Sim"}))
	require.NoError(t, ValidateQuickReplyLabels([]string{"Sim", "Não"}))
	requireInvalidField(t, ValidateQuickReplyLabels([]string{"1", "2", "3", "4", "5"}), "quick_replies")
	requireInvalidField(t, ValidateQuickReplyLabels([]string{strings.Repeat("a", dto.MaxQuestionLabel+1)}), "quick_replies")
	requireInvalidField(t, ValidateQuickReplyLabels([]string{"a", "A"}), "quick_replies")
}

// QR-01, QR-06: an empty, corrupt or future version of a stored document reads as none, and a v1
// document decodes to its DTO.
func TestParseTheStoredDocuments(t *testing.T) {
	require.Nil(t, dto.ParseMessageInteractive(""))
	require.Nil(t, dto.ParseMessageInteractive("not json"))
	require.Nil(t, dto.ParseMessageInteractive(`{"v":2,"quick_replies":[{"id":"r1","label":"Sim"}]}`))
	require.Nil(t, dto.ParseMessageAnswers(""))
	require.Nil(t, dto.ParseMessageAnswers("not json"))
	require.Nil(t, dto.ParseMessageAnswers(`{"v":2,"quick_reply":"Sim"}`))

	require.Equal(t, &dto.MessageInteractive{V: 1, QuickReplies: []dto.QuickReply{{ID: "r1", Label: "Sim"}}},
		dto.ParseMessageInteractive(`{"v":1,"quick_replies":[{"id":"r1","label":"Sim"}]}`))
	require.Equal(t, &dto.MessageAnswers{V: 1, QuickReply: "Sim"},
		dto.ParseMessageAnswers(`{"v":1,"quick_reply":"Sim"}`))
	require.Equal(t, &dto.MessageInteractive{V: 1, Wizard: &dto.Wizard{Questions: []dto.WizardQuestion{{
		ID: "q1", Title: "Quando?", Kind: dto.MessageQuestionKindMulti, AllowText: true,
		Options: []dto.WizardOption{{ID: "q1o1", Label: "Amanhã"}},
	}}}}, dto.ParseMessageInteractive(`{"v":1,"wizard":{"questions":[{"id":"q1","title":"Quando?","kind":"multi","allow_text":true,"options":[{"id":"q1o1","label":"Amanhã"}]}]}}`))
}
