// Fakes shared by the adapter tests: a spawn that records `partyline listen` processes and
// manual timers and clock, so restarts are tested without waiting.
import { EventEmitter } from "node:events";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

export class FakeChild extends EventEmitter {
	stdout = new EventEmitter();
	stderr = new EventEmitter();
	stdin = { end() {} };
	killed = false;
	constructor(
		public command: string,
		public args: string[],
		public options: { cwd?: string; env?: Record<string, string | undefined> },
	) {
		super();
	}
	kill() {
		this.killed = true;
		return true;
	}
	out(text: string) {
		this.stdout.emit("data", Buffer.from(text));
	}
	err(text: string) {
		this.stderr.emit("data", Buffer.from(text));
	}
	exit(code: number | null) {
		this.emit("exit", code, null);
	}
}

export const spawned: FakeChild[] = [];

// importAlways imports a copy of an adapter with PARTYLINE_ALWAYS = true, the way
// `partyline setup --always` installs it. The adapter is one self-contained file, so a copy
// outside integrations keeps the test honest: it exercises the installed constant, not a flag.
export async function importAlways(file: string): Promise<Record<string, unknown>> {
	const source = readFileSync(new URL(`../${file}`, import.meta.url), "utf8");
	const marker = "const PARTYLINE_ALWAYS = false;";
	if (!source.includes(marker)) throw new Error(`${file} does not carry ${marker}`);
	const dir = mkdtempSync(join(tmpdir(), "partyline-always-"));
	const path = join(dir, "partyline.ts");
	writeFileSync(path, source.replace(marker, "const PARTYLINE_ALWAYS = true;"));
	try {
		return await import(pathToFileURL(path).href);
	} finally {
		rmSync(dir, { recursive: true, force: true });
	}
}

export function fakeSpawn(command: string, args: string[], options: FakeChild["options"]) {
	const c = new FakeChild(command, args, options);
	spawned.push(c);
	return c;
}

type Pending = { id: number; fn: () => void; ms: number };

// FakeTime replaces setTimeout, clearTimeout and Date.now until restore().
export class FakeTime {
	now = 1_000_000;
	pending: Pending[] = [];
	private next = 1;
	private saved = { setTimeout: globalThis.setTimeout, clearTimeout: globalThis.clearTimeout, now: Date.now };
	constructor() {
		globalThis.setTimeout = ((fn: () => void, ms: number) => {
			const id = this.next++;
			this.pending.push({ id, fn, ms });
			return id;
		}) as unknown as typeof setTimeout;
		globalThis.clearTimeout = ((id: number) => {
			this.pending = this.pending.filter((p) => p.id !== id);
		}) as unknown as typeof clearTimeout;
		Date.now = () => this.now;
	}
	delays(): number[] {
		return this.pending.map((p) => p.ms);
	}
	// fire runs the pending timers and advances the clock by their delay.
	fire() {
		const due = this.pending;
		this.pending = [];
		for (const p of due) {
			this.now += p.ms;
			p.fn();
		}
	}
	restore() {
		globalThis.setTimeout = this.saved.setTimeout;
		globalThis.clearTimeout = this.saved.clearTimeout;
		Date.now = this.saved.now;
	}
}
