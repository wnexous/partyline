import { afterEach, beforeEach, describe, expect, mock, test } from "bun:test";
import { FakeTime, fakeSpawn, importAlways, spawned } from "../testdata/fakes.ts";
import { framingCases, notices } from "../testdata/framing.ts";

mock.module("node:child_process", () => ({ spawn: fakeSpawn }));
const plugin = await import("./partyline.ts");
type Api = typeof plugin;

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

async function load(opts: Parameters<typeof fakeClient>[0] = {}, api: Api = plugin) {
	const c = fakeClient(opts);
	const hooks = await api.PartylinePlugin({ client: c.client as never, directory: "/work/proj" } as never);
	const emit = (type: string, properties: Record<string, unknown>) => hooks.event({ event: { type, properties } });
	return { ...c, hooks, emit };
}

const flush = () => new Promise((resolve) => queueMicrotask(() => resolve(undefined)));
const sent = (text: string, id: string): Prompt => ({ path: { id }, body: { parts: [{ type: "text", text }] } });

let time: FakeTime;
let launched: string | undefined;
let liveVar: string | undefined;
beforeEach(() => {
	spawned.length = 0;
	liveVar = process.env.PARTYLINE_LIVE;
	delete process.env.PARTYLINE_LIVE;
	launched = process.env.PARTYLINE_HARNESS;
	process.env.PARTYLINE_HARNESS = "opencode"; // the launcher's environment
	time = new FakeTime();
});
afterEach(() => {
	if (liveVar === undefined) delete process.env.PARTYLINE_LIVE;
	else process.env.PARTYLINE_LIVE = liveVar;
	if (launched === undefined) delete process.env.PARTYLINE_HARNESS;
	else process.env.PARTYLINE_HARNESS = launched;
	time.restore();
});

describe("OpenCode plugin", () => {
	test("the plugin file exports only the plugin (OpenCode calls every export)", () => {
		expect(Object.keys(plugin)).toEqual(["PartylinePlugin"]);
		expect(typeof plugin.PartylinePlugin).toBe("function");
	});

	test("launch-only: a plain OpenCode session runs no listen", async () => {
		delete process.env.PARTYLINE_HARNESS;
		const oc = await load();
		expect(spawned).toEqual([]);
		expect(oc.prompts).toEqual([]);
		expect(oc.logs).toEqual([]);
	});

	test("launch-only: the launcher's PARTYLINE_HARNESS runs listen", async () => {
		await load();
		expect(spawned.length).toBe(1);
		expect(spawned[0].options.env?.PARTYLINE_HARNESS).toBe("opencode");
	});

	test("setup --always: the installed plugin listens without the launcher", async () => {
		delete process.env.PARTYLINE_HARNESS;
		const always = (await importAlways("opencode/partyline.ts")) as unknown as Api;
		await load({}, always);
		expect(spawned.length).toBe(1);
		expect(spawned[0].args).toEqual(["listen"]);
		expect(spawned[0].options.env?.PARTYLINE_HARNESS).toBe("opencode");
	});

	test("SESS-45: listen and the tools get one live id, tied to this process", async () => {
		await load();
		const live = spawned[0].options.env?.PARTYLINE_LIVE ?? "";
		expect(live).toMatch(new RegExp(`^${process.pid}:[A-Za-z0-9_-]{22}$`));
		expect(process.env.PARTYLINE_LIVE).toBe(live); // the MCP servers started later inherit it
	});

	test("SESS-45: the launcher's live id is kept when its pid is this process's", async () => {
		const launcher = `${process.pid}:LLLLLLLLLLLLLLLLLLLLLL`;
		process.env.PARTYLINE_LIVE = launcher;
		await load();
		expect(spawned[0].options.env?.PARTYLINE_LIVE).toBe(launcher);
	});

	test("SESS-45: an id inherited from another harness process is never shared", async () => {
		process.env.PARTYLINE_LIVE = `${process.pid + 1}:LLLLLLLLLLLLLLLLLLLLLL`;
		await load();
		const live = spawned[0].options.env?.PARTYLINE_LIVE ?? "";
		expect(live).toMatch(new RegExp(`^${process.pid}:[A-Za-z0-9_-]{22}$`));
		expect(live).not.toContain("LLLLLLLLLLLLLLLLLLLLLL");
	});

	test("SESS-45: a restarted listen is the same session: same live id", async () => {
		await load();
		spawned[0].exit(1);
		time.fire();
		expect(spawned.length).toBe(2);
		expect(spawned[1].options.env?.PARTYLINE_LIVE).toBe(spawned[0].options.env?.PARTYLINE_LIVE);
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

	test("SES-05: exit code 3 (another session holds the AI) is not restarted, other codes are", async () => {
		await load();
		spawned[0].exit(3);
		expect(time.delays()).toEqual([]);
		expect(spawned.length).toBe(1);

		for (const code of [1, 4, 127, null]) {
			await load();
			spawned[spawned.length - 1].exit(code);
			expect(time.delays()).toEqual([1000]);
			time.fire();
		}
	});

	for (const c of notices) {
		test(`SES-04/SES-05: the ${c.reason} notice is shown in the log, not sent to the model`, async () => {
			const oc = await load();
			await oc.emit("session.idle", { sessionID: "ses_1" });
			spawned[0].out(`${JSON.stringify(c.record)}\n`);
			spawned[0].exit(3);
			await flush();
			expect(oc.prompts).toEqual([]);
			expect(oc.syncPrompts).toEqual([]);
			expect(oc.logs).toEqual([{ service: "partyline", level: "warn", message: c.record.content }]);
			expect(time.delays()).toEqual([]);
		});
	}

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
