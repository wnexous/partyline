import type { APIRequestContext } from "@playwright/test";
import { expect } from "./fixtures";
import type { Instance } from "./server";

/**
 * A synthetic recording: a soft tone as an 8 kHz mono WAV, so a test needs no audio file and every
 * browser can play it whatever type the upload declares for it.
 */
export function makeWav(seconds: number, hz = 330): Buffer {
  const rate = 8000;
  const samples = Math.round(rate * seconds);
  const out = Buffer.alloc(44 + samples);
  out.write("RIFF", 0, "ascii");
  out.writeUInt32LE(36 + samples, 4);
  out.write("WAVEfmt ", 8, "ascii");
  out.writeUInt32LE(16, 16); // size of the format chunk
  out.writeUInt16LE(1, 20); // PCM
  out.writeUInt16LE(1, 22); // mono
  out.writeUInt32LE(rate, 24);
  out.writeUInt32LE(rate, 28); // bytes per second
  out.writeUInt16LE(1, 32); // bytes per sample frame
  out.writeUInt16LE(8, 34); // bits per sample
  out.write("data", 36, "ascii");
  out.writeUInt32LE(samples, 40);
  for (let i = 0; i < samples; i += 1) {
    out[44 + i] = 128 + Math.round(40 * Math.sin((2 * Math.PI * hz * i) / rate));
  }
  return out;
}

/**
 * Posts a voice message as the logged-in user (multipart: the message part, then the recording as
 * a `file` part), the way the web composer does. The bytes are a WAV; the name and type are a
 * recording's.
 */
export async function postVoice(
  request: APIRequestContext,
  instance: Instance,
  roomId: string,
  { seconds, stamp }: { seconds: number; stamp: string },
) {
  const form = new FormData();
  form.append("message", JSON.stringify({ text: "" }));
  form.append(
    "file",
    new Blob([new Uint8Array(makeWav(seconds))], { type: "audio/webm;codecs=opus" }),
    `voice-message-${stamp}.webm`,
  );
  const res = await request.post(`${instance.url}/api/v1/rooms/${roomId}/messages`, {
    multipart: form,
    headers: { Origin: instance.url },
  });
  expect(res.ok(), `post voice → ${res.status()}`).toBe(true);
}
