package rooms

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/validation"
)

// checkPostShapes refuses a post that carries more than one of the question and answer shapes
// (QR-01, QR-02, QR-05): a message asks a question or answers one, not both. A post with both
// question shapes names the first field, as the validation table does.
func checkPostShapes(quickReplies []string, wizard *dto.WizardRequest, answers *dto.AnswerSubmission) error {
	check := validation.Check{}
	if answers != nil && (quickReplies != nil || wizard != nil) {
		check.Add("answers", "a message asks a question or answers one, not both")
	}
	if quickReplies != nil && wizard != nil {
		check.Add("quick_replies", "a message asks a question or answers one, not both")
	}
	return check.Err()
}

// questionFrom canonicalises the question of a post (QR-01..QR-03): the server assigns the ids
// from the order the author gave, trims the labels and refuses the design's limits and two shapes
// in one message. It returns nil when the post carries no interactive part, which is every other
// message.
func questionFrom(quickReplies []string, wizard *dto.WizardRequest) (*dto.MessageInteractive, error) {
	check := validation.Check{}
	switch {
	case quickReplies != nil && wizard != nil:
		check.Add("quick_replies", "a message asks a question or answers one, not both")
		return nil, check.Err()
	case quickReplies != nil:
		labels := checkLabels(check, quickReplies, dto.MaxQuickReplies, "quick_replies")
		if err := check.Err(); err != nil {
			return nil, err
		}
		replies := make([]dto.QuickReply, len(labels))
		for i, label := range labels {
			replies[i] = dto.QuickReply{ID: "r" + strconv.Itoa(i+1), Label: label}
		}
		return &dto.MessageInteractive{V: 1, QuickReplies: replies}, nil
	case wizard != nil:
		out := wizardFrom(check, wizard)
		if err := check.Err(); err != nil {
			return nil, err
		}
		return &dto.MessageInteractive{V: 1, Wizard: out}, nil
	}
	return nil, nil
}

// wizardFrom canonicalises the questions of a wizard: q1..q8 and q1o1..q1o8, single by default,
// trimmed titles and labels (QR-02, QR-03).
func wizardFrom(check validation.Check, req *dto.WizardRequest) *dto.Wizard {
	if len(req.Questions) < 1 {
		check.Add("wizard", "must have at least one question")
	}
	if len(req.Questions) > dto.MaxWizardQuestions {
		check.Add("wizard", "must have at most "+strconv.Itoa(dto.MaxWizardQuestions)+" questions")
	}
	out := &dto.Wizard{Questions: make([]dto.WizardQuestion, len(req.Questions))}
	for i, question := range req.Questions {
		title := strings.TrimSpace(question.Title)
		switch {
		case title == "":
			check.Add("wizard", "a question title must not be empty")
		case utf8.RuneCountInString(title) > dto.MaxQuestionTitle:
			check.Add("wizard", "a question title must have at most "+strconv.Itoa(dto.MaxQuestionTitle)+" characters")
		case strings.ContainsAny(title, "\r\n"):
			check.Add("wizard", "a question title must be one line")
		}
		kind := question.Kind
		if kind == "" {
			kind = dto.MessageQuestionKindSingle
		}
		if kind != dto.MessageQuestionKindSingle && kind != dto.MessageQuestionKindMulti {
			check.Add("wizard", "a question kind must be single or multi")
		}
		labels := checkLabels(check, question.Options, dto.MaxQuestionOptions, "wizard")
		options := make([]dto.WizardOption, len(labels))
		for j, label := range labels {
			options[j] = dto.WizardOption{ID: "q" + strconv.Itoa(i+1) + "o" + strconv.Itoa(j+1), Label: label}
		}
		out.Questions[i] = dto.WizardQuestion{
			ID: "q" + strconv.Itoa(i+1), Title: title, Kind: kind, AllowText: question.AllowText, Options: options,
		}
	}
	return out
}

