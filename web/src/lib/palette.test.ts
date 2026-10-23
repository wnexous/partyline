import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { THEME_COLORS } from "@/lib/theme";
import { contrast, type Oklch, over, parseOklch, type Rgb, toHex, toSrgb } from "@/test/color";

/*
 * The neutral palette (product owner's brief, 2026-10-23): black, greys and white, plus one muted
 * red that only errors and destructive actions use; WCAG AA in light and dark, focus ring
 * included. The tokens are read straight from src/index.css and the sources are scanned for
 * colours that bypass them. DESIGN.md explains the roles.
 */

const here = (relative: string) => fileURLToPath(new URL(relative, import.meta.url));
const css = readFileSync(here("../index.css"), "utf8");

const MODES = [
  ["light", ":root"],
  ["dark", ".dark"],
] as const;

function tokenBlock(selector: string): string {
  const start = css.indexOf(`${selector} {`);
  return css.slice(start, css.indexOf("\n}", start) + 2);
}

function tokens(selector: string): Record<string, Oklch> {
  const colours: Record<string, Oklch> = {};
  for (const m of tokenBlock(selector).matchAll(/--([a-z-]+):\s*(oklch\([^)]*\))\s*;/g)) {
    if (m[1] && m[2]) colours[m[1]] = parseOklch(m[2]);
  }
  return colours;
}

describe("colour helpers", () => {
  it("compute the WCAG ratio and the sRGB hex the browser chrome needs", () => {
    expect(contrast([0, 0, 0], [1, 1, 1])).toBeCloseTo(21, 5);
    expect(contrast([1, 1, 1], [1, 1, 1])).toBe(1);
    expect(toHex(toSrgb(parseOklch("oklch(0.985 0 0)")))).toBe("#fafafa");
    expect(toHex(toSrgb(parseOklch("oklch(1 0 0)")))).toBe("#ffffff");
  });
});

describe("palette tokens", () => {
  it.each(MODES)(
    "%s: every token is a grey except the muted destructive red",
    (_mode, selector) => {
      const colours = tokens(selector);
      expect(Object.keys(colours).length).toBeGreaterThan(25);
      const chromatic = Object.entries(colours)
        .filter(([, colour]) => colour.c > 0)
        .map(([name]) => name);
      expect(chromatic).toEqual(["destructive"]);
      expect(colours.destructive?.c).toBeLessThanOrEqual(0.1);
    },
  );

  it("has the same tokens in both themes", () => {
    expect(Object.keys(tokens(".dark")).sort()).toEqual(Object.keys(tokens(":root")).sort());
  });
});

/** A surface is a token, or a token painted at some alpha over the previous layer. */
type Surface = string[];

function paint(colours: Record<string, Oklch>, surface: Surface): Rgb {
  let canvas: Rgb | undefined;
  for (const layer of surface) {
    const [name = "", percent] = layer.split("/");
    const token = colours[name];
    if (!token) throw new Error(`no --${name} token`);
    const rgb = toSrgb(token);
    canvas = canvas && percent ? over(rgb, canvas, Number(percent) / 100) : rgb;
  }
  if (!canvas) throw new Error("empty surface");
  return canvas;
}

function ratio(colours: Record<string, Oklch>, fg: string, surface: Surface): number {
  const bg = paint(colours, surface);
  const [name = "", percent] = fg.split("/");
  const token = colours[name];
  if (!token) throw new Error(`no --${name} token`);
  const rgb = toSrgb(token);
  return contrast(percent ? over(rgb, bg, Number(percent) / 100) : rgb, bg);
}

interface Pair {
  fg: string;
  surface: Surface;
}

const BASES = ["background", "card", "popover", "sidebar"];
const FILLS = ["secondary", "muted", "accent", "sidebar-accent", "tape"];
const TINTS = [
  "tape/40",
  "tape/50",
  "tape/60",
  "tape/70",
  "tape/80",
  "accent/35",
  "accent/40",
  "accent/50",
  "accent/60",
  "muted/50",
  "sidebar-accent/60",
];
/** Token pairs the components always use together as fill and ink. */
const INK_ON_FILL: [fill: string, ink: string][] = [
  ["primary", "primary-foreground"],
  ["secondary", "secondary-foreground"],
  ["accent", "accent-foreground"],
  ["card", "card-foreground"],
  ["popover", "popover-foreground"],
  ["sidebar", "sidebar-foreground"],
  ["sidebar-accent", "sidebar-accent-foreground"],
  ["sidebar-primary", "sidebar-primary-foreground"],
  ["tape", "tape-foreground"],
  ["destructive", "destructive-foreground"],
];

function textPairs(): Pair[] {
  const pairs: Pair[] = [];
  const add = (fg: string, ...surface: string[]) => pairs.push({ fg, surface });
  for (const surface of [...BASES, ...FILLS]) {
    add("foreground", surface);
    add("muted-foreground", surface);
  }
  for (const [fill, ink] of INK_ON_FILL) add(ink, fill);
  for (const base of BASES) {
    for (const tint of TINTS) {
      add("foreground", base, tint);
      add("muted-foreground", base, tint);
    }
    for (const alpha of [40, 50, 60, 70, 80]) add("tape-foreground", base, `tape/${alpha}`);
    add("foreground/80", base);
    add("foreground/85", base);
    // Errors: the red as text, on its own tint, and the text that sits on that tint.
    add("destructive", base);
    for (const tint of ["destructive/8", "destructive/10"]) {
      add("destructive", base, tint);
      add("foreground", base, tint);
    }
  }
  add("destructive", "muted");
  add("destructive-foreground", "background", "destructive/90");
  add("destructive-foreground", "card", "destructive/90");
  add("primary-foreground", "background", "primary/90");
  add("tape-foreground/80", "tape");
  add("foreground/60", "muted");
  // The inverted live banner and countdown chip; avatar initials on the mid and dark greys.
  add("background", "foreground");
  add("background/80", "foreground");
  add("background", "tape-strong");
  add("background", "muted-foreground");
  add("foreground", "input");
  return pairs;
}

