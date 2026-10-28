/** The avatar limits the server enforces (PROF-06): 2 MiB and a sane decoded size. */
export const AvatarMaxBytes = 2 << 20;
export const AvatarMaxSide = 1024;
const QUALITY = 0.85;

/**
 * The image to upload for a chosen file. A file that already fits is sent as it is; a photo over
 * 2 MiB or over 1024 px on a side is drawn to a canvas at most 1024 px on its longest side and
 * re-encoded as WebP (JPEG when the browser cannot encode WebP), so a big camera photo still fits
 * the server's limit instead of stalling the upload. Decoding a file that is not an image rejects.
 */
export async function prepareAvatar(file: File): Promise<Blob> {
  // Older browsers (and happy-dom) cannot decode: send the file and let the server judge it.
  if (typeof createImageBitmap !== "function") return file;
  const bitmap = await createImageBitmap(file);
  try {
    const longest = Math.max(bitmap.width, bitmap.height);
    if (file.size <= AvatarMaxBytes && longest <= AvatarMaxSide) return file;
    const scale = longest > 0 ? Math.min(1, AvatarMaxSide / longest) : 1;
    const canvas = document.createElement("canvas");
    canvas.width = Math.max(1, Math.round(bitmap.width * scale));
    canvas.height = Math.max(1, Math.round(bitmap.height * scale));
    const ctx = canvas.getContext("2d");
    if (!ctx) return file;
    ctx.drawImage(bitmap, 0, 0, canvas.width, canvas.height);
    const webp = await encode(canvas, "image/webp");
    if (webp?.type === "image/webp") return webp;
    // A browser without a WebP encoder silently answers a PNG; JPEG is the asked-for fallback.
    return (await encode(canvas, "image/jpeg")) ?? file;
  } finally {
    bitmap.close();
  }
}

function encode(canvas: HTMLCanvasElement, type: string): Promise<Blob | null> {
  return new Promise((resolve) => canvas.toBlob(resolve, type, QUALITY));
}

/*
 * Face colours (PROF-08): initials on a soft tint picked from the design tokens by a hash of the
 * id. Only the id drives the tint, never the kind: people, AIs and organizations are drawn from
 * the same palette and look alike, and a rename keeps the face. Red is for errors and green for
 * presence, so neither is a tint.
 */

const TONES = [
  "bg-tint-apricot text-tint-apricot-foreground",
  "bg-tint-ochre text-tint-ochre-foreground",
  "bg-tint-sage text-tint-sage-foreground",
  "bg-tint-teal text-tint-teal-foreground",
  "bg-tint-sky text-tint-sky-foreground",
  "bg-tint-indigo text-tint-indigo-foreground",
  "bg-tint-violet text-tint-violet-foreground",
  "bg-tint-rose text-tint-rose-foreground",
] as const;

/** First letter of the first and of the last word; the single letter for a one-word name. */
export function initials(displayName: string): string {
  const words = displayName.trim().split(/\s+/).filter(Boolean);
  const firstWord = words[0];
  if (!firstWord) return "?";
  const lastWord = words.length > 1 ? words[words.length - 1] : undefined;
  const first = Array.from(firstWord)[0] ?? "";
  const last = lastWord ? (Array.from(lastWord)[0] ?? "") : "";
  return `${first}${last}`.toUpperCase();
}

/** Token classes for the initials: one of the eight tints, stable per id and blind to the kind. */
export function avatarTone(id: string): string {
  let hash = 0;
  for (let i = 0; i < id.length; i += 1) {
    hash = (hash * 31 + id.charCodeAt(i)) >>> 0;
  }
  return TONES[hash % TONES.length] ?? TONES[0];
}