// checkLabels trims a set of quick reply or option labels and records its first problem (QR-03):
// 1 to limit labels, each 1 to MaxQuestionLabel characters on one line, and no two equal labels
// ignoring case. It returns the trimmed labels, which are what the server stores.
func checkLabels(check validation.Check, labels []string, limit int, field string) []string {
	if len(labels) < 1 {
		check.Add(field, "must have at least one label")
	}
	if len(labels) > limit {
		check.Add(field, "must have at most "+strconv.Itoa(limit)+" labels")
	}
	trimmed := make([]string, len(labels))
	seen := make(map[string]bool, len(labels))
	for i, label := range labels {
		trimmed[i] = strings.TrimSpace(label)
		switch {
		case trimmed[i] == "":
			check.Add(field, "a label must not be empty")
		case utf8.RuneCountInString(trimmed[i]) > dto.MaxQuestionLabel:
			check.Add(field, "a label must have at most "+strconv.Itoa(dto.MaxQuestionLabel)+" characters")
		case strings.ContainsAny(trimmed[i], "\r\n"):
			check.Add(field, "a label must be one line")
		}
		folded := strings.ToLower(trimmed[i])
		if seen[folded] {
			check.Add(field, "must not repeat a label")
		}
		seen[folded] = true
	}
	return trimmed
}

// ValidateQuickReplyLabels applies the quick reply label rules to labels a caller is about to send
// as a numbered list on a platform (QR-19, QR-20): the same set a room would store.
func ValidateQuickReplyLabels(labels []string) error {
	check := validation.Check{}
	checkLabels(check, labels, dto.MaxQuickReplies, "quick_replies")
	return check.Err()
}

// answersFor validates an answer against the stored question, never against the request, and
// builds its structured copy (QR-06, QR-07): the label of the chosen quick reply, or one item per
// wizard question in the wizard's order, with the chosen labels and the trimmed text note. It
// returns nil when the post carries no answer.
func answersFor(question *dto.MessageInteractive, submission *dto.AnswerSubmission) (*dto.MessageAnswers, error) {
	if submission == nil {
		return nil, nil
	}
	check := validation.Check{}
	if question == nil {
		check.Add("answers", "must answer a message that asks a question")
		return nil, check.Err()
	}
	if question.Wizard != nil {
		return wizardAnswers(question.Wizard, submission)
	}
	if submission.QuickReply == "" || len(submission.Items) > 0 {
		check.Add("answers", "must choose one of the question's quick replies")
		return nil, check.Err()
	}
	for _, reply := range question.QuickReplies {
		if reply.ID == submission.QuickReply {
			return &dto.MessageAnswers{V: 1, QuickReply: reply.Label}, nil
		}
	}
	check.Add("answers", "must choose one of the question's quick replies")
	return nil, check.Err()
}

