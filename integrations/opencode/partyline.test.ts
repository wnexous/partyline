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
beforeEach(() => {
	spawned.length = 0;
	launched = process.env.PARTYLINE_HARNESS;
	process.env.PARTYLINE_HARNESS = "opencode"; // the launcher's environment
	time = new FakeTime();
});
afterEach(() => {
	if (launched === undefined) delete process.env.PARTYLINE_HARNESS;
	else process.env.PARTYLINE_HARNESS = launched;
	time.restore();
});

describe("OpenCode plugin", () => {
	test("the default export loads in both OpenCode APIs: id, server (1.x) and setup (2.x)", () => {
		const def = plugin.default as unknown as Record<string, unknown>;
		expect(Object.keys(def).sort()).toEqual(["id", "server", "setup"]);
		expect(def.id).toBe("partyline");
		expect(def.server).toBe(plugin.PartylinePlugin);
		expect(typeof def.setup).toBe("function");
		// OpenCode 1.4+ reads only the default of a module that has one; OpenCode 2 reads only the default.
		expect(Object.keys(plugin).sort()).toEqual(["PartylinePlugin", "default", "setupOpenCode2"]);
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

// --- OpenCode 2: the setup function of the default export, on a fake plugin context ---

type Ev = { type: string; data?: Record<string, unknown> };
type Prompt2 = { sessionID: string; text: string; delivery?: string };

function fakeContext2(opts: { parents?: Record<string, string>; wrapped?: boolean; reject?: boolean; getFails?: boolean } = {}) {
	const prompts: Prompt2[] = [];
	const gets: string[] = [];
	const subscribed: Array<{ types?: string[]; signal?: AbortSignal }> = [];
	const queue: Ev[] = [];
	let wake: (() => void) | undefined;
	let ended = false;
	const ctx = {
		location: { directory: "/work/proj" },
		session: {
			prompt: (o: Prompt2) => {
				prompts.push(o);
				return opts.reject ? Promise.reject(new Error("session not found")) : Promise.resolve({});
			},
			get: async ({ sessionID }: { sessionID: string }) => {
				gets.push(sessionID);
				if (opts.getFails) throw new Error("gone");
				const info = { id: sessionID, parentID: opts.parents?.[sessionID] };
				return opts.wrapped ? { data: info } : info;
			},
		},
		event: {
			subscribe: (o: { types?: string[]; signal?: AbortSignal }) => {
				subscribed.push(o);
				o.signal?.addEventListener("abort", () => {
					ended = true;
					wake?.();
				});
				return {
					async *[Symbol.asyncIterator]() {
						while (!ended) {
							const next = queue.shift();
							if (next) yield next;
							else await new Promise<void>((resolve) => (wake = resolve));
						}
					},
				};
			},
		},
	};
	const emit = async (type: string, data: Record<string, unknown>) => {
		queue.push({ type, data });
		wake?.();
		// let the consumer loop handle the event, including its awaited session.get
		for (let i = 0; i < 5; i++) await new Promise((resolve) => queueMicrotask(() => resolve(undefined)));
	};
	return { ctx, prompts, gets, subscribed, emit };
}

async function load2(opts: Parameters<typeof fakeContext2>[0] = {}, api: Api = plugin) {
	const c = fakeContext2(opts);
	const dispose = await api.setupOpenCode2(c.ctx as never);
	return { ...c, dispose };
}

describe("OpenCode 2 plugin", () => {
	let errors: string[];
	let consoleError: typeof console.error;
	beforeEach(() => {
		errors = [];
		consoleError = console.error;
		console.error = (...args: unknown[]) => void errors.push(args.join(" "));
	});
	afterEach(() => {
		console.error = consoleError;
	});

	test("the default export's setup is the OpenCode 2 entry point", async () => {
		const c = fakeContext2();
		const dispose = await (plugin.default as unknown as { setup: (ctx: never) => Promise<() => Promise<void>> }).setup(c.ctx as never);
		expect(spawned.length).toBe(1);
		await dispose();
	});

	test("launch-only: a plain OpenCode 2 server runs no listen and returns no cleanup", async () => {
		delete process.env.PARTYLINE_HARNESS;
		const oc = await load2();
		expect(spawned).toEqual([]);
		expect(oc.subscribed).toEqual([]);
		expect(oc.dispose).toBeUndefined();
	});

	test("setup --always: the installed plugin listens in OpenCode 2 without the launcher", async () => {
		delete process.env.PARTYLINE_HARNESS;
		const always = (await importAlways("opencode/partyline.ts")) as unknown as Api;
		const oc = await load2({}, always);
		expect(spawned.length).toBe(1);
		await oc.dispose?.();
	});

	test("PUSH-05: it runs partyline listen in the project directory of the plugin context", async () => {
		const oc = await load2();
		expect(spawned.length).toBe(1);
		expect(spawned[0].command).toBe("partyline");
		expect(spawned[0].args).toEqual(["listen"]);
		expect(spawned[0].options.cwd).toBe("/work/proj");
		expect(spawned[0].options.env?.PARTYLINE_HARNESS).toBe("opencode");
		await oc.dispose?.();
	});

	test("PUSH-05: each line goes to the active session with session.prompt, queued behind the turn", async () => {
		const oc = await load2();
		await oc.emit("session.status", { sessionID: "ses_1", status: { type: "busy" } });
		spawned[0].out(`${JSON.stringify(framingCases[0].record)}\n`);
		await oc.emit("session.execution.started", { sessionID: "ses_2" });
		spawned[0].out(`${JSON.stringify(framingCases[1].record)}\n`);
		expect(oc.prompts).toEqual([
			{ sessionID: "ses_1", text: framingCases[0].text, delivery: "queue" },
			{ sessionID: "ses_2", text: framingCases[1].text, delivery: "queue" },
		]);
		await oc.dispose?.();
	});

	test("the event stream asks for the session events only", async () => {
		const oc = await load2();
		expect(oc.subscribed.length).toBe(1);
		expect(oc.subscribed[0].types).toEqual(["session.created", "session.status", "session.idle", "session.execution.started"]);
		expect(oc.subscribed[0].signal).toBeInstanceOf(AbortSignal);
		await oc.dispose?.();
	});

	test("PUSH-05: without a session yet, messages wait and go to the first one in order", async () => {
		const oc = await load2();
		spawned[0].out(`${JSON.stringify(framingCases[0].record)}\n${JSON.stringify(framingCases[1].record)}\n`);
		expect(oc.prompts).toEqual([]);
		await oc.emit("session.created", { sessionID: "ses_9" });
		expect(oc.prompts.map((p) => [p.sessionID, p.text])).toEqual([
			["ses_9", framingCases[0].text],
			["ses_9", framingCases[1].text],
		]);
		await oc.emit("session.idle", { sessionID: "ses_9" });
		expect(oc.prompts.length).toBe(2);
		await oc.dispose?.();
	});

	test("a new top-level session becomes the active one and is not looked up", async () => {
		const oc = await load2();
		await oc.emit("session.created", { sessionID: "ses_a" });
		await oc.emit("session.created", { sessionID: "ses_b" });
		await oc.emit("session.status", { sessionID: "ses_b", status: { type: "busy" } });
		spawned[0].out(`${JSON.stringify(framingCases[0].record)}\n`);
		expect(oc.prompts.map((p) => p.sessionID)).toEqual(["ses_b"]);
		expect(oc.gets).toEqual([]);
		await oc.dispose?.();
	});

	test("subagent sessions never become the active session", async () => {
		const oc = await load2({ parents: { ses_old_sub: "ses_main" } });
		await oc.emit("session.status", { sessionID: "ses_main", status: { type: "busy" } });
		await oc.emit("session.created", { sessionID: "ses_sub", parentID: "ses_main" });
		await oc.emit("session.status", { sessionID: "ses_sub", status: { type: "busy" } });
		// a subagent created before the plugin subscribed is found through session.get
		await oc.emit("session.execution.started", { sessionID: "ses_old_sub" });
		spawned[0].out(`${JSON.stringify(framingCases[0].record)}\n`);
		expect(oc.prompts.map((p) => p.sessionID)).toEqual(["ses_main"]);
		expect(oc.gets).toEqual(["ses_main", "ses_old_sub"]);
		await oc.dispose?.();
	});

	test("session.get answers either the bare record or { data }", async () => {
		const oc = await load2({ wrapped: true, parents: { ses_sub: "ses_main" } });
		await oc.emit("session.status", { sessionID: "ses_sub", status: { type: "busy" } });
		await oc.emit("session.status", { sessionID: "ses_main", status: { type: "busy" } });
		spawned[0].out(`${JSON.stringify(framingCases[0].record)}\n`);
		expect(oc.prompts.map((p) => p.sessionID)).toEqual(["ses_main"]);
		await oc.dispose?.();
	});

	test("a session that cannot be read counts as a top-level one", async () => {
		const oc = await load2({ getFails: true });
		await oc.emit("session.status", { sessionID: "ses_1", status: { type: "busy" } });
		spawned[0].out(`${JSON.stringify(framingCases[0].record)}\n`);
		expect(oc.prompts.map((p) => p.sessionID)).toEqual(["ses_1"]);
		await oc.dispose?.();
	});

	test("events without a session id are ignored", async () => {
		const oc = await load2();
		await oc.emit("session.status", {});
		spawned[0].out(`${JSON.stringify(framingCases[0].record)}\n`);
		expect(oc.prompts).toEqual([]);
		await oc.dispose?.();
	});

	for (const c of framingCases) {
		test(`PUSH-06: framing - ${c.name}`, async () => {
			const oc = await load2();
			await oc.emit("session.idle", { sessionID: "ses_1" });
			spawned[0].out(`${JSON.stringify(c.record)}\n`);
			expect(oc.prompts.map((p) => p.text)).toEqual([c.text]);
			await oc.dispose?.();
		});
	}

	test("SES-05: exit code 3 is not restarted, other codes are, and stderr goes to the log", async () => {
		const oc = await load2();
		spawned[0].err("partyline: server unreachable\n");
		spawned[0].exit(1);
		expect(errors).toEqual(["partyline: server unreachable", "partyline: listen exited (code 1); restarting in 1s"]);
		expect(time.delays()).toEqual([1000]);
		time.fire();
		spawned[1].exit(3);
		expect(time.delays()).toEqual([]);
		await oc.dispose?.();
	});

	for (const c of notices) {
		test(`SES-04/SES-05: the ${c.reason} notice is logged, not sent to the model`, async () => {
			const oc = await load2();
			await oc.emit("session.idle", { sessionID: "ses_1" });
			spawned[0].out(`${JSON.stringify(c.record)}\n`);
			spawned[0].exit(3);
			expect(oc.prompts).toEqual([]);
			expect(errors).toEqual([c.record.content as string]);
			expect(time.delays()).toEqual([]);
			await oc.dispose?.();
		});
	}

	test("a rejected prompt is logged", async () => {
		const oc = await load2({ reject: true });
		await oc.emit("session.idle", { sessionID: "ses_1" });
		spawned[0].out(`${JSON.stringify(framingCases[0].record)}\n`);
		await flush();
		await flush();
		expect(errors).toEqual(["partyline: could not deliver a room message: session not found"]);
		await oc.dispose?.();
	});

	test("dispose ends the event stream and the running listen, and stops restarts", async () => {
		const oc = await load2();
		spawned[0].exit(1);
		expect(time.delays()).toEqual([1000]);
		await oc.dispose?.();
		expect(time.delays()).toEqual([]);
		time.fire();
		expect(spawned.length).toBe(1);

		const second = await load2();
		await second.dispose?.();
		expect(spawned[1].killed).toBe(true);
		expect(second.subscribed[0].signal?.aborted).toBe(true);
	});
});
