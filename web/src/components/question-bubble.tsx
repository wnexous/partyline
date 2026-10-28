import { type RefObject, useEffect, useId, useRef, useState } from "react";
import { Button } from "@/shared/ui/primitives/button";
import { useAnswerQuestion } from "@/hooks/use-answer-question";
import {
  MaxAnswerText,
  type Message,
  type MessageAnswerItem,
  type MessageAnswers,
  type QuickReply,
  type WizardQuestion,
} from "@/lib/api/types.gen";
import { useI18n } from "@/shared/i18n";
import { emptyDraft, type QuestionDraft, questionValid, submissionOf } from "@/lib/questions";
import { cn } from "@/shared/lib/utils";

/*
 * The question a message carries, under its text (QR-10..QR-17). A quick reply is one tap: the
 * buttons post the answer as a reply at once, hold still while it is sent, and mark the chosen label
 * once it is stored. A wizard shows one question per step, keeps Next and Send disabled until the
 * step is valid, and posts every answer as one reply. The state is derived from the timeline (the
 * viewer's answer, `myAnswer`), so a reload keeps it; a tap guard means a second tap posts nothing.
 * The author of the question, a member who may not post and a conversation frozen as history see the
 * question without acting and one line saying why.
 */

/** A label's box: 44 px, wrapping inside the bubble, starting at its own edge (QR-10). */
const CHIP =
  "flex min-h-11 min-w-0 max-w-full flex-1 basis-32 items-center rounded-md border px-3 py-2 text-start text-sm break-words";

/** The focus ring of the wizard's own controls, as the app draws it elsewhere. */
const FOCUS = "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring";

