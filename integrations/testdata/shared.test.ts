import { expect, test } from "bun:test";
import { readFileSync } from "node:fs";

// Each adapter is one self-contained file (the harnesses load single files), so the framing
// and the listen supervision are copied into each; they must not drift apart.
function block(file: string, start: string, end: string): string {
	const src = readFileSync(new URL(`../${file}`, import.meta.url), "utf8");
	const i = src.indexOf(start);
	const j = src.indexOf(end);
	expect(i).toBeGreaterThanOrEqual(0);
	expect(j).toBeGreaterThan(i);
	return src.slice(i, j);
}

test("PUSH-06: every adapter has the same framing code", () => {
	const [start, end] = ["// --- framing", "// --- end framing ---"];
	const omp = block("omp/partyline.ts", start, end);
	expect(block("pi/partyline.ts", start, end)).toBe(omp);
	expect(block("opencode/partyline.ts", start, end)).toBe(omp);
});

test("pi and OpenCode supervise listen with the same code", () => {
	const [start, end] = ["// --- listen supervision", "// --- end listen supervision ---"];
	expect(block("opencode/partyline.ts", start, end)).toBe(block("pi/partyline.ts", start, end));
});
