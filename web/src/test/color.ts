/** OKLCH to sRGB and WCAG contrast, for the tests that pin the palette in `src/index.css`. */

export interface Oklch {
  l: number;
  c: number;
  h: number;
}

/** Gamma-encoded sRGB channels, each 0..1. */
export type Rgb = [number, number, number];

export function parseOklch(value: string): Oklch {
  const m = value.trim().match(/^oklch\(\s*([\d.]+)\s+([\d.]+)\s+([\d.]+)\s*\)$/);
  if (!m) throw new Error(`not an oklch() colour: ${value}`);
  return { l: Number(m[1]), c: Number(m[2]), h: Number(m[3]) };
}

const encode = (v: number) => (v <= 0.0031308 ? 12.92 * v : 1.055 * v ** (1 / 2.4) - 0.055);
const decode = (v: number) => (v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4);
const clamp = (v: number) => Math.min(1, Math.max(0, v));

/** The sRGB colour of an OKLCH one, clipped to the gamut (every palette colour is inside it). */
export function toSrgb({ l, c, h }: Oklch): Rgb {
  const a = c * Math.cos((h * Math.PI) / 180);
  const b = c * Math.sin((h * Math.PI) / 180);
  const lms = [
    l + 0.3963377774 * a + 0.2158037573 * b,
    l - 0.1055613458 * a - 0.0638541728 * b,
    l - 0.0894841775 * a - 1.291485548 * b,
  ].map((v) => v ** 3) as [number, number, number];
  const [x, y, z] = lms;
  return [
    4.0767416621 * x - 3.3077115913 * y + 0.2309699292 * z,
    -1.2684380046 * x + 2.6097574011 * y - 0.3413193965 * z,
    -0.0041960863 * x - 0.7034186147 * y + 1.707614701 * z,
  ].map((v) => encode(clamp(v))) as Rgb;
}

export function toHex(rgb: Rgb): string {
  return `#${rgb
    .map((v) =>
      Math.round(clamp(v) * 255)
        .toString(16)
        .padStart(2, "0"),
    )
    .join("")}`;
}

function luminance(rgb: Rgb): number {
  const [r, g, b] = rgb.map(decode) as [number, number, number];
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

/** WCAG 2 contrast ratio, 1 to 21. */
export function contrast(a: Rgb, b: Rgb): number {
  const [light, dark] = [luminance(a), luminance(b)].sort((p, q) => q - p) as [number, number];
  return (light + 0.05) / (dark + 0.05);
}

/** `fg` painted at `alpha` over `bg`, blended in encoded sRGB like a browser does. */
export function over(fg: Rgb, bg: Rgb, alpha: number): Rgb {
  return fg.map((v, i) => v * alpha + (bg[i] ?? 0) * (1 - alpha)) as Rgb;
}
