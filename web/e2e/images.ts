import { deflateSync } from "node:zlib";
import type { APIRequestContext } from "@playwright/test";
import { expect } from "./fixtures";
import type { Instance } from "./server";

const CRC_TABLE = Array.from({ length: 256 }, (_, n) => {
  let c = n;
  for (let k = 0; k < 8; k += 1) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
  return c >>> 0;
});

function crc32(bytes: Buffer): number {
  let c = 0xffffffff;
  for (const byte of bytes) c = (CRC_TABLE[(c ^ byte) & 0xff] ?? 0) ^ (c >>> 8);
  return (c ^ 0xffffffff) >>> 0;
}

function chunk(type: string, data: Buffer): Buffer {
  const body = Buffer.concat([Buffer.from(type, "ascii"), data]);
  const out = Buffer.alloc(body.length + 8);
  out.writeUInt32BE(data.length, 0);
  body.copy(out, 4);
  out.writeUInt32BE(crc32(body), body.length + 4);
  return out;
}

/** A synthetic picture: `pixel` gives the [r, g, b] of each point, so a test needs no image file. */
export function makePng(
  width: number,
  height: number,
  pixel: (x: number, y: number) => [number, number, number],
): Buffer {
  const stride = width * 3 + 1;
  const raw = Buffer.alloc(stride * height);
  for (let y = 0; y < height; y += 1) {
    for (let x = 0; x < width; x += 1) {
      const [r, g, b] = pixel(x, y);
      const at = y * stride + 1 + x * 3;
      raw[at] = r;
      raw[at + 1] = g;
      raw[at + 2] = b;
    }
  }
  const header = Buffer.alloc(13);
  header.writeUInt32BE(width, 0);
  header.writeUInt32BE(height, 4);
  header[8] = 8; // bit depth
  header[9] = 2; // truecolor
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk("IHDR", header),
    chunk("IDAT", deflateSync(raw)),
    chunk("IEND", Buffer.alloc(0)),
  ]);
}

function hsl(h: number, s: number, l: number): [number, number, number] {
  const a = s * Math.min(l, 1 - l);
  const f = (n: number) => {
    const k = (n + h / 30) % 12;
    return Math.round(255 * (l - a * Math.max(-1, Math.min(k - 3, 9 - k, 1))));
  };
  return [f(0), f(8), f(4)];
}

/** A 3:2 landscape (a sky over rolling dunes) tinted by `hue`; big enough to have to be scaled to fit. */
export function landscape(hue: number): Buffer {
  const width = 1500;
  const height = 1000;
  return makePng(width, height, (x, y) => {
    const dune = 620 + Math.sin(x / 150 + hue / 40) * 50 + Math.sin(x / 61 + hue) * 12;
    if (y > dune) return hsl((hue + 25) % 360, 0.38, 0.42 - ((y - dune) / (height - dune)) * 0.24);
    return hsl(hue % 360, 0.5, 0.82 - (y / dune) * 0.3);
  });
}

/**
 * Posts a message with pictures as the logged-in user (multipart: the message part, then one
 * `file` part per picture), the way the web composer does.
 */
export async function postPictures(
  request: APIRequestContext,
  instance: Instance,
  roomId: string,
  pictures: { name: string; hue: number }[],
  text = "",
) {
  const form = new FormData();
  form.append("message", JSON.stringify({ text }));
  for (const { name, hue } of pictures) {
    form.append("file", new Blob([new Uint8Array(landscape(hue))], { type: "image/png" }), name);
  }
  const res = await request.post(`${instance.url}/api/v1/rooms/${roomId}/messages`, {
    multipart: form,
    headers: { Origin: instance.url },
  });
  expect(res.ok(), `post pictures → ${res.status()}`).toBe(true);
}
