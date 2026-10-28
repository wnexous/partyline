/*
 * Layer checker (WA-05): enforces the Feature-Sliced Design import rules of web/src, see
 * web/AGENTS.md and .specs/features/web-architecture/design.md §2.
 *
 *   bun run check:layers                    fail on new violations and on stale baseline lines
 *   bun run check:layers --prune            drop stale baseline lines (the baseline only shrinks)
 *   bun run check:layers --write-baseline   write every current violation (T1 only; never to hide new ones)
 *
 * Each violation prints as `file:line rule message`. The baseline (scripts/layers-baseline.txt)
 * holds one `file -> target rule` per line for the accepted legacy violations.
 */
import { existsSync, readdirSync, readFileSync, statSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

export type Layer = "app" | "pages" | "widgets" | "features" | "entities" | "shared";

/** Top to bottom; a module may import only strictly lower layers (a larger rank). */
const RANK: Record<Layer, number> = {
  app: 0,
  pages: 1,
  widgets: 2,
  features: 3,
  entities: 4,
  shared: 5,
};
const SLICED: ReadonlySet<Layer> = new Set(["pages", "widgets", "features", "entities"]);
const FIRST_SEGMENT_LAYERS = new Set<string>([
  "pages",
  "widgets",
  "features",
  "entities",
  "shared",
]);
const LEGACY_DIRS = new Set(["components", "hooks", "lib"]);
const SKIPPED = new Set(["routeTree.gen", "lib/api/types.gen"]);

export interface Place {
  /** `legacy` is components/, hooks/, lib/; `test` is src/test; `other` is anything unclassified. */
  layer: Layer | "legacy" | "test" | "other";
  /** The slice name, for the sliced layers. */
  slice?: string;
  /** The path inside the slice (or inside `shared`), without extension. */
  rest: string[];
}

export interface Violation {
  file: string;
  line: number;
  rule: string;
  message: string;
  /** The resolved import target, relative to src and without extension. */
  target: string;
}

/** Path relative to `src` without extension -> where it lives. */
export function classify(rel: string): Place {
  const parts = rel.split("/").filter(Boolean);
  const [first = "", ...tail] = parts;
  if (first === "test") return { layer: "test", rest: tail };
  if (first === "routes" || first === "app") return { layer: "app", rest: tail };
  if (first === "shared") return { layer: "shared", rest: tail };
  if (FIRST_SEGMENT_LAYERS.has(first)) {
    const layer = first as Layer;
    const [slice, ...rest] = tail;
    return slice === undefined ? { layer, rest: [] } : { layer, slice, rest };
  }
  // main.tsx and router.tsx wire the app together.
  if (parts.length === 1 && (first === "main" || first === "router")) {
    return { layer: "app", rest: parts };
  }
  if (LEGACY_DIRS.has(first)) return { layer: "legacy", rest: parts };
  return { layer: "other", rest: parts };
}

const stripExt = (p: string) => p.replace(/\.(tsx?|jsx?|mjs|css)$/, "");

/** Resolves an import specifier of a file under src to a path relative to src, or null for packages. */
export function resolveSpecifier(fromRel: string, specifier: string): string | null {
  if (specifier.startsWith("@/")) return stripExt(path.posix.normalize(specifier.slice(2)));
  if (specifier.startsWith(".")) {
    const joined = path.posix.normalize(path.posix.join(path.posix.dirname(fromRel), specifier));
    return joined.startsWith("..") ? null : stripExt(joined);
  }
  return null;
}

export interface Import {
  specifier: string;
  line: number;
}

/** Static, re-export and dynamic import specifiers with their line; comment lines are skipped. */
export function extractImports(source: string): Import[] {
  const lineOf = (index: number) => source.slice(0, index).split("\n").length;
  const isComment = (index: number) => {
    const start = source.lastIndexOf("\n", index - 1) + 1;
    return /^\s*(\/\/|\*|\/\*)/.test(source.slice(start, index + 1));
  };
  const found = new Map<number, string>();
  const patterns = [
    /^[ \t]*(?:import|export)\b[^;'"]*?\bfrom\s*["']([^"'\n]+)["']/gm,
    /^[ \t]*import\s*["']([^"'\n]+)["']/gm,
    /\bimport\(\s*["']([^"'\n]+)["']\s*\)/g,
  ];
  for (const pattern of patterns) {
    for (const match of source.matchAll(pattern)) {
      const index = match.index ?? 0;
      const spec = match[1];
      if (spec === undefined || isComment(index)) continue;
      // The line of the specifier itself (a multi-line import names it on its last line).
      const at = index + match[0].lastIndexOf(spec);
      found.set(at, spec);
    }
  }
  return [...found.entries()]
    .sort(([a], [b]) => a - b)
    .map(([at, specifier]) => ({ specifier, line: lineOf(at) }));
}

const isTestFile = (rel: string) => /\.(test|spec)\.tsx?$/.test(rel);

type Rule = { rule: string; message: string };

function sharedUiSub(place: Place): "primitives" | "atoms" | "molecules" | null {
  if (place.layer !== "shared" || place.rest[0] !== "ui") return null;
  const sub = place.rest[1];
  return sub === "primitives" || sub === "atoms" || sub === "molecules" ? sub : null;
}

const isShared = (place: Place, ...segments: string[]) =>
  place.layer === "shared" && segments.every((s, i) => place.rest[i] === s);

/** The rules an import breaks; `fromRel` and `toRel` are relative to src, without extension. */
export function checkImport(fromRel: string, toRel: string, isTest: boolean): Rule[] {
  const from = classify(fromRel);
  const to = classify(toRel);
  const broken: Rule[] = [];
  if (from.layer === "test" || to.layer === "test") return broken;
  if (SKIPPED.has(toRel)) return broken;
  const name = (p: Place) => (p.slice ? `${p.layer}/${p.slice}` : p.layer);

  const fromRank = from.layer in RANK ? RANK[from.layer as Layer] : null;
  const toRank = to.layer in RANK ? RANK[to.layer as Layer] : null;
  const sameSlice = from.layer === to.layer && from.slice === to.slice;

  // Layer order. Legacy is below nothing and above nothing: anything may import it.
  if (fromRank !== null && toRank !== null && toRank < fromRank) {
    broken.push({
      rule: "layer-order",
      message: `${name(from)} must not import the higher layer ${name(to)}`,
    });
  }

  // Slices of one layer do not see each other; an entity may use another's @x cross-API.
  if (
    !isTest &&
    from.layer === to.layer &&
    SLICED.has(from.layer as Layer) &&
    from.slice !== undefined &&
    to.slice !== undefined &&
    from.slice !== to.slice
  ) {
    const viaX = from.layer === "entities" && to.rest[0] === "@x" && to.rest[1] === from.slice;
    if (!viaX) {
      broken.push({
        rule: "slice-isolation",
        message: `${name(from)} must not import the sibling slice ${name(to)}`,
      });
    }
  }

  // Another slice is reached through its index only (or `@x/<importer>` for entities).
  if (!isTest && toRank !== null && SLICED.has(to.layer as Layer) && to.slice && !sameSlice) {
    const [first, second] = to.rest;
    const index = to.rest.length === 0 || (to.rest.length === 1 && first === "index");
    const x = first === "@x" && to.rest.length === 2;
    if (!index && !(x && from.layer === "entities" && second === from.slice)) {
      broken.push({
        rule: first === "@x" ? "x-api" : "public-api",
        message:
          first === "@x"
            ? `${name(to)}/@x/${second ?? ""} is the cross-API for the entity ${second ?? "?"} only`
            : `import ${name(to)} through its index.ts, not a deep path`,
      });
    }
  }

  // shared/ui sub-layers.
  const sub = sharedUiSub(from);
  if (sub === "primitives") {
    const ok = sharedUiSub(to) === "primitives" || isShared(to, "lib", "utils");
    if (!ok) {
      broken.push({
        rule: "primitives-imports",
        message: "primitives import only primitives and shared/lib/utils",
      });
    }
  } else if (sub === "atoms") {
    const target = sharedUiSub(to);
    if (target === "molecules") {
      broken.push({ rule: "atoms-no-molecules", message: "atoms must not import molecules" });
    } else if (target === null && !isShared(to, "lib")) {
      broken.push({
        rule: "atoms-imports",
        message: "atoms import only primitives, atoms and shared/lib",
      });
    }
  } else if (sub === "molecules") {
    const ok = sharedUiSub(to) !== null || isShared(to, "lib") || isShared(to, "i18n");
    if (!ok) {
      broken.push({
        rule: "molecules-imports",
        message: "molecules import only atoms, primitives, molecules, shared/lib and shared/i18n",
      });
    }
  }
  return broken;
}

/** Violations of one file; `fileRel` is relative to src and includes the extension. */
export function checkFile(fileRel: string, source: string): Violation[] {
  const base = stripExt(fileRel);
  if (SKIPPED.has(base) || base.startsWith("test/")) return [];
  const isTest = isTestFile(fileRel);
  const out: Violation[] = [];
  for (const { specifier, line } of extractImports(source)) {
    const target = resolveSpecifier(fileRel, specifier);
    if (target === null) continue;
    for (const { rule, message } of checkImport(base, target, isTest)) {
      out.push({ file: `src/${fileRel}`, line, rule, message, target: `src/${target}` });
    }
  }
  return out;
}

/** The baseline key of a violation: stable across line moves. */
export const keyOf = (v: Pick<Violation, "file" | "target" | "rule">) =>
  `${v.file} -> ${v.target} ${v.rule}`;

export function parseBaseline(text: string): string[] {
  return text
    .split("\n")
    .map((l) => l.trim())
    .filter((l) => l !== "" && !l.startsWith("#"));
}

export interface Report {
  fresh: Violation[];
  stale: string[];
}

export function compare(violations: Violation[], baseline: string[]): Report {
  const accepted = new Set(baseline);
  const seen = new Set(violations.map(keyOf));
  return {
    fresh: violations.filter((v) => !accepted.has(keyOf(v))),
    stale: baseline.filter((line) => !seen.has(line)),
  };
}

function walk(dir: string): string[] {
  const files: string[] = [];
  for (const entry of readdirSync(dir)) {
    const full = path.join(dir, entry);
    if (statSync(full).isDirectory()) files.push(...walk(full));
    else if (/\.tsx?$/.test(entry)) files.push(full);
  }
  return files;
}

export function scan(srcDir: string): Violation[] {
  const out: Violation[] = [];
  for (const file of walk(srcDir).sort()) {
    const rel = path.relative(srcDir, file).split(path.sep).join("/");
    out.push(...checkFile(rel, readFileSync(file, "utf8")));
  }
  return out;
}

const BASELINE_HEADER =
  "# Accepted legacy layer violations: `file -> target rule`. This file only shrinks (WA-05).\n" +
  "# Fix a violation and run `bun run check:layers --prune`; never add a line to hide a new one.\n";

function main(argv: string[]): number {
  const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
  const baselinePath = path.join(root, "scripts", "layers-baseline.txt");
  const violations = scan(path.join(root, "src"));
  const baseline = existsSync(baselinePath)
    ? parseBaseline(readFileSync(baselinePath, "utf8"))
    : [];
  const write = (keys: string[]) =>
    writeFileSync(baselinePath, BASELINE_HEADER + keys.map((k) => `${k}\n`).join(""));

  if (argv.includes("--write-baseline")) {
    const keys = [...new Set(violations.map(keyOf))].sort();
    write(keys);
    console.log(`check:layers: baseline written with ${keys.length} line(s)`);
    return 0;
  }
  const { fresh, stale } = compare(violations, baseline);
  if (argv.includes("--prune")) {
    const kept = baseline.filter((l) => !stale.includes(l));
    write(kept);
    console.log(`check:layers: pruned ${stale.length} stale line(s)`);
  }
  const pruned = argv.includes("--prune");
  for (const v of fresh) console.error(`${v.file}:${v.line} ${v.rule} ${v.message}`);
  if (!pruned) {
    for (const line of stale) {
      console.error(`${path.relative(root, baselinePath)} stale: ${line} (fixed; run --prune)`);
    }
  }
  const failed = fresh.length > 0 || (!pruned && stale.length > 0);
  const known = violations.length - fresh.length;
  console.log(
    `check:layers: ${failed ? "FAILED" : "ok"} (${fresh.length} new, ${pruned ? 0 : stale.length} stale, ${known} baselined)`,
  );
  return failed ? 1 : 0;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  process.exit(main(process.argv.slice(2)));
}
