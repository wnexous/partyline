import { afterEach, beforeEach, describe, expect, mock, test } from "bun:test";
import { FakeTime, fakeSpawn, importAlways, spawned } from "../testdata/fakes.ts";
import { framingCases, notices } from "../testdata/framing.ts";

mock.module("node:child_process", () => ({ spawn: fakeSpawn }));
const { default: partyline } = await import("./partyline.ts");

type Plugin = typeof partyline;

type Handler = (event: unknown, ctx: unknown) => unknown;
type Sent = { message: { customType: string; content: string; display: boolean }; options: unknown };

function fakePi(opts: { hasUI?: boolean; throwOnSend?: boolean; sessionId?: unknown } = {}, plugin: Plugin = partyline) {
	const handlers = new Map<string, Handler>();
	const sent: Sent[] = [];
	const notices: Array<{ message: string; level?: string }> = [];
	const api = {
		on(event: string, handler: Handler) {
			handlers.set(event, handler);
		},
		sendMessage(message: Sent["message"], options?: unknown) {
			if (opts.throwOnSend) throw new Error("session closed");
			sent.push({ message, options });
		},
	};
	const ctx = {
		cwd: "/work/proj",
		hasUI: opts.hasUI ?? true,
		ui: { notify: (message: string, level?: string) => notices.push({ message, level }) },
		sessionManager: "sessionId" in opts ? { getSessionId: () => opts.sessionId as string } : undefined,
	};
	plugin(api);
	return {
		handlers,
		sent,
		notices,
		start: () => handlers.get("session_start")?.({ type: "session_start" }, ctx),
		shutdown: () => handlers.get("session_shutdown")?.({ type: "session_shutdown" }, ctx),
	};
}

let time: FakeTime;
let launched: string | undefined;
let liveVar: string | undefined;
beforeEach(() => {
	spawned.length = 0;
	liveVar = process.env.PARTYLINE_LIVE;
	delete process.env.PARTYLINE_LIVE;
	launched = process.env.PARTYLINE_HARNESS;
	process.env.PARTYLINE_HARNESS = "pi"; // the launcher's environment
	time = new FakeTime();
});
afterEach(() => {
	if (liveVar === undefined) delete process.env.PARTYLINE_LIVE;
	else process.env.PARTYLINE_LIVE = liveVar;
	if (launched === undefined) delete process.env.PARTYLINE_HARNESS;
	else process.env.PARTYLINE_HARNESS = launched;
	time.restore();
});

const expected = (content: string): Sent => ({
	message: { customType: "partyline", content, display: true },
	options: { triggerTurn: true, deliverAs: "followUp" },
});

