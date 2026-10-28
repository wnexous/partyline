// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  checkFile,
  classify,
  compare,
  extractImports,
  keyOf,
  parseBaseline,
  resolveSpecifier,
} from "./check-layers";

const rules = (file: string, source: string) => checkFile(file, source).map((v) => v.rule);

describe("classify", () => {
  it("maps folders to layers; routes are the app layer; the deleted legacy folders map to none", () => {
    expect(classify("routes/_app/rooms/$roomId").layer).toBe("app");
    expect(classify("main").layer).toBe("app");
    expect(classify("components/composer").layer).toBe("other");
    expect(classify("hooks/use-mobile").layer).toBe("other");
    expect(classify("lib/api/queries").layer).toBe("other");
    expect(classify("entities/intelligence/ui/intelligence-row")).toEqual({
      layer: "entities",
      slice: "intelligence",
      rest: ["ui", "intelligence-row"],
    });
    expect(classify("shared/ui/atoms/status-dot").rest).toEqual(["ui", "atoms", "status-dot"]);
  });
});

describe("extractImports", () => {
  it("finds static, type, re-export, side-effect and dynamic imports with their lines", () => {
    const source = [
      'import { a } from "@/shared/lib/utils";',
      "import type {",
      "  B,",
      '} from "@/entities/x";',
      'export * from "./y";',
      'import "./side.css";',
      "const m = () => import(",
      '  "@/features/z"',
      ");",
      'const lazy = import("@/widgets/w");',
      '// import { c } from "@/pages/commented";',
      ' * import { d } from "@/pages/doc";',
      'const text = "import x from y";',
    ].join("\n");
    expect(extractImports(source)).toEqual([
      { specifier: "@/shared/lib/utils", line: 1 },
      { specifier: "@/entities/x", line: 4 },
      { specifier: "./y", line: 5 },
      { specifier: "./side.css", line: 6 },
      { specifier: "@/features/z", line: 8 },
      { specifier: "@/widgets/w", line: 10 },
    ]);
  });
});

describe("resolveSpecifier", () => {
  it("resolves the alias and relative paths, and ignores packages", () => {
    expect(resolveSpecifier("a/b/c.ts", "@/shared/ui/x.tsx")).toBe("shared/ui/x");
    expect(resolveSpecifier("a/b/c.ts", "../d/e")).toBe("a/d/e");
    expect(resolveSpecifier("a/b/c.ts", "./f")).toBe("a/b/f");
    expect(resolveSpecifier("a/b/c.ts", "react")).toBeNull();
    expect(resolveSpecifier("a/c.ts", "../../outside")).toBeNull();
  });
});

describe("layer order", () => {
  it("allows importing a lower layer and fails on a higher one", () => {
    expect(rules("widgets/w/ui/w.tsx", 'import { a } from "@/entities/e";')).toEqual([]);
    expect(rules("widgets/w/ui/w.tsx", 'import { a } from "@/shared/lib/time";')).toEqual([]);
    expect(rules("entities/e/ui/e.tsx", 'import { a } from "@/widgets/w";')).toEqual([
      "layer-order",
    ]);
    expect(rules("shared/lib/time.ts", 'import { a } from "@/features/f";')).toEqual([
      "layer-order",
    ]);
    expect(rules("features/f/ui/f.tsx", 'import { a } from "@/pages/p";')).toEqual(["layer-order"]);
  });

  it("lets routes and app import everything", () => {
    expect(rules("routes/_app/x.tsx", 'import { a } from "@/pages/p";')).toEqual([]);
    expect(rules("app/providers.tsx", 'import { a } from "@/widgets/w";')).toEqual([]);
  });

  it("fails on a file in, or an import of, the deleted legacy folders", () => {
    expect(rules("entities/e/ui/e.tsx", 'import { a } from "@/components/old";')).toEqual([
      "legacy-removed",
    ]);
    expect(rules("entities/e/ui/e.tsx", 'import { a } from "@/hooks/use-x";')).toEqual([
      "legacy-removed",
    ]);
    expect(rules("routes/_app/x.tsx", 'import { a } from "../../lib/y";')).toEqual([
      "legacy-removed",
    ]);
    expect(rules("components/old.tsx", 'import { a } from "@/entities/e";')).toEqual([
      "legacy-removed",
    ]);
    expect(rules("lib/api/queries.ts", "export const q = 1;")).toEqual(["legacy-removed"]);
    // The shared layer is `shared/lib`, which is not the legacy folder.
    expect(rules("shared/ui/x.tsx", 'import { a } from "@/shared/lib/utils";')).toEqual([]);
  });

  it("reports the line and file", () => {
    const [v] = checkFile("entities/e/ui/e.tsx", '\nimport { a } from "@/widgets/w";\n');
    expect(v).toMatchObject({ file: "src/entities/e/ui/e.tsx", line: 2, target: "src/widgets/w" });
  });
});

