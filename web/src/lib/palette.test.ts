import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { THEME_COLORS } from "@/lib/theme";
import {
  contrast,
  inGamut,
  type Oklch,
  over,
  parseOklch,
  type Rgb,
  toHex,
  toSrgb,
} from "@/test/color";

/*
 * The palette (product owner's briefs, 2026-10-23): a neutral base of greys with a little colour
 * in a few details. Surfaces and ink are greys (chroma 0). The only colour is a small, declared
 * vocabulary: one restrained accent, a quiet green for presence, the muted red for errors and
 * destructive actions, and soft identity tints for avatar fallbacks. WCAG AA in light and dark,
 * focus ring included. The tokens are read straight from src/index.css and the sources are
 * scanned for colours that bypass them. DESIGN.md explains the roles.
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

/** Hue distance on the colour wheel, 0 to 180. */
const hueGap = (a: number, b: number) => {
  const gap = Math.abs(a - b) % 360;
  return gap > 180 ? 360 - gap : gap;
};

/**
 * The colour vocabulary. Every token that may have chroma is listed here with the hue it keeps
 * in both themes; every other token is a grey. A new colour is a deliberate edit of this list.
 */
interface Family {
  hue: number;
  /** The most chroma any token of the family may have: what keeps the colour restrained. */
  maxChroma: number;
  tokens: string[];
}

/** Warm tints are people's faces, cool ones are AIs' (`avatarTone`); the hue is its name's colour. */
const PEOPLE_TINTS = { apricot: 55, ochre: 90, sage: 125, rose: 355 } as const;
const AI_TINTS = { teal: 195, sky: 235, indigo: 275, violet: 305 } as const;
const TINT_HUES: Record<string, number> = { ...PEOPLE_TINTS, ...AI_TINTS };

const FAMILIES: Record<string, Family> = {
  // The accent: a dusty indigo for the primary action, focus, links, selection and the AI badge.
  accent: {
    hue: 275,
    maxChroma: 0.13,
    tokens: [
      "primary",
      "sidebar-primary",
      "link",
      "ring",
      "sidebar-ring",
      "tape",
      "tape-strong",
      "ai",
      "ai-foreground",
    ],
  },
  online: { hue: 152, maxChroma: 0.14, tokens: ["online"] },
  destructive: { hue: 26, maxChroma: 0.1, tokens: ["destructive"] },
  ...Object.fromEntries(
    Object.entries(TINT_HUES).map(([name, hue]) => [
      `tint-${name}`,
      { hue, maxChroma: 0.07, tokens: [`tint-${name}`, `tint-${name}-foreground`] },
    ]),
  ),
};

/** A tint's fill stays this close to grey, so a row of faces reads as one calm set. */
const MAX_TINT_FILL_CHROMA = 0.055;
/** The most distinct hues the stylesheet may use: the accent, green, red and the eight tints. */
const MAX_HUES = 10;
/** Two different families never sit closer than this on the wheel, or they would read as one. */
const MIN_HUE_GAP = 25;

describe("colour helpers", () => {
  it("compute the WCAG ratio and the sRGB hex the browser chrome needs", () => {
    expect(contrast([0, 0, 0], [1, 1, 1])).toBeCloseTo(21, 5);
    expect(contrast([1, 1, 1], [1, 1, 1])).toBe(1);
    expect(toHex(toSrgb(parseOklch("oklch(0.985 0 0)")))).toBe("#fafafa");
    expect(toHex(toSrgb(parseOklch("oklch(1 0 0)")))).toBe("#ffffff");
  });

  it("tell a colour inside sRGB from one the browser would have to map", () => {
    expect(inGamut(parseOklch("oklch(0.46 0.115 275)"))).toBe(true);
    expect(inGamut(parseOklch("oklch(0.7 0.3 150)"))).toBe(false);
  });
});

