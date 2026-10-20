// Fakes shared by the adapter tests: a spawn that records `partyline listen` processes and
// manual timers and clock, so restarts are tested without waiting.
import { EventEmitter } from "node:events";

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
