import { ChevronLeftIcon, MicIcon, SendHorizontalIcon } from "lucide-react";
import { type PointerEvent, type Ref, useEffect, useRef } from "react";
import { formatDuration } from "@/entities/message";
import { useI18n } from "@/shared/i18n";
import { cn } from "@/shared/lib/utils";
import { Button } from "@/shared/ui/primitives/button";
import { HOLD_MS, SLIDE_CANCEL_PX } from "../lib/voice";
import type { VoicePhase } from "../model/use-voice-recorder";

/*
 * The microphone button and the recording bar of the composer (VOI-02, VOI-03). One button does
 * both jobs, so that a finger can stay on it from the start of a hold to its end: idle it is the
 * microphone, and while a recording is on (or waits to be sent) it is Send. A tap starts a
 * recording that stays on; a hold longer than HOLD_MS records while held, sends on release and
 * cancels when the finger has slid SLIDE_CANCEL_PX to the left.
 */

/** What a finger that is down on the button means so far: nothing yet, a hold, or a hold on its way to cancel. */
export type HoldState = "off" | "holding" | "cancel";

interface Press {
  x: number;
  at: number;
  state: HoldState;
  timer: number;
}

export function MicButton({
  phase,
  sending,
  onStart,
  onSend,
  onCancel,
  onHold,
  className,
  ref,
}: {
  phase: VoicePhase;
  /** A send is in flight: Send waits. */
  sending: boolean;
  onStart: () => void;
  onSend: () => void;
  onCancel: () => void;
  onHold: (state: HoldState) => void;
  className?: string;
  /** The composer moves focus here when a recording hides the text box. */
  ref?: Ref<HTMLButtonElement>;
}) {
  const { t } = useI18n();
  const on = phase === "recording" || phase === "recorded";
  const press = useRef<Press | null>(null);
  /** The click that ends a press that began on the idle microphone is no press on Send. */
  const swallowClick = useRef(false);

  useEffect(() => () => window.clearTimeout(press.current?.timer), []);

  const down = (e: PointerEvent<HTMLButtonElement>) => {
    if (e.pointerType === "mouse" && e.button !== 0) return;
    // A press must not take focus from the text box: that closes the on-screen keyboard (KBD-03).
    e.preventDefault();
    swallowClick.current = false;
    if (phase !== "idle") return;
    swallowClick.current = true;
    try {
      // Keeps the moves and the release coming to this button when the finger slides off it.
      e.currentTarget.setPointerCapture(e.pointerId);
    } catch {
      // A pointer that is no longer there (a synthetic one): nothing to keep.
    }
    press.current = {
      x: e.clientX,
      at: Date.now(),
      state: "off",
      timer: window.setTimeout(() => {
        if (press.current) press.current.state = "holding";
        onHold("holding");
      }, HOLD_MS),
    };
    onStart();
  };

  const move = (e: PointerEvent<HTMLButtonElement>) => {
    const p = press.current;
    // A wobble in the first moments of a tap is not a slide: only a hold slides.
    if (!p || p.state === "off") return;
    const next: HoldState = e.clientX - p.x <= -SLIDE_CANCEL_PX ? "cancel" : "holding";
    if (next === p.state) return;
    p.state = next;
    onHold(next);
  };

  const up = (e: PointerEvent<HTMLButtonElement>) => {
    const p = press.current;
    if (!p) return;
    press.current = null;
    window.clearTimeout(p.timer);
    onHold("off");
    const held = Date.now() - p.at >= HOLD_MS;
    const slid = p.state === "cancel" || e.clientX - p.x <= -SLIDE_CANCEL_PX;
    // A hold that slid away cancels whatever the phase: while the browser is still asking for the
    // microphone, the answer may yet arrive, and it must not start the recording the user cancelled.
    if (held && slid) {
      onCancel();
      return;
    }
    // Let go before the hold time, or before the microphone was even granted: a tap, and the
    // recording stays on with its buttons.
    if (!held || phase !== "recording") return;
    onSend();
  };

  // The system took the pointer (a gesture of its own): what is recording keeps recording.
  const lost = () => {
    const p = press.current;
    if (!p) return;
    press.current = null;
    window.clearTimeout(p.timer);
    onHold("off");
  };

  const click = () => {
    if (swallowClick.current) {
      swallowClick.current = false;
      return;
    }
    // No press came before it: the keyboard (Enter, Space) or a screen reader.
    if (phase === "idle") onStart();
    else onSend();
  };

  return (
    <Button
      ref={ref}
      type="button"
      size="icon-sm"
      variant={on ? "default" : "ghost"}
      disabled={sending}
      aria-label={on ? t("composer.sendVoice") : t("composer.record")}
      aria-busy={phase === "requesting" || undefined}
      title={on ? t("composer.sendVoice") : t("composer.record")}
      onPointerDown={down}
      onPointerMove={move}
      onPointerUp={up}
      onPointerCancel={lost}
      onClick={click}
      onContextMenu={(e) => e.preventDefault()}
      className={cn(
        // A finger slides across it: the page must not take the gesture for a scroll.
        "touch-none select-none [-webkit-touch-callout:none]",
        !on && "text-muted-foreground hover:text-foreground",
        className,
      )}
    >
      {on ? <SendHorizontalIcon aria-hidden="true" /> : <MicIcon aria-hidden="true" />}
    </Button>
  );
}

/**
 * What replaces the text box while a recording is on, or waits to be sent: the state and the time,
 * and under them the live transcript (VOI-09), which scrolls inside a small box on a phone.
 */
export function RecordingBar({
  phase,
  elapsedMs,
  hold,
  transcript,
  unavailable = false,
}: {
  phase: "recording" | "recorded";
  elapsedMs: number;
  hold: HoldState;
  /** What the browser heard so far; empty when it hears nothing. */
  transcript?: string;
  /** The browser has speech recognition but it failed: one muted line, never an alert. */
  unavailable?: boolean;
}) {
  const { t } = useI18n();
  const recording = phase === "recording";
  return (
    <div className="px-3 pt-1.5 pb-1">
      <div className="flex min-h-11 items-center gap-2.5 text-[0.9375rem]">
        <span
          aria-hidden="true"
          className={cn(
            "size-2.5 shrink-0 rounded-full",
            recording ? "animate-line-pulse bg-primary" : "bg-muted-foreground",
          )}
        />
        <span className="font-medium">
          {t(recording ? "composer.recording" : "composer.recordingReady")}
        </span>
        <span role="timer" className="font-mono text-sm text-muted-foreground tabular">
          {formatDuration(elapsedMs / 1000)}
        </span>
        {recording && hold !== "off" ? (
          <span
            className={cn(
              "ml-auto flex min-w-0 items-center gap-0.5 truncate text-xs",
              hold === "cancel" ? "font-medium text-destructive" : "text-muted-foreground",
            )}
          >
            {hold === "cancel" ? null : <ChevronLeftIcon className="size-3.5" aria-hidden="true" />}
            {t(hold === "cancel" ? "composer.recordingRelease" : "composer.recordingSlide")}
          </span>
        ) : null}
      </div>
      {transcript ? (
        <p
          data-testid="live-transcript"
          className="mt-0.5 max-h-20 overflow-y-auto overscroll-contain pr-1 text-xs leading-snug break-words text-muted-foreground"
        >
          {transcript}
        </p>
      ) : unavailable ? (
        <p className="mt-0.5 text-xs leading-snug text-muted-foreground">
          {t("composer.transcriptUnavailable")}
        </p>
      ) : null}
    </div>
  );
}
