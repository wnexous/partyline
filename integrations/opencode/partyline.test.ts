import { afterEach, beforeEach, describe, expect, mock, test } from "bun:test";
import { FakeTime, fakeSpawn, spawned } from "../testdata/fakes.ts";
import { framingCases } from "../testdata/framing.ts";

mock.module("node:child_process", () => ({ spawn: fakeSpawn }));
const plugin = await import("./partyline.ts");

type Prompt = { path: { id: string }; body: { parts: Array<{ type: string; text: string }> } };

function fakeClient(opts: { promptAsync?: boolean; result?: unknown; reject?: boolean } = {}) {
	const prompts: Prompt[] = [];
	const syncPrompts: Prompt[] = [];
	const logs: Array<{ service: string; level: string; message: string }> = [];
	const answer = () => (opts.reject ? Promise.reject(new Error("connection refused")) : Promise.resolve(opts.result ?? { data: {} }));
	const session: Record<string, (o: Prompt) => Promise<unknown>> = {
		prompt: (o) => {
			syncPrompts.push(o);
			return answer();
		},
	};
	if (opts.promptAsync ?? true) {
		session.promptAsync = (o) => {
			prompts.push(o);
			return answer();
		};
	}
	const client = {
		session,
		app: {
			log: (o: { body: { service: string; level: string; message: string } }) => {
				logs.push(o.body);
				return Promise.resolve({});
			},
		},
	};
	return { client, prompts, syncPrompts, logs };
}

async function load(opts: Parameters<typeof fakeClient>[0] = {}) {
	const c = fakeClient(opts);
	const hooks = await plugin.PartylinePlugin({ client: c.client as never, directory: "/work/proj" } as never);
	const emit = (type: string, properties: Record<string, unknown>) => hooks.event({ event: { type, properties } });
	return { ...c, hooks, emit };
}

const flush = () => new Promise((resolve) => queueMicrotask(() => resolve(undefined)));
const sent = (text: string, id: string): Prompt => ({ path: { id }, body: { parts: [{ type: "text", text }] } });

let time: FakeTime;
beforeEach(() => {
	spawned.length = 0;
	time = new FakeTime();
});
afterEach(() => time.restore());

