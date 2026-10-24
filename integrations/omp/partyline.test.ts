import { afterEach, beforeEach, describe, expect, spyOn, test } from "bun:test";
import partyline from "./partyline.ts";
import { FakeTime, importAlways } from "../testdata/fakes.ts";
import { framingCases, notices, notificationParams } from "../testdata/framing.ts";

type Plugin = typeof partyline;

type Handler = (event: unknown, ctx: unknown) => unknown;
// What the fake host's sendUserMessage does on a call: deliver, throw, or return a rejected
// promise (omp 18.6.1 returns nothing; other modes may hand back the session's promise).
type Outcome = "ok" | { throw: string } | { reject: string };

// fakeOmp records what the extension registers and sends, like omp's ExtensionAPI. outcomes
// are consumed one per sendUserMessage call; when they run out, sends succeed.
function fakeOmp(opts: { idle?: boolean; hasUI?: boolean; model?: unknown; outcomes?: Outcome[] } = {}, plugin: Plugin = partyline) {
	const handlers = new Map<string, Handler>();
	const sent: Array<{ content: string; options?: { deliverAs?: string } }> = [];
	const notices: Array<{ message: string; level?: string }> = [];
	const outcomes = [...(opts.outcomes ?? [])];
	const attempts: string[] = []; // the content of every sendUserMessage call, in order
	let calls = 0;
	const api = {
		on(event: string, handler: Handler) {
			handlers.set(event, handler);
		},
		sendUserMessage(content: string, options?: { deliverAs?: "steer" | "followUp" | "aside" }): unknown {
			calls++;
			attempts.push(content);
			const o = outcomes.shift() ?? "ok";
			if (o !== "ok" && "throw" in o) throw new Error(o.throw);
			if (o !== "ok" && "reject" in o) return Promise.reject(new Error(o.reject));
			sent.push(options ? { content, options } : { content });
			return undefined;
		},
	};
	const ctx: Record<string, unknown> = {
		isIdle: () => opts.idle ?? true,
		hasUI: opts.hasUI ?? true,
		ui: { notify: (message: string, level?: string) => notices.push({ message, level }) },
	};
	if ("model" in opts) ctx.model = opts.model;
	plugin(api);
	const emit = (server: string, method: string, params?: unknown) => handlers.get("mcp_notification")?.({ server, method, params }, ctx);
	const agentEnd = () => handlers.get("agent_end")?.({ type: "agent_end", messages: [] }, ctx);
	return { handlers, sent, notices, emit, agentEnd, ctx, calls: () => calls, attempts, fail: (...o: Outcome[]) => outcomes.push(...o) };
}

const flushPromises = () => new Promise<void>((resolve) => queueMicrotask(resolve)).then(() => undefined);

const CHANNEL = "notifications/claude/channel";

let time: FakeTime;
let launched: string | undefined;
beforeEach(() => {
	launched = process.env.PARTYLINE_HARNESS;
	process.env.PARTYLINE_HARNESS = "omp"; // the launcher's environment
	time = new FakeTime();
});
afterEach(() => {
	if (launched === undefined) delete process.env.PARTYLINE_HARNESS;
	else process.env.PARTYLINE_HARNESS = launched;
	time.restore();
});

const held = (reason: string) => ({
	message: `partyline: could not deliver room messages yet (${reason}); they are held and retried`,
	level: "warning",
});

