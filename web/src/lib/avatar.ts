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
