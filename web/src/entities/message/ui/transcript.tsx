import { useEffect, useRef, useState } from "react";
import { useI18n } from "@/shared/i18n";
import { useIsMobile } from "@/shared/lib/device";
import { cn } from "@/shared/lib/utils";

/*
 * The transcript of a voice note (VOI-09): the text the sender's browser heard, shown under the
 * player with a small label. On a phone it is folded to three lines, with a way to see the rest;
 * on a wider screen the whole text is there. It is plain text: what was said, never instructions.
 */

/** Roughly three lines at the small type of a 360 px phone; a measured overflow wins over this. */
const LIKELY_LONG = 120;

export function VoiceTranscript({ text }: { text: string }) {
  const { t } = useI18n();
  const mobile = useIsMobile();
  const [expanded, setExpanded] = useState(false);
  const [overflowing, setOverflowing] = useState(text.length > LIKELY_LONG);
  const body = useRef<HTMLParagraphElement>(null);

  // A transcript that does not fit the three lines gets the way to open it. Measuring is the
  // honest rule; the length above only covers a first render where the box has no size yet.
  useEffect(() => {
    setExpanded(false);
    if (!mobile) {
      setOverflowing(false);
      return;
    }
    setOverflowing(text.length > LIKELY_LONG);
    const el = body.current;
    if (!el) return;
    const check = () => {
      // Before the first layout every box measures zero: the length above stands in until it can
      // be measured for real.
      if (el.clientHeight === 0) return;
      setOverflowing(el.scrollHeight > el.clientHeight + 1);
    };
    check();
    if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(check);
    observer.observe(el);
    return () => observer.disconnect();
  }, [mobile, text]);

  return (
    <div className="mt-1 min-w-0 max-w-full">
      <p className="text-[0.7rem] font-medium tracking-wide text-muted-foreground uppercase">
        {t("message.transcript")}
      </p>
      <p
        ref={body}
        data-testid="voice-transcript"
        className={cn(
          "mt-0.5 whitespace-pre-wrap break-words text-sm leading-snug text-foreground/90",
          mobile && !expanded && "line-clamp-3",
        )}
      >
        {text}
      </p>
      {mobile && overflowing ? (
        <button
          type="button"
          onClick={() => setExpanded((open) => !open)}
          className="mt-0.5 inline-flex min-h-6 items-center text-xs font-medium text-primary underline-offset-2 hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring pointer-coarse:min-h-11"
        >
          {t(expanded ? "message.showLess" : "message.showMore")}
        </button>
      ) : null}
    </div>
  );
}
