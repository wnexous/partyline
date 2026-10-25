/*
 * Voice messages, the parts that need no browser (VOI-01..VOI-08): which format to record in, what
 * to call the file, how long it was, and what a refused microphone means.
 */

/** A recording stops by itself here and waits to be sent (VOI-05). */
export const MAX_RECORDING_MS = 10 * 60_000;
/** A voice at 32 kbit/s is clear and small: ten minutes are under 2.5 MB. */
export const AUDIO_BITS_PER_SECOND = 32_000;
/** A press on the microphone this long is a hold: it records while held (VOI-03). */
export const HOLD_MS = 500;
/** Sliding the finger this far to the left, in a hold, cancels the recording (VOI-03). */
export const SLIDE_CANCEL_PX = 80;

/** What Chrome (WebM) and Firefox (Ogg) record, in the order of preference (VOI-04). */
const RECORDER_MIMES = ["audio/webm;codecs=opus", "audio/ogg;codecs=opus"] as const;

/**
 * The format to ask a MediaRecorder for, or undefined to let the browser record what it records
 * (Safari: MP4). `isTypeSupported` is `MediaRecorder.isTypeSupported`.
 */
export function pickRecorderMime(isTypeSupported: (type: string) => boolean): string | undefined {
  return RECORDER_MIMES.find((type) => isTypeSupported(type));
}

const baseType = (mime: string) => (mime.split(";")[0] ?? "").trim().toLowerCase();

const EXTENSIONS: Record<string, string> = {
  "audio/webm": "webm",
  "audio/ogg": "ogg",
  "audio/mp4": "m4a",
  "audio/mpeg": "mp3",
  "audio/wav": "wav",
};

const pad = (n: number, width = 2) => String(n).padStart(width, "0");

/** `voice-message-2026-10-24-140509.webm`: named after the moment, in the extension of its format. */
export function voiceFileName(mime: string, at: Date = new Date()): string {
  const day = `${at.getFullYear()}-${pad(at.getMonth() + 1)}-${pad(at.getDate())}`;
  const time = `${pad(at.getHours())}${pad(at.getMinutes())}${pad(at.getSeconds())}`;
  return `voice-message-${day}-${time}.${EXTENSIONS[baseType(mime)] ?? "audio"}`;
}

const VOICE_NAME = /^voice-message-\d{4}-\d{2}-\d{2}-\d{6}\.[a-z0-9]+$/;

/** True for the file a recording makes: the list of conversations names such a message. */
export const isVoiceName = (name: string) => VOICE_NAME.test(name);

/** The preview of a conversation for a message that is only a recording: `[voice-message-….webm]`. */
export function isVoicePreview(text: string): boolean {
  const name = /^\[(.+)\]$/.exec(text)?.[1];
  return name !== undefined && isVoiceName(name);
}

/** An audio type, whatever parameters (`;codecs=opus`) the recorder added to it. */
export const isAudio = (mime: string) => baseType(mime).startsWith("audio/");

/** `m:ss`, the seconds always two digits and the fraction dropped; minutes keep counting past an hour. */
export function formatDuration(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return "0:00";
  const whole = Math.floor(seconds);
  return `${Math.floor(whole / 60)}:${pad(whole % 60)}`;
}

/** Why the microphone could not be had: refused, not there, in use, or anything else (VOI-06). */
export type VoiceFailure = "denied" | "missing" | "busy" | "failed";

const FAILURES: Record<string, VoiceFailure> = {
  NotAllowedError: "denied",
  PermissionDeniedError: "denied",
  SecurityError: "denied",
  NotFoundError: "missing",
  DevicesNotFoundError: "missing",
  OverconstrainedError: "missing",
  NotReadableError: "busy",
  TrackStartError: "busy",
  AbortError: "busy",
};

/** The failure a rejected `getUserMedia` stands for, by the name of its error. */
export function micFailure(error: unknown): VoiceFailure {
  const name =
    typeof error === "object" && error !== null ? (error as { name?: unknown }).name : "";
  return (typeof name === "string" && FAILURES[name]) || "failed";
}

/** Whether this browser can record at all: MediaRecorder and a microphone API (VOI-01). */
export function canRecord(): boolean {
  return (
    typeof MediaRecorder === "function" &&
    typeof navigator !== "undefined" &&
    typeof navigator.mediaDevices?.getUserMedia === "function"
  );
}