describe("omp extension", () => {
	test("launch-only: a plain omp session is inert", () => {
		delete process.env.PARTYLINE_HARNESS;
		const omp = fakeOmp();
		expect(omp.handlers.size).toBe(0);
	});

	test("launch-only: the launcher's PARTYLINE_HARNESS subscribes", () => {
		expect([...fakeOmp().handlers.keys()].sort()).toEqual(["agent_end", "mcp_notification"]);
	});

	test("setup --always: the installed extension subscribes without the launcher", async () => {
		delete process.env.PARTYLINE_HARNESS;
		const always = (await importAlways("omp/partyline.ts")) as { default: Plugin };
		const omp = fakeOmp({ idle: true }, always.default);
		expect([...omp.handlers.keys()].sort()).toEqual(["agent_end", "mcp_notification"]);
		omp.emit("partyline", CHANNEL, notificationParams(framingCases[0].record));
		expect(omp.sent).toEqual([{ content: framingCases[0].text }]);
	});

	test("PUSH-03: subscribes to mcp_notification and agent_end", () => {
		expect([...fakeOmp().handlers.keys()].sort()).toEqual(["agent_end", "mcp_notification"]);
	});

	test("PUSH-03: a channel notification while idle becomes one user message that starts a turn", () => {
		const omp = fakeOmp({ idle: true });
		omp.emit("partyline", CHANNEL, notificationParams(framingCases[0].record));
		expect(omp.sent).toEqual([{ content: framingCases[0].text }]);
	});

	test("PUSH-03: while the agent is busy the message is delivered as a follow-up", () => {
		const omp = fakeOmp({ idle: false });
		omp.emit("partyline", CHANNEL, notificationParams(framingCases[1].record));
		expect(omp.sent).toEqual([{ content: framingCases[1].text, options: { deliverAs: "followUp" } }]);
	});

	for (const c of notices) {
		test(`SES-05: the ${c.reason} notice is shown to the person, not sent to the session`, () => {
			const omp = fakeOmp({ idle: true });
			omp.emit("partyline", CHANNEL, notificationParams(c.record));
			expect(omp.attempts).toEqual([]);
			expect(omp.notices).toEqual([{ message: c.record.content, level: "warning" }]);
		});
	}

	test("PUSH-03: other servers and other methods are ignored", () => {
		const omp = fakeOmp();
		const params = notificationParams(framingCases[0].record);
		omp.emit("github", CHANNEL, params);
		omp.emit("partyline", "notifications/tools/list_changed", {});
		omp.emit("partyline", "notifications/message", params);
		expect(omp.sent).toEqual([]);
		expect(omp.notices).toEqual([]);
	});

	for (const c of framingCases) {
		test(`PUSH-06: framing - ${c.name}`, () => {
			const omp = fakeOmp();
			omp.emit("partyline", CHANNEL, notificationParams(c.record));
			expect(omp.sent.map((s) => s.content)).toEqual([c.text]);
		});
	}

	test("a malformed notification is reported, not delivered", () => {
		const omp = fakeOmp();
		omp.emit("partyline", CHANNEL, { meta: { room: "r" } });
		expect(omp.sent).toEqual([]);
		expect(omp.notices).toEqual([{ message: "partyline: ignored a malformed channel notification", level: "error" }]);
	});

	test("without a UI the problem goes to stderr", () => {
		const errors = spyOn(console, "error").mockImplementation(() => {});
		try {
			const omp = fakeOmp({ hasUI: false });
			omp.emit("partyline", CHANNEL, "not an object");
			expect(errors.mock.calls).toEqual([["partyline: ignored a malformed channel notification"]]);
			expect(omp.notices).toEqual([]);
		} finally {
			errors.mockRestore();
		}
	});

	test("PUSH-03: a failed send is held and delivered by the retry, with one notice", () => {
		const omp = fakeOmp({ outcomes: [{ throw: "No model selected" }] });
		omp.emit("partyline", CHANNEL, notificationParams(framingCases[0].record));
		expect(omp.sent).toEqual([]);
		expect(omp.notices).toEqual([held("No model selected")]);
		expect(time.delays()).toEqual([1_000]);
		time.fire();
		expect(omp.sent).toEqual([{ content: framingCases[0].text }]);
		expect(omp.notices).toEqual([held("No model selected")]);
		expect(time.delays()).toEqual([]);
	});

	test("PUSH-03: held messages keep their order and the notice is shown once per outage", () => {
		const omp = fakeOmp({ outcomes: [{ throw: "busy" }, { throw: "busy" }, { throw: "busy" }] });
		const [a, b] = [framingCases[0], framingCases[1]];
		omp.emit("partyline", CHANNEL, notificationParams(a.record));
		omp.emit("partyline", CHANNEL, notificationParams(b.record));
		expect(omp.calls()).toBe(2);
		time.fire(); // third failure
		expect(omp.sent).toEqual([]);
		time.fire();
		expect(omp.sent.map((s) => s.content)).toEqual([a.text, b.text]);
		expect(omp.notices).toEqual([held("busy")]);

		omp.fail({ throw: "closed" });
		omp.emit("partyline", CHANNEL, notificationParams(a.record));
		expect(omp.notices).toEqual([held("busy"), held("closed")]);
	});

	test("PUSH-03: retries back off from 1 s to 30 s and start again at 1 s after a delivery", () => {
		const omp = fakeOmp({ outcomes: Array.from({ length: 7 }, () => ({ throw: "no" })) });
		omp.emit("partyline", CHANNEL, notificationParams(framingCases[0].record));
		const delays: number[] = [];
		for (let i = 0; i < 7; i++) {
			delays.push(...time.delays());
			time.fire();
		}
		expect(delays).toEqual([1_000, 2_000, 4_000, 8_000, 16_000, 30_000, 30_000]);
		expect(omp.sent.map((s) => s.content)).toEqual([framingCases[0].text]);

		omp.fail({ throw: "no" });
		omp.emit("partyline", CHANNEL, notificationParams(framingCases[1].record));
		expect(time.delays()).toEqual([1_000]);
	});

	test("PUSH-03: without a model nothing is sent; agent_end with a model delivers at once", () => {
		const omp = fakeOmp({ model: undefined });
		omp.emit("partyline", CHANNEL, notificationParams(framingCases[0].record));
		expect(omp.calls()).toBe(0);
		expect(omp.notices).toEqual([held("no model selected")]);
		time.fire();
		expect(omp.calls()).toBe(0);

		omp.ctx.model = { provider: "x", id: "y" };
		omp.agentEnd();
		expect(omp.sent).toEqual([{ content: framingCases[0].text }]);
		expect(time.delays()).toEqual([]);
	});

	test("PUSH-03: a send whose promise rejects is held and retried", async () => {
		const omp = fakeOmp({ outcomes: [{ reject: "No API key found for x" }] });
		omp.emit("partyline", CHANNEL, notificationParams(framingCases[0].record));
		await flushPromises();
		expect(omp.notices).toEqual([held("No API key found for x")]);
		expect(time.delays()).toEqual([1_000]);
		time.fire();
		expect(omp.sent).toEqual([{ content: framingCases[0].text }]);
	});

	test("PUSH-03: sends whose promises reject later keep the order: one message at a time", async () => {
		const omp = fakeOmp({ outcomes: [{ reject: "busy" }, { reject: "busy" }] });
		const [a, b] = framingCases;
		omp.emit("partyline", CHANNEL, notificationParams(a.record));
		omp.emit("partyline", CHANNEL, notificationParams(b.record));
		expect(omp.attempts).toEqual([a.text]); // b waits while a's send is in flight
		await flushPromises();
		expect(omp.notices).toEqual([held("busy")]);
		expect(omp.attempts).toEqual([a.text]);

		time.fire(); // a again: rejects a second time
		expect(omp.attempts).toEqual([a.text, a.text]);
		await flushPromises();
		expect(omp.sent).toEqual([]);

		time.fire(); // a goes through, then b
		await flushPromises();
		expect(omp.attempts).toEqual([a.text, a.text, a.text, b.text]);
		expect(omp.sent).toEqual([{ content: a.text }, { content: b.text }]);
		expect(omp.notices).toEqual([held("busy")]);
	});
});
