import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

/*
 * Focus stays loud (DESIGN.md rule 7): an outline is never removed without a replacement. In
 * Tailwind 4 `outline-none` switches the outline style off for good, so a `focus-visible:outline-2`
 * written next to it keeps a width and a colour and still draws nothing: a keyboard user tabs onto
 * the link and sees no sign of it. A control that drops its outline needs a ring; one that keeps
 * the outline must not carry `outline-none`.
 */

const here = (relative: string) => fileURLToPath(new URL(relative, import.meta.url));

function sources(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) return sources(path);
    const isSource = /\.tsx?$/.test(entry.name) && !/\.(test|gen|d)\.tsx?$/.test(entry.name);
    return isSource ? [path] : [];
  });
}

describe("focus rings", () => {
  const files = sources(here("../.."));

  it("finds the sources it scans", () => {
    expect(files.length).toBeGreaterThan(100);
  });

  it("never pairs outline-none with a focus outline that cannot show, unless a ring takes over", () => {
    const offenders = files.flatMap((file) =>
      readFileSync(file, "utf8")
        .split("\n")
        .flatMap((line, index) =>
          /\boutline-none\b/.test(line) &&
          /\bfocus(?:-visible)?:outline-\d/.test(line) &&
          !/\bfocus(?:-visible)?:ring/.test(line)
            ? [`${file.replace(`${here("../..")}/`, "src/")}:${index + 1}`]
            : [],
        ),
    );
    expect(offenders).toEqual([]);
  });
});
