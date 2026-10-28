import { useEffect, useRef, useState } from "react";
import {
  QuickReplyChoices,
  WizardQuestionList,
  WizardStep,
  WizardSummary,
} from "@/entities/message";
import type { Message, MessageAnswers, QuickReply, WizardQuestion } from "@/shared/api/types.gen";
import { useI18n } from "@/shared/i18n";
import { emptyDraft, type QuestionDraft, questionValid, submissionOf } from "../lib/questions";
import { useAnswerQuestion } from "../model/use-answer-question";

/*
 * The question a message carries, under its text (QR-10..QR-17). A quick reply is one tap: the
 * buttons post the answer as a reply at once, hold still while it is sent, and mark the chosen label
 * once it is stored. A wizard shows one question per step, keeps Next and Send disabled until the
 * step is valid, and posts every answer as one reply. The state is derived from the timeline (the
 * viewer's answer, `myAnswer`), so a reload keeps it; a tap guard means a second tap posts nothing.
 * The author of the question, a member who may not post and a conversation frozen as history see the
 * question without acting and one line saying why. This file holds the state and the posts; how
 * the question looks is the message entity's (entities/message/ui/question-view).
 */

export interface QuestionBubbleProps {
  message: Message;
  /** The viewer's intelligence id: their own question is not theirs to answer (QR-13). */
  meId: string;
  /** The viewer's answer to this question, when the timeline holds it (QR-12). */
  myAnswer?: Message;
  /** The viewer may post in this conversation: without it the labels are text and say why (QR-13). */
  canAnswer?: boolean;
  /** The conversation is history (CID-21): its questions are answerable by no one (QR-13). */
  frozen?: boolean;
}

export function QuestionBubble(props: QuestionBubbleProps) {
  const quickReplies = props.message.interactive?.quick_replies ?? [];
  const questions = props.message.interactive?.wizard?.questions ?? [];
  if (quickReplies.length > 0) return <QuickReplies {...props} quickReplies={quickReplies} />;
  if (questions.length > 0) return <QuestionWizard {...props} questions={questions} />;
  return null;
}

/**
 * The viewer's answer the bubble shows (QR-12): the timeline's when it has one, and this bubble's
 * own post only while the timeline catches up. Dropping the post's reply as soon as the timeline
 * carries the answer is what lets a deleted answer make the question answerable again without a
 * reload: only the timeline says whether the answer still exists.
 */
function useShownAnswer(
  myAnswer: Message | undefined,
  answer: ReturnType<typeof useAnswerQuestion>,
): MessageAnswers | undefined {
  const { data, reset } = answer;
  useEffect(() => {
    if (myAnswer && data) reset();
  }, [myAnswer, data, reset]);
  return myAnswer ? myAnswer.answers : data?.answers;
}

function QuickReplies({
  message,
  meId,
  myAnswer,
  canAnswer = false,
  frozen = false,
  quickReplies,
}: QuestionBubbleProps & { quickReplies: QuickReply[] }) {
  const { t } = useI18n();
  const answer = useAnswerQuestion(message.room_id);

  const own = message.author_id === meId;
  // The stored answer is the authority: the live event or a reload brings it, the reply of this tap
  // fills the gap while the timeline catches up (QR-11, QR-12).
  const sent = useShownAnswer(myAnswer, answer)?.quick_reply;
  const pending = answer.isPending;
  const disabled = pending || sent !== undefined;
  const acts = !own && canAnswer && !frozen;
  // The line that says why a question offers no action (QR-13); the question's author first.
  const reason = own
    ? t("qr.ownQuestion")
    : frozen
      ? t("qr.frozen")
      : canAnswer
        ? null
        : t("qr.noPermission");
  const status = pending
    ? t("qr.sending")
    : sent !== undefined
      ? t("qr.answered", { label: sent })
      : reason;

  return (
    <QuickReplyChoices
      quickReplies={quickReplies}
      sent={sent}
      acts={acts}
      disabled={disabled}
      status={status}
      onPick={(option) => {
        answer.mutate({ replyTo: message.id, answers: { quick_reply: option.id } });
      }}
    />
  );
}