function uiPairs(): Pair[] {
  const pairs: Pair[] = [];
  const add = (fg: string, ...surface: string[]) => pairs.push({ fg, surface });
  for (const base of BASES) {
    // Control borders, the focus ring and its 50 % and 60 % halos, dots, checked boxes.
    for (const fg of ["input", "ring", "ring/50", "ring/60", "tape-strong", "muted-foreground"]) {
      add(fg, base);
    }
    add("destructive", base);
    add("primary", base);
    for (const alpha of [40, 60, 70]) {
      add("tape-strong", base, `tape/${alpha}`);
      add("ring/50", base, `tape/${alpha}`);
    }
  }
  add("sidebar-ring", "sidebar");
  add("sidebar-ring", "sidebar-accent");
  add("sidebar-ring", "sidebar", "tape");
  return pairs;
}

function failures(selector: string, pairs: Pair[], min: number): string[] {
  const colours = tokens(selector);
  return pairs
    .map(({ fg, surface }) => ({
      label: `${fg} on ${surface.join(" over ")}`,
      value: ratio(colours, fg, surface),
    }))
    .filter(({ value }) => value < min)
    .map(({ label, value }) => `${label}: ${value.toFixed(2)} < ${min}`);
}

describe("palette contrast (WCAG AA)", () => {
  it.each(MODES)("%s: text is at least 4.5:1 on every surface it sits on", (_mode, selector) => {
    expect(failures(selector, textPairs(), 4.5)).toEqual([]);
  });

  it.each(MODES)("%s: controls, dots and the focus ring are at least 3:1", (_mode, selector) => {
    expect(failures(selector, uiPairs(), 3)).toEqual([]);
  });
});

describe("browser chrome", () => {
  const html = readFileSync(here("../../index.html"), "utf8");

  it("theme-color is the page background of each theme", () => {
    expect(THEME_COLORS.light).toBe(toHex(toSrgb(tokens(":root").background as Oklch)));
    expect(THEME_COLORS.dark).toBe(toHex(toSrgb(tokens(".dark").background as Oklch)));
  });

  it("index.html carries the same colours for first paint", () => {
    const metas = [
      ...html.matchAll(
        /<meta name="theme-color" content="(#[0-9a-f]{6})" media="\(prefers-color-scheme: (light|dark)\)"/g,
      ),
    ];
    expect(Object.fromEntries(metas.map((m) => [m[2], m[1]]))).toEqual({
      light: THEME_COLORS.light,
      dark: THEME_COLORS.dark,
    });
  });
});

function sources(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) return sources(path);
    const isSource = /\.tsx?$/.test(entry.name) && !/\.(test|gen|d)\.tsx?$/.test(entry.name);
    return isSource ? [path] : [];
  });
}

const HEX = /#(?:[0-9a-fA-F]{8}|[0-9a-fA-F]{6}|[0-9a-fA-F]{3,4})\b/g;
const isGreyHex = (hex: string) => {
  const digits = hex.slice(1);
  const step = digits.length <= 4 ? 1 : 2;
  const [r, g, b] = [0, 1, 2].map((i) => digits.slice(i * step, (i + 1) * step));
  return r === g && g === b;
};

describe("no colour bypasses the tokens", () => {
  const files = sources(here(".."));
  const read = (path: string) => readFileSync(path, "utf8");

  it("finds the sources it scans", () => {
    expect(files.length).toBeGreaterThan(100);
  });

  it("uses no functional colour notation in components", () => {
    const hits = files.filter((f) =>
      /\b(?:rgba?|hsla?|hwb|lab|lch|oklab|oklch)\(\s*\d/.test(read(f)),
    );
    expect(hits).toEqual([]);
  });

  it("uses only grey hex literals", () => {
    const hits = files.flatMap((f) =>
      (read(f).match(HEX) ?? []).filter((hex) => !isGreyHex(hex)).map((hex) => `${f}: ${hex}`),
    );
    expect(hits).toEqual([]);
  });

  it("uses no Tailwind palette colours (tokens only)", () => {
    const palette =
      /\b(?:bg|text|border|ring|outline|fill|stroke|from|via|to|decoration|divide|shadow|accent|caret)-(?:red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose|slate|gray|zinc|stone)-\d{2,3}\b/;
    expect(files.filter((f) => palette.test(read(f)))).toEqual([]);
  });

  it("uses none of the retired colour tokens (success, warning, info)", () => {
    const retired =
      /\b(?:bg|text|border|ring|outline|fill|stroke|from|via|to|decoration)-(?:success|warning|info)\b/;
    expect(files.filter((f) => retired.test(read(f)))).toEqual([]);
  });

  it("keeps colour literals inside the two token blocks of index.css", () => {
    const rest = css.replace(tokenBlock(":root"), "").replace(tokenBlock(".dark"), "");
    expect(rest.match(HEX) ?? []).toEqual([]);
    expect(rest).not.toMatch(/\b(?:rgba?|hsla?|hwb|lab|lch|oklab|oklch)\(\s*\d/);
  });

  it("keeps the static assets grey", () => {
    const assets = [here("../../index.html"), here("../../public/favicon.svg")];
    const hits = assets.flatMap((path) =>
      (read(path).match(HEX) ?? [])
        .filter((hex) => !isGreyHex(hex))
        .map((hex) => `${path}: ${hex}`),
    );
    expect(hits).toEqual([]);
  });
});