describe("slices", () => {
  it("fails on an import between slices of one layer, allows the own slice", () => {
    expect(rules("features/a/ui/a.tsx", 'import { b } from "@/features/b";')).toEqual([
      "slice-isolation",
    ]);
    expect(rules("features/a/ui/a.tsx", 'import { b } from "../model/b";')).toEqual([]);
    expect(rules("features/a/ui/a.tsx", 'import { b } from "@/features/a/model/b";')).toEqual([]);
  });

  it("requires the index when another layer imports a slice", () => {
    expect(rules("pages/p/ui/p.tsx", 'import { x } from "@/features/send-message";')).toEqual([]);
    expect(rules("pages/p/ui/p.tsx", 'import { x } from "@/features/send-message/index";')).toEqual(
      [],
    );
    expect(
      rules("pages/p/ui/p.tsx", 'import { x } from "@/features/send-message/model/draft-store";'),
    ).toEqual(["public-api"]);
    // The app layer is held to it too.
    expect(rules("app/c.tsx", 'import { x } from "@/entities/e/ui/row";')).toEqual(["public-api"]);
    // Shared has no slices: deep paths are fine.
    expect(
      rules("features/f/ui/f.tsx", 'import { x } from "@/shared/ui/molecules/confirm-dialog";'),
    ).toEqual([]);
  });

  it("lets an entity use the @x cross-API made for it, and only that one", () => {
    expect(rules("entities/b/model/b.ts", 'import { T } from "@/entities/a/@x/b";')).toEqual([]);
    expect(rules("entities/c/model/c.ts", 'import { T } from "@/entities/a/@x/b";')).toEqual([
      "slice-isolation",
      "x-api",
    ]);
    expect(rules("entities/b/model/b.ts", 'import { T } from "@/entities/a";')).toEqual([
      "slice-isolation",
    ]);
    expect(rules("features/f/ui/f.tsx", 'import { T } from "@/entities/a/@x/b";')).toEqual([
      "x-api",
    ]);
  });

  it("exempts tests and src/test from the slice rules but not from the layer order", () => {
    expect(rules("features/a/ui/a.test.tsx", 'import { b } from "@/features/b/model/b";')).toEqual(
      [],
    );
    expect(rules("pages/p/ui/p.test.tsx", 'import { b } from "@/entities/e/ui/row";')).toEqual([]);
    expect(rules("entities/e/ui/e.test.tsx", 'import { b } from "@/widgets/w";')).toEqual([
      "layer-order",
    ]);
    expect(rules("test/render.tsx", 'import { b } from "@/features/b/model/b";')).toEqual([]);
    expect(rules("entities/e/ui/e.test.tsx", 'import { m } from "@/test/render";')).toEqual([]);
  });

  it("skips generated files", () => {
    expect(rules("routeTree.gen.ts", 'import { r } from "./routes/_app/x";')).toEqual([]);
    expect(rules("entities/e/ui/e.tsx", 'import { t } from "@/shared/api/types.gen";')).toEqual([]);
    expect(rules("shared/api/types.gen.ts", 'import { x } from "@/widgets/w";')).toEqual([]);
  });
});

