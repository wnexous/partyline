import { type RefObject, useId } from "react";
import {
  MaxAnswerText,
  type MessageAnswerItem,
  type QuickReply,
  type WizardQuestion,
} from "@/shared/api/types.gen";
import { useI18n } from "@/shared/i18n";
import { cn } from "@/shared/lib/utils";
import { Button } from "@/shared/ui/primitives/button";

/*
 * What a question bubble shows (QR-10..QR-17): the quick-reply labels, the wizard's step, its
 * read-only form and its summary of the answer. Presentation only: the state and the posts are
 * the answer-question feature's, passed in as props.
 */

/** A label's box: 44 px, wrapping inside the bubble, starting at its own edge (QR-10). */
const CHIP =
  "flex min-h-11 min-w-0 max-w-full flex-1 basis-32 items-center rounded-md border px-3 py-2 text-start text-sm break-words";

/** The focus ring of the wizard's own controls, as the app draws it elsewhere. */
const FOCUS = "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring";

/** A control that is busy stays focusable: aria-disabled and a guard, not a real disabled. */
const BUSY = "aria-disabled:cursor-default aria-disabled:opacity-50";

export interface QuickReplyChoicesProps {
  quickReplies: QuickReply[];
  /** The label the viewer answered with, once there is one. */
  sent: string | undefined;
  /** The labels are buttons the viewer can press; otherwise they are text. */
  acts: boolean;
  disabled: boolean;
  onPick: (option: QuickReply) => void;
  /** The line under the labels: sending, answered, or why nothing can be pressed. */
  status: string | null;
}

export function QuickReplyChoices({
  quickReplies,
  sent,
  acts,
  disabled,
  onPick,
  status,
}: QuickReplyChoicesProps) {
  const { t } = useI18n();
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
                  onPick(option);
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

/** The questions of a wizard the viewer cannot answer: the options as a list, and why. */
export function WizardQuestionList({
  questions,
  reason,
}: {
  questions: WizardQuestion[];
  reason: string | null;
}) {
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

/** The draft of one question as the step shows it: the chosen option ids and the note. */
export interface StepDraft {
  optionIds: readonly string[];
  text: string;
}

export interface WizardStepProps {
  step: number;
  total: number;
  question: WizardQuestion;
  draft: StepDraft | undefined;
  pending: boolean;
  valid: boolean;
  /** Focused by the wizard when the step changes, so the reader hears the new question. */
  headingRef: RefObject<HTMLHeadingElement | null>;
  messageId: string;
  onChoose: (optionId: string) => void;
  onNote: (text: string) => void;
  onBack: () => void;
  onNext: () => void;
  onSubmit: () => void;
}

/** One step of the wizard: its question, the options or the note, and Back, Next or Send (QR-15, QR-16). */
export function WizardStep({
  step,
  total,
  question,
  draft,
  pending,
  valid,
  headingRef,
  messageId,
  onChoose,
  onNote,
  onBack,
  onNext,
  onSubmit,
}: WizardStepProps) {
  const { t } = useI18n();
  const headingId = useId();
  const textId = `${headingId}-text`;
  const hintId = `${headingId}-hint`;

  // A single question that allows text may be answered by the text alone, so the note is the
  // answer there, not an optional extra (QR-15).
  const textLabel =
    question.kind === "single" && question.allow_text
      ? t("qr.textLabelOrOption")
      : t("qr.textLabel");

  return (
    <div className="mt-1.5 min-w-0">
      <p role="status" className="text-xs text-muted-foreground">
        {t("qr.step", { current: step + 1, total })}
      </p>
      <div className="mt-1 min-w-0">
        <h4
          ref={headingRef}
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
                  name={`${messageId}-${question.id}`}
                  value={option.id}
                  checked={draft?.optionIds.includes(option.id) ?? false}
                  aria-disabled={pending ? "true" : undefined}
                  onChange={() => {
                    if (pending) return;
                    onChoose(option.id);
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
                onNote(event.target.value);
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
              onBack();
            }}
          >
            {t("qr.back")}
          </Button>
        ) : null}
        {step < total - 1 ? (
          <Button
            type="button"
            className={cn("h-11 flex-1", BUSY)}
            disabled={!valid}
            aria-disabled={pending ? "true" : undefined}
            onClick={() => {
              if (!valid || pending) return;
              onNext();
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
            onClick={onSubmit}
          >
            {pending ? t("qr.sending") : t("qr.send")}
          </Button>
        )}
      </div>
    </div>
  );
}

/** The read-only summary of a sent wizard: the same values the stored answer carries (QR-16). */
export function WizardSummary({
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
