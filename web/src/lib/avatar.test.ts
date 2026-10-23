import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AvatarMaxSide, prepareAvatar } from "./avatar";

/*
 * A chosen photo is made small enough before it is sent (PROF-06): a file over 2 MiB or over
 * 1024 px on a side is re-encoded at no more than 1024 px on its longest side, so the upload
 * does not stall on the server's limit.
 */

const png = (size: number) => new File([new Uint8Array(size)], "photo.png", { type: "image/png" });

const bitmapOf = (width: number, height: number) => ({
  width,
  height,
  close: vi.fn(),
});

let decoded: ReturnType<typeof bitmapOf>;
let drawn: { width: number; height: number };
let encoded: { type?: string; quality?: number }[];

beforeEach(() => {
  decoded = bitmapOf(800, 600);
  vi.stubGlobal(
    "createImageBitmap",
    vi.fn(async () => decoded),
  );
  drawn = { width: 0, height: 0 };
  encoded = [];
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockImplementation(
    () =>
      ({
        drawImage: (
          _bitmap: ImageBitmap,
          _x: number,
          _y: number,
          width: number,
          height: number,
        ) => {
          drawn = { width, height };
        },
      }) as unknown as CanvasRenderingContext2D,
  );
  vi.spyOn(HTMLCanvasElement.prototype, "toBlob").mockImplementation(function (
    this: HTMLCanvasElement,
    callback: BlobCallback,
    type?: string,
    quality?: number,
  ) {
    encoded.push({ type, quality });
    callback(new Blob([`${type}`], { type }));
  });
});
afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("preparing a profile photo (PROF-06)", () => {
  it("sends a file that already fits as it is", async () => {
    const file = png(1000);
    expect(await prepareAvatar(file)).toBe(file);
    expect(encoded).toEqual([]);
    expect(decoded.close).toHaveBeenCalled();
  });

  it("resizes a large photo to 1024 px on its longest side and encodes it as WebP", async () => {
    decoded = bitmapOf(3000, 2000);
    const file = png(5 << 20);
    const out = await prepareAvatar(file);

    expect(out).toBeInstanceOf(Blob);
    expect(out.type).toBe("image/webp");
    expect(drawn).toEqual({ width: AvatarMaxSide, height: 683 });
    expect(encoded).toEqual([{ type: "image/webp", quality: 0.85 }]);
    expect(decoded.close).toHaveBeenCalled();
  });

  it("re-encodes a heavy photo without enlarging it", async () => {
    const out = await prepareAvatar(png(3 << 20));
    expect(out.type).toBe("image/webp");
    expect(drawn).toEqual({ width: 800, height: 600 });
  });

  it("falls back to JPEG when the browser cannot encode WebP", async () => {
    vi.mocked(HTMLCanvasElement.prototype.toBlob).mockImplementation(function (
      this: HTMLCanvasElement,
      callback: BlobCallback,
      type?: string,
      quality?: number,
    ) {
      encoded.push({ type, quality });
      callback(new Blob([`${type}`], { type: type === "image/webp" ? "image/png" : type }));
    });
    const out = await prepareAvatar(png(3 << 20));
    expect(out.type).toBe("image/jpeg");
    expect(encoded.map((e) => e.type)).toEqual(["image/webp", "image/jpeg"]);
    expect(encoded[1]?.quality).toBe(0.85);
  });

  it("rejects a file that is not an image", async () => {
    vi.stubGlobal(
      "createImageBitmap",
      vi.fn(async () => {
        throw new DOMException("could not decode", "InvalidStateError");
      }),
    );
    await expect(prepareAvatar(png(10))).rejects.toThrow("could not decode");
  });
});