describe("dialogs and sheets come from the molecules", () => {
  it("fails on the dialog or sheet primitive outside shared/ui", () => {
    expect(
      rules("features/f/ui/f.tsx", 'import { D } from "@/shared/ui/primitives/dialog";'),
    ).toEqual(["primitive-dialog"]);
    expect(rules("pages/p/ui/p.tsx", 'import { S } from "@/shared/ui/primitives/sheet";')).toEqual([
      "primitive-dialog",
    ]);
    expect(rules("routes/_app/x.tsx", 'import { S } from "@/shared/ui/primitives/sheet";')).toEqual(
      ["primitive-dialog"],
    );
  });

  it("allows the molecules, the other primitives, and the primitives inside shared/ui", () => {
    expect(
      rules("features/f/ui/f.tsx", 'import { D } from "@/shared/ui/molecules/dialog";'),
    ).toEqual([]);
    expect(rules("pages/p/ui/p.tsx", 'import { S } from "@/shared/ui/molecules/sheet";')).toEqual(
      [],
    );
    expect(
      rules("features/f/ui/f.tsx", 'import { A } from "@/shared/ui/primitives/alert-dialog";'),
    ).toEqual([]);
    expect(
      rules("shared/ui/molecules/dialog.tsx", 'import { D } from "@/shared/ui/primitives/dialog";'),
    ).toEqual([]);
    expect(rules("shared/ui/primitives/command.tsx", 'import { D } from "./dialog";')).toEqual([]);
  });

  it("is not exempted for test files outside src/test", () => {
    expect(
      rules("features/f/ui/f.test.tsx", 'import { D } from "@/shared/ui/primitives/dialog";'),
    ).toEqual(["primitive-dialog"]);
  });
});

describe("shared/ui sub-layers", () => {
  it("keeps primitives free of app code", () => {
    const from = "shared/ui/primitives/dialog.tsx";
    expect(rules(from, 'import { cn } from "@/shared/lib/utils";')).toEqual([]);
    expect(rules(from, 'import { Button } from "./button";')).toEqual([]);
    expect(rules(from, 'import { useT } from "@/shared/i18n";')).toEqual(["primitives-imports"]);
    expect(rules(from, 'import { useIsMobile } from "@/shared/lib/device";')).toEqual([
      "primitives-imports",
    ]);
    expect(rules(from, 'import { x } from "@/shared/ui/atoms/status-dot";')).toEqual([
      "primitives-imports",
    ]);
  });

  it("lets atoms use primitives and shared/lib but not molecules", () => {
    const from = "shared/ui/atoms/status-dot.tsx";
    expect(rules(from, 'import { cn } from "@/shared/lib/utils";')).toEqual([]);
    expect(rules(from, 'import { b } from "@/shared/ui/primitives/badge";')).toEqual([]);
    expect(rules(from, 'import { m } from "@/shared/ui/molecules/empty-state";')).toEqual([
      "atoms-no-molecules",
    ]);
    expect(rules(from, 'import { t } from "@/shared/i18n";')).toEqual(["atoms-imports"]);
  });

  it("lets molecules use atoms, primitives, lib and i18n", () => {
    const from = "shared/ui/molecules/confirm-dialog.tsx";
    expect(rules(from, 'import { t } from "@/shared/i18n";')).toEqual([]);
    expect(rules(from, 'import { a } from "@/shared/ui/atoms/status-dot";')).toEqual([]);
    expect(rules(from, 'import { q } from "@/shared/api/client";')).toEqual(["molecules-imports"]);
    expect(rules(from, 'import { e } from "@/entities/e";')).toEqual([
      "layer-order",
      "molecules-imports",
    ]);
  });
});

describe("baseline", () => {
  const violation = checkFile("entities/e/ui/e.tsx", 'import { a } from "@/widgets/w";\n');

  it("parses lines and skips comments and blanks", () => {
    expect(parseBaseline("# note\n\nsrc/a.ts -> src/b layer-order\n")).toEqual([
      "src/a.ts -> src/b layer-order",
    ]);
  });

  it("accepts a baselined violation", () => {
    const report = compare(violation, [keyOf(violation[0] as (typeof violation)[number])]);
    expect(report).toEqual({ fresh: [], stale: [] });
  });

  it("fails on a violation that is not in the baseline", () => {
    expect(compare(violation, []).fresh).toHaveLength(1);
  });

  it("fails on a baseline line that no longer occurs (stale)", () => {
    const report = compare([], ["src/old.ts -> src/gone layer-order"]);
    expect(report.stale).toEqual(["src/old.ts -> src/gone layer-order"]);
  });

  it("keys a violation without its line, so moving it does not make it new", () => {
    const moved = checkFile("entities/e/ui/e.tsx", '\n\nimport { a } from "@/widgets/w";\n');
    expect(compare(moved, [keyOf(violation[0] as (typeof violation)[number])]).fresh).toEqual([]);
  });
});
