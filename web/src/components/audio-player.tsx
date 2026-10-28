import { FileIcon, PauseIcon, PlayIcon } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Button } from "@/shared/ui/primitives/button";
import { attachmentUrl } from "@/lib/api/queries";
import type { Attachment } from "@/shared/api/types.gen";
import { useI18n } from "@/shared/i18n";
import { formatDuration, isVoiceName } from "@/lib/voice";

/*
 * The compact player of an audio attachment (VOI-07, VOI-08): play and pause, the time and a seek
 * bar, which is a native range input, so it is a slider with its position as text for assistive
 * technology and moves with the arrow keys. Starting one pauses the one that was playing.
 */

/** The player that is playing; the next one to start pauses it. */
let playing: HTMLAudioElement | null = null;

/** Past the end of any recording: asking for it makes the element work out how long the file is. */
const FAR_PAST_THE_END = 1e101;

export function AudioPlayer({ attachment: a }: { attachment: Attachment }) {
  const { t } = useI18n();
  const audio = useRef<HTMLAudioElement>(null);
  const [isPlaying, setIsPlaying] = useState(false);
  const [time, setTime] = useState(0);
  /** Seconds, or null until the file says how long it is. */
  const [duration, setDuration] = useState<number | null>(null);
  const [failed, setFailed] = useState(false);
  /** The trip to the end that tells how long a recording is (VOI-08), in progress. */
  const probing = useRef(false);
  const url = attachmentUrl(a.id);

  // The player leaves the page (the row scrolled far away, the conversation closed): it stops.
  useEffect(() => {
    const el = audio.current;
    return () => {
      el?.pause();
      if (playing === el) playing = null;
    };
  }, []);

  /**
   * What the element knows of its length. MediaRecorder writes none into a WebM, so a recording of
   * this app reports Infinity until something asks for its end: seeking far past it does, and the
   * element then goes back to the start.
   */
  const learnLength = () => {
    const el = audio.current;
    if (!el) return;
    if (Number.isFinite(el.duration)) {
      setDuration(el.duration);
      if (probing.current) {
        probing.current = false;
        el.currentTime = 0;
        setTime(0);
      }
    } else if (el.duration === Number.POSITIVE_INFINITY && !probing.current) {
      probing.current = true;
      el.currentTime = FAR_PAST_THE_END;
    }
  };

  const toggle = () => {
    const el = audio.current;
    if (!el) return;
    if (!el.paused) {
      el.pause();
      return;
    }
    // Played before the trip to the end came back: start from the beginning, length or not.
    if (probing.current) {
      probing.current = false;
      el.currentTime = 0;
    }
    void el.play().catch(() => {});
  };

  if (failed) {
    return (
      <div className="flex max-w-full flex-col items-start gap-1">
        <a
          href={url}
          download={a.name}
          className="inline-flex max-w-full items-center gap-1.5 rounded-md border bg-card px-2 py-1 text-xs shadow-xs transition-colors hover:bg-accent"
        >
          <FileIcon className="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
          <span className="truncate font-medium">{a.name}</span>
        </a>
        <p className="text-xs text-muted-foreground">{t("player.unavailable")}</p>
      </div>
    );
  }

  const total = duration ?? 0;
  return (
    // biome-ignore lint/a11y/useSemanticElements: a row of controls named as one group; a fieldset brings a border, a legend and a min-width of its own
    <div
      role="group"
      aria-label={isVoiceName(a.name) ? t("player.voice") : a.name}
      className="flex h-11 w-72 max-w-full items-center gap-1.5 rounded-md border bg-card pr-3 pl-1.5 shadow-xs pointer-coarse:h-12"
    >
      {/* biome-ignore lint/a11y/useMediaCaption: a voice message has no captions to offer */}
      <audio
        ref={audio}
        src={url}
        preload="metadata"
        onLoadedMetadata={learnLength}
        onDurationChange={learnLength}
        onTimeUpdate={(e) => {
          if (!probing.current) setTime(e.currentTarget.currentTime);
        }}
        onPlay={(e) => {
          const el = e.currentTarget;
          // One at a time: whatever was playing gives way.
          if (playing && playing !== el) playing.pause();
          playing = el;
          setIsPlaying(true);
        }}
        onPause={(e) => {
          if (playing === e.currentTarget) playing = null;
          setIsPlaying(false);
        }}
        onEnded={(e) => {
          if (playing === e.currentTarget) playing = null;
          setIsPlaying(false);
          e.currentTarget.currentTime = 0;
          setTime(0);
        }}
        onError={() => setFailed(true)}
      />
      <Button
        type="button"
        variant="secondary"
        size="icon-sm"
        className="size-8 rounded-full pointer-coarse:size-11"
        aria-label={isPlaying ? t("player.pause") : t("player.play")}
        onClick={toggle}
      >
        {isPlaying ? <PauseIcon aria-hidden="true" /> : <PlayIcon aria-hidden="true" />}
      </Button>
      <input
        type="range"
        min={0}
        max={total}
        step={1}
        value={Math.min(time, total)}
        disabled={duration === null}
        aria-label={t("player.seek")}
        aria-valuetext={t("player.position", {
          time: formatDuration(time),
          total: formatDuration(total),
        })}
        onChange={(e) => {
          const next = Number(e.target.value);
          if (audio.current) audio.current.currentTime = next;
          setTime(next);
        }}
        className="h-6 min-w-0 flex-1 cursor-pointer pointer-coarse:h-11 disabled:cursor-default"
      />
      <span className="shrink-0 font-mono text-[0.7rem] text-muted-foreground tabular">
        {duration === null
          ? formatDuration(time)
          : `${formatDuration(time)} / ${formatDuration(duration)}`}
      </span>
    </div>
  );
}