describe("pi extension", () => {
	test("launch-only: a plain pi session runs no listen and shows nothing", () => {
		delete process.env.PARTYLINE_HARNESS;
		const pi = fakePi();
		pi.start();
		pi.shutdown();
		expect(spawned).toEqual([]);
		expect(pi.sent).toEqual([]);
		expect(pi.notices).toEqual([]);
	});

	test("launch-only: the launcher's PARTYLINE_HARNESS starts listen", () => {
		const pi = fakePi();
		pi.start();
		expect(spawned.length).toBe(1);
		expect(spawned[0].options.env?.PARTYLINE_HARNESS).toBe("pi");
	});

	test("setup --always: the installed extension listens without the launcher", async () => {
		delete process.env.PARTYLINE_HARNESS;
		const always = (await importAlways("pi/partyline.ts")) as { default: Plugin };
		const pi = fakePi({}, always.default);
		pi.start();
		expect(spawned.length).toBe(1);
		expect(spawned[0].args).toEqual(["listen"]);
		expect(spawned[0].options.env?.PARTYLINE_HARNESS).toBe("pi");
	});

	test("PUSH-04: session_start runs partyline listen in the session directory, once", () => {
		const pi = fakePi();
		expect(spawned).toEqual([]);
		pi.start();
		pi.start();
		expect(spawned.length).toBe(1);
		expect(spawned[0].command).toBe("partyline");
		expect(spawned[0].args).toEqual(["listen"]);
		expect(spawned[0].options.cwd).toBe("/work/proj");
		expect(spawned[0].options.env?.PARTYLINE_HARNESS).toBe("pi");
		expect(spawned[0].options.env?.PARTYLINE_SESSION).toBeUndefined();
		expect(spawned[0].options.env?.PATH).toBe(process.env.PATH);
	});

	test("SESS-50: listen gets pi's session id as PARTYLINE_HARNESS_SESSION", () => {
		fakePi({ sessionId: "0199-abc_DEF.1:2" }).start();
		expect(spawned[0].options.env?.PARTYLINE_HARNESS_SESSION).toBe("0199-abc_DEF.1:2");
	});

	test("SESS-50: a restarted listen keeps the session id", () => {
		const pi = fakePi({ sessionId: "pi-session-1" });
		pi.start();
		spawned[0].exit(1);
		time.fire();
		expect(spawned.length).toBe(2);
		expect(spawned[1].options.env?.PARTYLINE_HARNESS_SESSION).toBe("pi-session-1");
	});

	test("SESS-50: no id, an unusable id or a pi without the API means no variable, never an inherited one", () => {
		const inherited = process.env.PARTYLINE_HARNESS_SESSION;
		process.env.PARTYLINE_HARNESS_SESSION = "another-session";
		try {
			for (const opts of [
				{},
				{ sessionId: undefined },
				{ sessionId: "" },
				{ sessionId: "has space" },
				{ sessionId: "x".repeat(129) },
				{ sessionId: 42 },
			]) {
				spawned.length = 0;
				fakePi(opts).start();
				expect(spawned.length).toBe(1);
				expect(spawned[0].options.env).not.toHaveProperty("PARTYLINE_HARNESS_SESSION");
			}
		} finally {
			if (inherited === undefined) delete process.env.PARTYLINE_HARNESS_SESSION;
			else process.env.PARTYLINE_HARNESS_SESSION = inherited;
		}
	});

	test("SESS-45: listen and the tools get one live id, tied to this process", () => {
		const pi = fakePi();
		pi.start();
		const live = spawned[0].options.env?.PARTYLINE_LIVE ?? "";
		expect(live).toMatch(new RegExp(`^${process.pid}:[A-Za-z0-9_-]{22}$`));
		expect(process.env.PARTYLINE_LIVE).toBe(live); // the MCP servers started later inherit it
	});

	test("SESS-45: the launcher's live id is kept when its pid is this process's", () => {
		const launcher = `${process.pid}:LLLLLLLLLLLLLLLLLLLLLL`;
		process.env.PARTYLINE_LIVE = launcher;
		fakePi().start();
		expect(spawned[0].options.env?.PARTYLINE_LIVE).toBe(launcher);
	});

	test("SESS-45: an id inherited from another harness process is never shared", () => {
		process.env.PARTYLINE_LIVE = `${process.pid + 1}:LLLLLLLLLLLLLLLLLLLLLL`;
		fakePi().start();
		const live = spawned[0].options.env?.PARTYLINE_LIVE ?? "";
		expect(live).toMatch(new RegExp(`^${process.pid}:[A-Za-z0-9_-]{22}$`));
		expect(live).not.toContain("LLLLLLLLLLLLLLLLLLLLLL");
	});

	test("SESS-45: a restarted listen is the same session: same live id", () => {
		const pi = fakePi();
		pi.start();
		spawned[0].exit(1);
		time.fire();
		expect(spawned.length).toBe(2);
		expect(spawned[1].options.env?.PARTYLINE_LIVE).toBe(spawned[0].options.env?.PARTYLINE_LIVE);
	});

	test("SESS-45: a plain pi session mints no id", () => {
		delete process.env.PARTYLINE_HARNESS;
		fakePi().start();
		expect(process.env.PARTYLINE_LIVE).toBeUndefined();
	});

	test("PUSH-04: each line becomes one sendMessage that triggers a turn or follows up", () => {
		const pi = fakePi();
		pi.start();
		const [a, b] = [framingCases[0], framingCases[1]];
		const lineA = JSON.stringify(a.record);
		spawned[0].out(lineA.slice(0, 10));
		expect(pi.sent).toEqual([]);
		spawned[0].out(`${lineA.slice(10)}\n${JSON.stringify(b.record)}\n`);
		expect(pi.sent).toEqual([expected(a.text), expected(b.text)]);
	});

	for (const c of framingCases) {
		test(`PUSH-06: framing - ${c.name}`, () => {
			const pi = fakePi();
			pi.start();
			spawned[0].out(`${JSON.stringify(c.record)}\n`);
			expect(pi.sent).toEqual([expected(c.text)]);
		});
	}

	test("stderr lines and lines that are not JSON are logged", () => {
		const pi = fakePi();
		pi.start();
		spawned[0].err("partyline: server unreachable\n");
		spawned[0].out("not json\n");
		expect(pi.sent).toEqual([]);
		expect(pi.notices).toEqual([
			{ message: "partyline: server unreachable", level: "warning" },
			{ message: "partyline: ignored a line from partyline listen that is not JSON", level: "warning" },
		]);
	});

	test("without a UI the log goes to stderr", () => {
		const errors: unknown[] = [];
		const saved = console.error;
		console.error = (...args: unknown[]) => errors.push(args.join(" "));
		try {
			const pi = fakePi({ hasUI: false });
			pi.start();
			spawned[0].err("partyline: server unreachable\n");
			expect(errors).toEqual(["partyline: server unreachable"]);
			expect(pi.notices).toEqual([]);
		} finally {
			console.error = saved;
		}
	});

	test("a failed delivery is logged", () => {
		const pi = fakePi({ throwOnSend: true });
		pi.start();
		spawned[0].out(`${JSON.stringify(framingCases[0].record)}\n`);
		expect(pi.notices).toEqual([{ message: "partyline: could not deliver a room message: session closed", level: "warning" }]);
	});

	test("an exited listen restarts with backoff: 1s, 2s, 4s … 30s at most", () => {
		const pi = fakePi();
		pi.start();
		const seen: number[] = [];
		for (let i = 0; i < 7; i++) {
			spawned[spawned.length - 1].exit(1);
			seen.push(...time.delays());
			time.fire();
		}
		expect(seen).toEqual([1000, 2000, 4000, 8000, 16000, 30000, 30000]);
		expect(spawned.length).toBe(8);
		expect(pi.notices[0]).toEqual({ message: "partyline: listen exited (code 1); restarting in 1s", level: "warning" });
	});

	test("the backoff starts over after a listen ran for a minute", () => {
		const pi = fakePi();
		pi.start();
		spawned[0].exit(1);
		time.fire();
		spawned[1].exit(1);
		expect(time.delays()).toEqual([2000]);
		time.fire();
		time.now += 60_000;
		spawned[2].exit(1);
		expect(time.delays()).toEqual([1000]);
	});

	test("a listen that cannot start is logged and retried", () => {
		const pi = fakePi();
		pi.start();
		spawned[0].emit("error", new Error("spawn partyline ENOENT"));
		spawned[0].exit(null);
		expect(pi.notices.map((n) => n.message)).toEqual([
			"partyline: cannot run partyline listen: spawn partyline ENOENT",
			"partyline: listen exited (code null); restarting in 1s",
		]);
		expect(time.delays()).toEqual([1000]);
	});

	test("exit code 2 (not configured) is not retried", () => {
		const pi = fakePi();
		pi.start();
		spawned[0].exit(2);
		expect(time.delays()).toEqual([]);
		expect(pi.notices.map((n) => n.message)).toEqual([
			"partyline: listen is not configured; run partyline setup, then restart the session",
		]);
	});

	test("SES-05: exit code 3 (another session holds the AI) is not restarted, other codes are", () => {
		const pi = fakePi();
		pi.start();
		spawned[0].exit(3);
		expect(time.delays()).toEqual([]);
		expect(spawned.length).toBe(1);
		expect(pi.notices).toEqual([]);

		for (const code of [1, 4, 127, null]) {
			const other = fakePi();
			other.start();
			spawned[spawned.length - 1].exit(code);
			expect(time.delays()).toEqual([1000]);
			time.fire();
		}
	});

	for (const c of notices) {
		test(`SES-04/SES-05: the ${c.reason} notice is shown to the person, not sent to the model`, () => {
			const pi = fakePi();
			pi.start();
			spawned[0].out(`${JSON.stringify(c.record)}\n`);
			expect(pi.sent).toEqual([]);
			expect(pi.notices).toEqual([{ message: c.record.content, level: "warning" }]);
			spawned[0].exit(3);
			expect(time.delays()).toEqual([]);
			expect(pi.notices.length).toBe(1);
		});
	}

	test("session_shutdown stops listen and its restarts", () => {
		const pi = fakePi();
		pi.start();
		spawned[0].exit(1);
		expect(time.delays()).toEqual([1000]);
		pi.shutdown();
		expect(time.delays()).toEqual([]);
		pi.start();
		expect(spawned.length).toBe(2);
		pi.shutdown();
		expect(spawned[1].killed).toBe(true);
		spawned[1].exit(null);
		expect(time.delays()).toEqual([]);
		expect(spawned.length).toBe(2);
	});
});
