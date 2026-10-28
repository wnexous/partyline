import type { AnswerItem, WizardQuestion } from "@/shared/api/types.gen";

/*
 * The pure rules of the wizard (QR-14, QR-15, QR-17). They live outside the bubble so the step gate,
 * the text-only single answer and the submitted items can be read and tested without JSX.
 */

/** One question's draft while the wizard is being filled. */
export interface QuestionDraft {
  /** The ids of the chosen options, in the order they were chosen. */
  optionIds: string[];
  /** The optional note, at most MaxAnswerText characters. */
  text: string;
}

export const emptyDraft = (): QuestionDraft => ({ optionIds: [], text: "" });

/**
 * A single-choice question is valid with exactly one option, or, when it allows text and no option
 * is chosen, with a non-empty text (QR-15).
 */
export function singleValid(question: WizardQuestion, draft: QuestionDraft | undefined): boolean {
  const chosen = draft?.optionIds.length ?? 0;
  if (chosen === 1) return true;
  if (chosen > 1) return false;
  return question.allow_text && (draft?.text.trim().length ?? 0) > 0;
}

/** A multiple-choice question is valid with at least one option; its note is optional (QR-15). */
export function multiValid(_question: WizardQuestion, draft: QuestionDraft | undefined): boolean {
  return (draft?.optionIds.length ?? 0) > 0;
}

/** Whether the answer to one question may move the wizard past its step. */
export function questionValid(question: WizardQuestion, draft: QuestionDraft | undefined): boolean {
  return question.kind === "multi" ? multiValid(question, draft) : singleValid(question, draft);
}

/**
 * The submission of every question, in the wizard's order: the ids the server assigned, the chosen
 * option ids and the note (QR-14). An empty option list is kept for a text-only single answer, so the
 * server reads the same shape the bubble showed.
 */
export function submissionOf(
  questions: WizardQuestion[],
  drafts: Record<string, QuestionDraft>,
): AnswerItem[] {
  return questions.map((question) => {
    const draft = drafts[question.id] ?? emptyDraft();
    const item: AnswerItem = {
      question_id: question.id,
      option_ids: [...draft.optionIds],
    };
    if (draft.text.trim()) item.text = draft.text;
    return item;
  });
}
