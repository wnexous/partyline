import { useAnswerQuestion } from "@/hooks/use-answer-question";
import type { Message } from "@/lib/api/types.gen";
import { useI18n } from "@/lib/i18n";
import { cn } from "@/lib/utils";

/*
 * The question a message carries, under its text (QR-10..QR-13). A quick reply is one tap: the buttons
 * post the answer as a reply at once, hold still while it is sent, and mark the chosen label once it is
 * stored. The state is derived from the timeline (the viewer's answer, `myAnswer`), so a reload keeps it;
 * a tap guard means a second tap posts nothing. The author of the question, a member who may not post and
 * a conversation frozen as history see the labels as plain text and one line saying why.
 */

/** A label's box: 44 px, wrapping inside the bubble, starting at its own edge (QR-10). */
const CHIP =
  "flex min-h-11 min-w-0 max-w-full flex-1 basis-32 items-center rounded-md border px-3 py-2 text-start text-sm break-words";

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

export function QuestionBubble({
  message,
  meId,
  myAnswer,
  canAnswer = false,
  frozen = false,
}: QuestionBubbleProps) {
  const { t } = useI18n();
  const answer = useAnswerQuestion(message.room_id);
  const quickReplies = message.interactive?.quick_replies ?? [];
  if (quickReplies.length === 0) return null;

  const own = message.author_id === meId;
  // The stored answer is the authority: the live event or a reload brings it, the reply of this tap
  // fills the gap while the timeline catches up (QR-11, QR-12).
  const sent = myAnswer?.answers?.quick_reply ?? answer.data?.answers?.quick_reply;
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
                {option.label}
              </button>
            );
          })
        : quickReplies.map((option) => (
            <span key={option.id} className={cn(CHIP, "border-border text-muted-foreground")}>
              {option.label}
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