describe("OpenCode plugin", () => {
	test("the plugin file exports only the plugin (OpenCode calls every export)", () => {
		expect(Object.keys(plugin)).toEqual(["PartylinePlugin"]);
		expect(typeof plugin.PartylinePlugin).toBe("function");
	});

	test("PUSH-05: it runs partyline listen in the project directory", async () => {
		await load();
		expect(spawned.length).toBe(1);
		expect(spawned[0].command).toBe("partyline");
		expect(spawned[0].args).toEqual(["listen"]);
		expect(spawned[0].options.cwd).toBe("/work/proj");
		expect(spawned[0].options.env?.PARTYLINE_HARNESS).toBe("opencode");
		expect(spawned[0].options.env?.PARTYLINE_SESSION).toBeUndefined();
	});

	test("PUSH-05: each line goes to the active session with promptAsync", async () => {
		const oc = await load();
		await oc.emit("session.status", { sessionID: "ses_1", status: { type: "busy" } });
		spawned[0].out(`${JSON.stringify(framingCases[0].record)}\n`);
		await oc.emit("session.idle", { sessionID: "ses_2" });
		spawned[0].out(`${JSON.stringify(framingCases[1].record)}\n`);
		expect(oc.prompts).toEqual([sent(framingCases[0].text, "ses_1"), sent(framingCases[1].text, "ses_2")]);
		expect(oc.syncPrompts).toEqual([]);
	});

	test("PUSH-05: without a session yet, messages wait and go to the first one in order", async () => {
		const oc = await load();
		spawned[0].out(`${JSON.stringify(framingCases[0].record)}\n${JSON.stringify(framingCases[1].record)}\n`);
		expect(oc.prompts).toEqual([]);
		await oc.emit("session.status", { sessionID: "ses_9", status: { type: "idle" } });
		expect(oc.prompts).toEqual([sent(framingCases[0].text, "ses_9"), sent(framingCases[1].text, "ses_9")]);
		await oc.emit("session.idle", { sessionID: "ses_9" });
		expect(oc.prompts.length).toBe(2);
	});

	test("PUSH-05: a new top-level session becomes the active session; updates do not", async () => {
		const oc = await load();
		spawned[0].out(`${JSON.stringify(framingCases[0].record)}\n`);
		await oc.emit("session.updated", { info: { id: "ses_old" } });
		expect(oc.prompts).toEqual([]);
		await oc.emit("session.created", { info: { id: "ses_new" } });
		expect(oc.prompts).toEqual([sent(framingCases[0].text, "ses_new")]);
	});

	test("subagent sessions never become the active session", async () => {
		const oc = await load();
		await oc.emit("session.status", { sessionID: "ses_main", status: { type: "busy" } });
		await oc.emit("session.created", { info: { id: "ses_sub", parentID: "ses_main" } });
		await oc.emit("session.status", { sessionID: "ses_sub", status: { type: "busy" } });
		spawned[0].out(`${JSON.stringify(framingCases[0].record)}\n`);
		expect(oc.prompts).toEqual([sent(framingCases[0].text, "ses_main")]);
	});

	test("PUSH-05: an SDK without promptAsync uses session.prompt", async () => {
		const oc = await load({ promptAsync: false });
		await oc.emit("session.idle", { sessionID: "ses_1" });
		spawned[0].out(`${JSON.stringify(framingCases[0].record)}\n`);
		expect(oc.syncPrompts).toEqual([sent(framingCases[0].text, "ses_1")]);
	});

	for (const c of framingCases) {
		test(`PUSH-06: framing - ${c.name}`, async () => {
			const oc = await load();
			await oc.emit("session.idle", { sessionID: "ses_1" });
			spawned[0].out(`${JSON.stringify(c.record)}\n`);
			expect(oc.prompts.map((p) => p.body.parts[0].text)).toEqual([c.text]);
		});
	}

	test("failed deliveries, stderr and restarts go to the OpenCode log", async () => {
		const oc = await load({ result: { error: { name: "NotFoundError" } } });
		await oc.emit("session.idle", { sessionID: "ses_gone" });
		spawned[0].out(`${JSON.stringify(framingCases[0].record)}\n`);
		spawned[0].err("partyline: server unreachable\n");
		spawned[0].exit(1);
		await flush();
		await flush();
		expect(oc.logs).toEqual([
			{ service: "partyline", level: "warn", message: "partyline: server unreachable" },
			{ service: "partyline", level: "warn", message: "partyline: listen exited (code 1); restarting in 1s" },
			{ service: "partyline", level: "warn", message: 'partyline: could not deliver a room message: {"name":"NotFoundError"}' },
		]);
		expect(time.delays()).toEqual([1000]);
	});

	test("a rejected prompt is logged", async () => {
		const oc = await load({ reject: true });
		await oc.emit("session.idle", { sessionID: "ses_1" });
		spawned[0].out(`${JSON.stringify(framingCases[0].record)}\n`);
		await flush();
		await flush();
		expect(oc.logs).toEqual([{ service: "partyline", level: "warn", message: "partyline: could not deliver a room message: connection refused" }]);
	});

	test("dispose ends the running listen", async () => {
		const oc = await load();
		await oc.hooks.dispose();
		expect(spawned[0].killed).toBe(true);
		spawned[0].exit(null);
		expect(time.delays()).toEqual([]);
	});

	test("dispose stops listen and its restarts", async () => {
		const oc = await load();
		spawned[0].exit(1);
		expect(time.delays()).toEqual([1000]);
		await oc.hooks.dispose();
		expect(time.delays()).toEqual([]);
		time.fire();
		expect(spawned.length).toBe(1);
	});
});