function QuestionWizard({
  message,
  meId,
  myAnswer,
  canAnswer = false,
  frozen = false,
  questions,
}: QuestionBubbleProps & { questions: WizardQuestion[] }) {
  const { t } = useI18n();
  const answer = useAnswerQuestion(message.room_id);
  const [step, setStep] = useState(0);
  const [drafts, setDrafts] = useState<Record<string, QuestionDraft>>(() =>
    Object.fromEntries(questions.map((question) => [question.id, emptyDraft()])),
  );
  const heading = useRef<HTMLHeadingElement>(null);
  const summaryHeading = useRef<HTMLHeadingElement>(null);
  const firstStep = useRef(true);
  // Set by this bubble's own Send, so a loaded answer never takes the reader's focus on mount (QR-16).
  const sentHere = useRef(false);
  const own = message.author_id === meId;
  const acts = !own && canAnswer && !frozen;
  const reason = own
    ? t("qr.ownQuestion")
    : frozen
      ? t("qr.frozen")
      : canAnswer
        ? null
        : t("qr.noPermission");
  // The stored answer is the authority, from the timeline (QR-16). The mutation's reply fills the
  // gap between Send and the live room.message event, exactly as the quick reply does (QR-12).
  const stored = useShownAnswer(myAnswer, answer)?.answers;
  const sent = stored && stored.length > 0 ? stored : undefined;
  const question = questions[step];
  const draft = question ? (drafts[question.id] ?? emptyDraft()) : undefined;
  const valid = question ? questionValid(question, draft) : false;
  const pending = answer.isPending;

  // biome-ignore lint/correctness/useExhaustiveDependencies: the effect moves focus when the step changes, which its body reads through the heading it focuses
  useEffect(() => {
    // A step change moves the reader to the new question; the first render leaves focus alone.
    if (firstStep.current) {
      firstStep.current = false;
      return;
    }
    heading.current?.focus();
  }, [step]);

  useEffect(() => {
    // The summary takes focus after this bubble's own successful Send, and only then: an old
    // answered wizard that loads with the room leaves the composer's focus alone (QR-16).
    if (!sent || !sentHere.current) return;
    sentHere.current = false;
    summaryHeading.current?.focus();
  }, [sent]);

  if (sent) {
    return <WizardSummary answers={sent} canAnswer={acts} headingRef={summaryHeading} />;
  }

  if (!acts) {
    return <WizardQuestionList questions={questions} reason={reason} />;
  }

  const choose = (optionId: string) => {
    if (!question) return;
    setDrafts((current) => {
      const currentDraft = current[question.id] ?? emptyDraft();
      const optionIds =
        question.kind === "multi"
          ? currentDraft.optionIds.includes(optionId)
            ? currentDraft.optionIds.filter((id) => id !== optionId)
            : [...currentDraft.optionIds, optionId]
          : [optionId];
      return { ...current, [question.id]: { ...currentDraft, optionIds } };
    });
  };

  const note = (text: string) => {
    if (!question) return;
    setDrafts((current) => ({
      ...current,
      [question.id]: { ...(current[question.id] ?? emptyDraft()), text },
    }));
  };

  const submit = () => {
    if (!valid || pending) return;
    sentHere.current = true;
    answer.mutate(
      { replyTo: message.id, answers: { items: submissionOf(questions, drafts) } },
      // A failed send leaves no summary to focus; a later answer from the timeline is not ours.
      { onError: () => (sentHere.current = false) },
    );
  };

  if (!question) return null;

  return (
    <WizardStep
      step={step}
      total={questions.length}
      question={question}
      draft={draft}
      pending={pending}
      valid={valid}
      headingRef={heading}
      messageId={message.id}
      onChoose={choose}
      onNote={note}
      onBack={() => setStep((current) => current - 1)}
      onNext={() => setStep((current) => current + 1)}
      onSubmit={submit}
    />
  );
}