/** A control that is busy stays focusable: aria-disabled and a guard, not a real disabled. */
const BUSY = "aria-disabled:cursor-default aria-disabled:opacity-50";

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
    // biome-ignore lint/a11y/useSemanticElements: a fieldset would add a legend and a border to a row of buttons inside the bubble
    <div role="group" aria-label={t("qr.group")} className="mt-1.5 flex flex-wrap gap-2">
      {acts
        ? quickReplies.map((option) => {
            const pressed = sent === option.label;
            return (
              <button
                key={option.id}
                type="button"
                aria-pressed={sent === undefined ? undefined : pressed}
                aria-disabled={disabled ? "true" : undefined}
                onClick={() => {
                  // aria-disabled keeps the pressed button focusable; the guard is what stops a tap.
                  if (disabled) return;
                  answer.mutate({ replyTo: message.id, answers: { quick_reply: option.id } });
                }}
                className={cn(
                  CHIP,
                  "border-input bg-card text-foreground transition-colors hover:bg-accent focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring aria-disabled:cursor-default aria-disabled:hover:bg-card",
                  pressed && "border-primary bg-primary/10 font-medium text-foreground",
                )}
              >
                {/* The label is its own flex item so a long one shrinks and breaks instead of spilling (QR-24). */}
                <span className="min-w-0 [overflow-wrap:anywhere]">{option.label}</span>
              </button>
            );
          })
        : quickReplies.map((option) => (
            <span key={option.id} className={cn(CHIP, "border-border text-muted-foreground")}>
              <span className="min-w-0 [overflow-wrap:anywhere]">{option.label}</span>
            </span>
          ))}
      {status ? (
        <p role="status" className="w-full text-xs text-muted-foreground text-pretty">
          {status}
        </p>
      ) : null}
    </div>
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
  const headingId = useId();
  const textId = `${headingId}-text`;
  const hintId = `${headingId}-hint`;
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
    return (
      <div className="mt-1.5 min-w-0 space-y-2">
        {questions.map((item) => (
          <div key={item.id}>
            <p className="text-sm font-medium break-words">{item.title}</p>
            <ul className="mt-1 list-disc ps-5 text-sm text-muted-foreground">
              {item.options.map((option) => (
                <li key={option.id} className="break-words">
                  {option.label}
                </li>
              ))}
            </ul>
          </div>
        ))}
        {reason ? (
          <p role="status" className="text-xs text-muted-foreground text-pretty">
            {reason}
          </p>
        ) : null}
      </div>
    );
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

  // A single question that allows text may be answered by the text alone, so the note is the
  // answer there, not an optional extra (QR-15).
  const textLabel =
    question.kind === "single" && question.allow_text
      ? t("qr.textLabelOrOption")
      : t("qr.textLabel");

  return (
    <div className="mt-1.5 min-w-0">
      <p role="status" className="text-xs text-muted-foreground">
        {t("qr.step", { current: step + 1, total: questions.length })}
      </p>
      <div className="mt-1 min-w-0">
        <h4
          ref={heading}
          id={headingId}
          tabIndex={-1}
          className={cn("text-sm font-semibold break-words", FOCUS)}
        >
          {question.title}
        </h4>
        {/* The group holds the radios alone: the note after it is not part of the choice (QR-16). */}
        {/* biome-ignore lint/a11y/useAriaPropsSupportedByRole: a group and a radiogroup take their accessible name from the step heading */}
        <div
          role={question.kind === "multi" ? "group" : "radiogroup"}
          aria-labelledby={headingId}
          className="mt-1.5 min-w-0"
        >
          <div className="space-y-1.5">
            {question.options.map((option) => (
              <label
                key={option.id}
                className={cn(
                  "flex min-h-11 cursor-pointer items-center gap-2 rounded-md border border-input bg-card px-3 py-2 text-sm transition-colors hover:bg-accent has-[:focus-visible]:outline-2 has-[:focus-visible]:outline-offset-2 has-[:focus-visible]:outline-ring",
                  pending && "cursor-default opacity-60",
                )}
              >
                <input
                  type={question.kind === "multi" ? "checkbox" : "radio"}
                  // The question ids repeat in every message, so the group name carries the message
                  // too; otherwise two wizards on one page share one radio group (QR-16).
                  name={`${message.id}-${question.id}`}
                  value={option.id}
                  checked={draft?.optionIds.includes(option.id) ?? false}
                  aria-disabled={pending ? "true" : undefined}
                  onChange={() => {
                    if (pending) return;
                    choose(option.id);
                  }}
                  className="size-4 shrink-0 accent-primary"
                />
                <span className="min-w-0 break-words">{option.label}</span>
              </label>
            ))}
          </div>
        </div>
        {question.allow_text ? (
          <div className="mt-2 min-w-0">
            <label htmlFor={textId} className="text-xs font-medium">
              {textLabel}
            </label>
            <textarea
              id={textId}
              rows={2}
              maxLength={MaxAnswerText}
              value={draft?.text ?? ""}
              readOnly={pending}
              aria-disabled={pending ? "true" : undefined}
              onChange={(event) => {
                if (pending) return;
                note(event.target.value);
              }}
              aria-describedby={hintId}
              className={cn(
                "mt-1 w-full rounded-md border border-input bg-card px-3 py-2 text-sm break-words",
                FOCUS,
              )}
            />
            <p id={hintId} className="mt-1 text-xs text-muted-foreground">
              {t("qr.textHint", { max: MaxAnswerText })}
            </p>
          </div>
        ) : null}
      </div>
      <div className="mt-2 flex gap-2">
        {step > 0 ? (
          <Button
            type="button"
            variant="outline"
            className={cn("h-11 flex-1", BUSY)}
            aria-disabled={pending ? "true" : undefined}
            onClick={() => {
              // aria-disabled keeps the pressed control focusable; the guard is what stops it.
              if (pending) return;
              setStep((current) => current - 1);
            }}
          >
            {t("qr.back")}
          </Button>
        ) : null}
        {step < questions.length - 1 ? (
          <Button
            type="button"
            className={cn("h-11 flex-1", BUSY)}
            disabled={!valid}
            aria-disabled={pending ? "true" : undefined}
            onClick={() => {
              if (!valid || pending) return;
              setStep((current) => current + 1);
            }}
          >
            {t("qr.next")}
          </Button>
        ) : (
          <Button
            type="button"
            className={cn("h-11 flex-1", BUSY)}
            disabled={!valid}
            aria-disabled={pending ? "true" : undefined}
            onClick={submit}
          >
            {pending ? t("qr.sending") : t("qr.send")}
          </Button>
        )}
      </div>
    </div>
  );
}

/** The read-only summary of a sent wizard: the same values the stored answer carries (QR-16). */
function WizardSummary({
  answers,
  canAnswer,
  headingRef,
}: {
  answers: MessageAnswerItem[];
  canAnswer: boolean;
  /** Focused by the wizard only after its own Send, never when a loaded answer mounts it. */
  headingRef: RefObject<HTMLHeadingElement | null>;
}) {
  const { t } = useI18n();

  const value = (item: MessageAnswerItem) => {
    const labels = item.values.join(", ");
    if (item.text) return labels ? `${labels} (${item.text})` : item.text;
    return labels || t("qr.noOption");
  };

  return (
    <div className="mt-1.5 min-w-0">
      <h4 ref={headingRef} tabIndex={-1} className={cn("text-sm font-semibold break-words", FOCUS)}>
        {t("qr.yourAnswer")}
      </h4>
      <dl className="mt-1 space-y-1 text-sm">
        {answers.map((item) => (
          <div key={`${item.question}-${item.values.join(", ")}-${item.text}`} className="min-w-0">
            <dt className="font-medium break-words">{item.question}</dt>
            <dd className="break-words text-muted-foreground">{value(item)}</dd>
          </div>
        ))}
      </dl>
      {canAnswer ? (
        <p className="mt-1 text-xs text-muted-foreground text-pretty">{t("qr.readOnly")}</p>
      ) : null}
    </div>
  );
}
