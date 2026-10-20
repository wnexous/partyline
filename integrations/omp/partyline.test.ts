import { describe, expect, spyOn, test } from "bun:test";
import partyline from "./partyline.ts";
import { framingCases, notificationParams } from "../testdata/framing.ts";

type Handler = (event: { server: string; method: string; params?: unknown }, ctx: unknown) => unknown;

// fakeOmp records what the extension registers and sends, like omp's ExtensionAPI.
function fakeOmp(opts: { idle?: boolean; hasUI?: boolean; throwOnSend?: boolean } = {}) {
	const handlers = new Map<string, Handler>();
	const sent: Array<{ content: string; options?: { deliverAs?: string } }> = [];
	const notices: Array<{ message: string; level?: string }> = [];
	const api = {
		on(event: string, handler: Handler) {
			handlers.set(event, handler);
		},
		sendUserMessage(content: string, options?: { deliverAs?: "steer" | "followUp" | "aside" }) {
			if (opts.throwOnSend) throw new Error("session closed");
			sent.push(options ? { content, options } : { content });
		},
	};
	const ctx = {
		isIdle: () => opts.idle ?? true,
		hasUI: opts.hasUI ?? true,
		ui: { notify: (message: string, level?: string) => notices.push({ message, level }) },
	};
	partyline(api);
	const emit = (server: string, method: string, params?: unknown) => handlers.get("mcp_notification")?.({ server, method, params }, ctx);
	return { handlers, sent, notices, emit };
}

const CHANNEL = "notifications/claude/channel";

describe("omp extension", () => {
	test("PUSH-03: subscribes to mcp_notification", () => {
		expect([...fakeOmp().handlers.keys()]).toEqual(["mcp_notification"]);
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

	test("a failed send is reported", () => {
		const omp = fakeOmp({ throwOnSend: true });
		omp.emit("partyline", CHANNEL, notificationParams(framingCases[0].record));
		expect(omp.notices).toEqual([{ message: "partyline: could not deliver a room message: session closed", level: "error" }]);
	});
});