describe("palette tokens", () => {
  it.each(MODES)(
    "%s: surfaces and ink are greys; only the declared colour families have chroma",
    (_mode, selector) => {
      const colours = tokens(selector);
      expect(Object.keys(colours).length).toBeGreaterThan(25);
      const chromatic = Object.entries(colours)
        .filter(([, colour]) => colour.c > 0)
        .map(([name]) => name)
        .sort();
      const declared = Object.values(FAMILIES)
        .flatMap((family) => family.tokens)
        .sort();
      expect(chromatic).toEqual(declared);
    },
  );

  it.each(MODES)("%s: each colour family keeps its hue and stays restrained", (_mode, selector) => {
    const colours = tokens(selector);
    const wrong = Object.entries(FAMILIES).flatMap(([name, family]) =>
      family.tokens.flatMap((token) => {
        const colour = colours[token];
        if (!colour) return [`${name}: no --${token} token`];
        const problems: string[] = [];
        if (hueGap(colour.h, family.hue) > 8) {
          problems.push(`${token}: hue ${colour.h} is not ${family.hue}`);
        }
        if (colour.c > family.maxChroma) {
          problems.push(`${token}: chroma ${colour.c} is above ${family.maxChroma}`);
        }
        return problems;
      }),
    );
    expect(wrong).toEqual([]);
  });

  it("uses a bounded number of hues, kept apart on the wheel", () => {
    const hues = Object.entries(FAMILIES).map(([name, family]) => ({ name, hue: family.hue }));
    // The accent and the indigo tint are one hue on purpose: the AI badge and the AI faces agree.
    const distinct = [...new Set(hues.map(({ hue }) => hue))];
    expect(distinct.length).toBeLessThanOrEqual(MAX_HUES);
    const crowded = hues.flatMap((a, i) =>
      hues
        .slice(i + 1)
        .filter((b) => b.hue !== a.hue && hueGap(a.hue, b.hue) < MIN_HUE_GAP)
        .map((b) => `${a.name} (${a.hue}) and ${b.name} (${b.hue})`),
    );
    expect(crowded).toEqual([]);
  });

  it.each(MODES)(
    "%s: every colour is inside sRGB, so the contrast measured is the one shown",
    (_mode, selector) => {
      const outside = Object.entries(tokens(selector))
        .filter(([, colour]) => !inGamut(colour))
        .map(([name]) => name);
      expect(outside).toEqual([]);
    },
  );

  it.each(MODES)("%s: identity tints are soft", (mode, selector) => {
    const colours = tokens(selector);
    const wrong = Object.keys(TINT_HUES).flatMap((name) => {
      const fill = colours[`tint-${name}`];
      const ink = colours[`tint-${name}-foreground`];
      if (!fill || !ink) return [`${name}: missing`];
      // A pale fill with dark ink in light; a deep fill with pale ink in dark.
      const fillRange = mode === "light" ? [0.86, 0.94] : [0.3, 0.42];
      const problems: string[] = [];
      if (fill.l < (fillRange[0] ?? 0) || fill.l > (fillRange[1] ?? 1)) {
        problems.push(`${name}: fill lightness ${fill.l} is outside ${fillRange.join(" to ")}`);
      }
      if (fill.c > MAX_TINT_FILL_CHROMA) {
        problems.push(`${name}: fill chroma ${fill.c} is above ${MAX_TINT_FILL_CHROMA}`);
      }
      if (mode === "light" ? ink.l > 0.45 : ink.l < 0.85) {
        problems.push(
          `${name}: ink lightness ${ink.l} is too ${mode === "light" ? "pale" : "deep"}`,
        );
      }
      return problems;
    });
    expect(wrong).toEqual([]);
  });

  it("tunes the dark accent instead of inverting the light one: lighter and quieter", () => {
    const light = tokens(":root");
    const dark = tokens(".dark");
    const wrong = ["primary", "link", "ring", "tape-strong", "ai-foreground"].flatMap((name) => {
      const [on, off] = [light[name], dark[name]];
      if (!on || !off) return [`${name}: missing`];
      const problems: string[] = [];
      if (off.l <= on.l) problems.push(`${name}: dark lightness ${off.l} is not above ${on.l}`);
      // Colour looks stronger on a dark surface, so its chroma comes down.
      if (off.c >= on.c) problems.push(`${name}: dark chroma ${off.c} is not below ${on.c}`);
      return problems;
    });
    expect(wrong).toEqual([]);
  });

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
  ["ai", "ai-foreground"],
  ["destructive", "destructive-foreground"],
  ...Object.keys(TINT_HUES).map((name): [string, string] => [
    `tint-${name}`,
    `tint-${name}-foreground`,
  ]),
];

function textPairs(): Pair[] {
  const pairs: Pair[] = [];
  const add = (fg: string, ...surface: string[]) => pairs.push({ fg, surface });
  for (const surface of [...BASES, ...FILLS]) {
    add("foreground", surface);
    add("muted-foreground", surface);
    add("link", surface);
  }
  for (const [fill, ink] of INK_ON_FILL) add(ink, fill);
  for (const base of BASES) {
    for (const tint of TINTS) {
      add("foreground", base, tint);
      add("muted-foreground", base, tint);
      add("link", base, tint);
    }
    for (const alpha of [40, 50, 60, 70, 80]) add("tape-foreground", base, `tape/${alpha}`);
    add("foreground/80", base);
    add("foreground/85", base);
    // Selected text: the page ink over the accent wash that `::selection` paints.
    add("foreground", base, "primary/30");
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
  // The inverted live banner and countdown chip; ink on the mid greys.
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
    // Control borders, the focus ring and its 70 % halo, dots, checked boxes.
    for (const fg of ["input", "ring", "ring/70", "tape-strong", "muted-foreground", "online"]) {
      add(fg, base);
    }
    add("destructive", base);
    add("primary", base);
    for (const alpha of [40, 60, 70]) {
      add("tape-strong", base, `tape/${alpha}`);
      add("ring/70", base, `tape/${alpha}`);
    }
  }
  // The presence dot also sits on hover rows and pills.
  for (const fill of ["secondary", "muted", "accent", "sidebar-accent"]) add("online", fill);
  add("sidebar-ring", "sidebar");
  add("sidebar-ring/70", "sidebar");
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

  it("keeps the accent fill of a checked control in dark, where a field fill would outrank it", () => {
    // `dark:bg-input/30` beats `data-[state=checked]:bg-primary` on specificity and order, which
    // leaves the box dark with a dark check mark: the dark variant must be spelled out too.
    const hits = files.filter((f) => {
      const source = read(f);
      return (
        /(?<!dark:)data-\[state=checked\]:bg-primary/.test(source) &&
        /dark:bg-input/.test(source) &&
        !/dark:data-\[state=checked\]:bg-primary/.test(source)
      );
    });
    expect(hits).toEqual([]);
  });

  it("keeps colour literals inside the two token blocks of index.css", () => {
    const rest = css.replace(tokenBlock(":root"), "").replace(tokenBlock(".dark"), "");
    expect(rest.match(HEX) ?? []).toEqual([]);
    expect(rest).not.toMatch(/\b(?:rgba?|hsla?|hwb|lab|lch|oklab|oklch)\(\s*\d/);
  });

  it("keeps the page shell grey: only the favicon carries the accent", () => {
    const hits = [here("../../index.html")].flatMap((path) =>
      (read(path).match(HEX) ?? [])
        .filter((hex) => !isGreyHex(hex))
        .map((hex) => `${path}: ${hex}`),
    );
    expect(hits).toEqual([]);
  });

  it("paints the favicon with the light theme's accent and paper", () => {
    const light = tokens(":root");
    const svg = read(here("../../public/favicon.svg"));
    expect(svg).toContain(`fill="${toHex(toSrgb(light.primary as Oklch))}"`);
    expect(svg).toContain(`stroke="${toHex(toSrgb(light["primary-foreground"] as Oklch))}"`);
    const hexes = svg.match(HEX) ?? [];
    expect(hexes).toHaveLength(2);
  });

  it("gives avatar fallbacks exactly the declared tints, as token classes", () => {
    const avatar = read(here("../components/intelligence/intelligence-avatar.tsx"));
    const used = [...avatar.matchAll(/\bbg-tint-([a-z]+)\b/g)].map((m) => m[1]);
    expect([...new Set(used)].sort()).toEqual(Object.keys(TINT_HUES).sort());
    const inks = [...avatar.matchAll(/\btext-tint-([a-z]+)-foreground\b/g)].map((m) => m[1]);
    expect([...new Set(inks)].sort()).toEqual(Object.keys(TINT_HUES).sort());
  });
});