// wizardAnswers matches every answer item to its question by id, in any order, and builds one item
// per question in the wizard's order (QR-07).
func wizardAnswers(wizard *dto.Wizard, submission *dto.AnswerSubmission) (*dto.MessageAnswers, error) {
	check := validation.Check{}
	if submission.QuickReply != "" || len(submission.Items) == 0 {
		check.Add("answers", "must answer every question of the wizard")
		return nil, check.Err()
	}
	byQuestion := make(map[string]dto.AnswerItem, len(submission.Items))
	for _, item := range submission.Items {
		if _, ok := byQuestion[item.QuestionID]; ok {
			check.Add("answers", "must answer each question once")
			return nil, check.Err()
		}
		byQuestion[item.QuestionID] = item
	}
	answers := make([]dto.MessageAnswerItem, 0, len(wizard.Questions))
	for _, question := range wizard.Questions {
		item, ok := byQuestion[question.ID]
		if !ok {
			check.Add("answers", "must answer every question of the wizard")
			return nil, check.Err()
		}
		kind := question.Kind
		if kind == "" {
			kind = dto.MessageQuestionKindSingle
		}
		if kind != dto.MessageQuestionKindSingle && kind != dto.MessageQuestionKindMulti {
			check.Add("answers", "the question has an unknown kind")
			return nil, check.Err()
		}
		question.Kind = kind
		values, problem := chosenValues(question, item)
		if problem != "" {
			check.Add("answers", problem)
			return nil, check.Err()
		}
		text := strings.TrimSpace(item.Text)
		switch {
		case utf8.RuneCountInString(text) > dto.MaxAnswerText:
			check.Add("answers", "a text answer must have at most "+strconv.Itoa(dto.MaxAnswerText)+" characters")
			return nil, check.Err()
		case strings.ContainsAny(text, "\r\n"):
			check.Add("answers", "a text answer must be one line")
			return nil, check.Err()
		case text != "" && !question.AllowText:
			check.Add("answers", "a question that does not allow a text answer was given one")
			return nil, check.Err()
		case question.Kind == dto.MessageQuestionKindMulti && len(values) == 0:
			check.Add("answers", "a multiple-choice question needs at least one option")
			return nil, check.Err()
		case question.Kind != dto.MessageQuestionKindMulti && len(values) == 0 && text == "":
			check.Add("answers", "a single-choice question needs an option or a text")
			return nil, check.Err()
		}
		if values == nil {
			values = []string{}
		}
		answers = append(answers, dto.MessageAnswerItem{Question: question.Title, Values: values, Text: text})
	}
	if len(byQuestion) != len(wizard.Questions) {
		check.Add("answers", "must answer a question of the wizard")
		return nil, check.Err()
	}
	return &dto.MessageAnswers{V: 1, Answers: answers}, nil
}

// chosenValues returns the labels of the options an item chose, in the question's order, and the
// first problem with them: a single question takes exactly one and a multiple-choice question at
// least one, and every id must belong to the question (QR-07).
func chosenValues(question dto.WizardQuestion, item dto.AnswerItem) ([]string, string) {
	switch {
	case question.Kind == dto.MessageQuestionKindSingle && len(item.OptionIDs) > 1:
		return nil, "a single-choice question takes one option"
	case question.Kind == dto.MessageQuestionKindMulti && len(item.OptionIDs) == 0:
		return nil, "a multiple-choice question needs at least one option"
	}
	wanted := make(map[string]bool, len(item.OptionIDs))
	for _, id := range item.OptionIDs {
		wanted[id] = true
	}
	values := make([]string, 0, len(item.OptionIDs))
	for _, option := range question.Options {
		if wanted[option.ID] {
			values = append(values, option.Label)
			delete(wanted, option.ID)
		}
	}
	if len(wanted) > 0 {
		return nil, "an option must belong to its question"
	}
	return values, ""
}

// answerText renders the human text of a structured answer, the one the timeline, the previews and
// the asking AI read (QR-06, QR-07): the quick reply's label, or one "Title: answer" line per
// wizard question, with a text note in parentheses after the labels it accompanies.
func answerText(answer *dto.MessageAnswers) string {
	if answer == nil {
		return ""
	}
	if len(answer.Answers) == 0 {
		return answer.QuickReply
	}
	lines := make([]string, len(answer.Answers))
	for i, item := range answer.Answers {
		line := item.Question + ": "
		if len(item.Values) > 0 {
			line += strings.Join(item.Values, ", ")
			if item.Text != "" {
				line += " (" + item.Text + ")"
			}
		} else {
			line += item.Text
		}
		lines[i] = line
	}
	return strings.Join(lines, "\n")
}

// NumberedOptions renders a question for a platform that has no buttons (QR-19, QR-20): the text,
// a blank line and one "1. label" line per quick reply. The labels are trimmed as a room would
// store them.
func NumberedOptions(text string, labels []string) string {
	if len(labels) == 0 {
		return text
	}
	lines := make([]string, len(labels))
	for i, label := range labels {
		lines[i] = strconv.Itoa(i+1) + ". " + strings.TrimSpace(label)
	}
	return text + "\n\n" + strings.Join(lines, "\n")
}
