/*
 * Voice messages as the conversations read them (VOI-01..VOI-08): which file is a recording, what a
 * conversation list says for one, whether an attachment is audio, and how a length reads. The
 * recorder itself is features/send-voice-message.
 */

/** The type without its parameters, lower-cased: `audio/webm;codecs=opus` is `audio/webm`. */
export const mimeBaseType = (mime: string) => (mime.split(";")[0] ?? "").trim().toLowerCase();

const pad = (n: number, width = 2) => String(n).padStart(width, "0");

const VOICE_NAME = /^voice-message-\d{4}-\d{2}-\d{2}-\d{6}\.[a-z0-9]+$/;

/** True for the file a recording makes: the list of conversations names such a message. */
export const isVoiceName = (name: string) => VOICE_NAME.test(name);

/** The preview of a conversation for a message that is only a recording: `[voice-message-….webm]`. */
export function isVoicePreview(text: string): boolean {
  const name = /^\[(.+)\]$/.exec(text)?.[1];
  return name !== undefined && isVoiceName(name);
}

/** An audio type, whatever parameters (`;codecs=opus`) the recorder added to it. */
export const isAudio = (mime: string) => mimeBaseType(mime).startsWith("audio/");

/** `m:ss`, the seconds always two digits and the fraction dropped; minutes keep counting past an hour. */
export function formatDuration(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return "0:00";
  const whole = Math.floor(seconds);
  return `${Math.floor(whole / 60)}:${pad(whole % 60)}`;
}
