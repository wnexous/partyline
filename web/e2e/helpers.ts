import { randomBytes } from "node:crypto";
import { request } from "node:http";
import type { BrowserContext, Page, TestInfo } from "@playwright/test";
import type { AgentInfo, CreatedToken, Me } from "../src/shared/api/types.gen";
import { apiCall, type Credentials, expect } from "./fixtures";
import type { Instance } from "./server";

/** The widest viewport the app treats as a phone: below it the conversation list and each page take the whole screen. */
export const MOBILE_MAX_WIDTH = 767;

/** The machine every fake AI session reports; the server keeps it inside the AI's identity and shows it nowhere (AD-022). */
export const MACHINE = "e2e-box";

export function isMobile(testInfo: TestInfo) {
  return (testInfo.project.use.viewport?.width ?? 1280) <= MOBILE_MAX_WIDTH;
}

/** Logs a context in through the API; the session cookie lands in the browser context. */
export async function loginAs(context: BrowserContext, instance: Instance, who: Credentials) {
  const { username, password } = who;
  await apiCall<Me>(context.request, instance, "POST", "/auth/login", { username, password });
}

/** True once the live socket delivered its `hello` frame (it is then subscribed). */
export function trackLive(page: Page) {
  const state = { ready: false };
  page.on("websocket", (ws) => {
    ws.on("framereceived", ({ payload }) => {
      if (typeof payload === "string" && payload.includes('"type":"hello"')) state.ready = true;
    });
  });
  return state;
}

/**
 * The page itself must not scroll sideways (UI-24, CHART-04): the document is never wider than the
 * window it sits in. The shell lets these pages scroll as a document, so its metrics are the ones to
 * read. A failure names the elements that stick out, which is where to look.
 */
export async function expectNoSidewaysScroll(page: Page, where: string) {
  const width = await page.evaluate(() => {
    const client = document.documentElement.clientWidth;
    const wide = [...document.querySelectorAll("body *")]
      .filter((el) => el.getBoundingClientRect().right > client + 1)
      .slice(0, 6)
      .map((el) => {
        const classes = typeof el.className === "string" ? el.className : "";
        return `<${el.tagName.toLowerCase()} class="${classes.slice(0, 80)}"> "${(el.textContent ?? "").slice(0, 40)}"`;
      });
    return { scroll: document.documentElement.scrollWidth, client, wide };
  });
  expect(
    width.scroll,
    `${where} scrolls sideways; sticking out: ${width.wide.join(" | ")}`,
  ).toBeLessThanOrEqual(width.client);
}

/**
 * What a page reports when it does not fit: the document's width beside the window's, and every
 * visible element whose own text spills out of it. Elements that clip or ellipsize their text are
 * doing what they were made for and are not listed.
 */
export async function fitReport(page: Page) {
  return page.evaluate(() => {
    const overflowing: string[] = [];
    for (const el of document.querySelectorAll("body *")) {
      const style = getComputedStyle(el);
      if (style.display === "none" || style.visibility === "hidden") continue;
      if (el.getClientRects().length === 0) continue;
      const hasText = Array.from(el.childNodes).some(
        (node) => node.nodeType === Node.TEXT_NODE && (node.textContent ?? "").trim() !== "",
      );
      if (!hasText) continue;
      // A row that is cut with an ellipsis, or clipped by its own box, is meant to do that.
      if (style.overflowX !== "visible" || style.textOverflow === "ellipsis") continue;
      if (el.scrollWidth <= el.clientWidth + 1) continue;
      const classes = typeof el.className === "string" ? el.className : "";
      const text = (el.textContent ?? "").replace(/\s+/g, " ").trim();
      overflowing.push(
        `<${el.tagName.toLowerCase()} class="${classes.slice(0, 80)}"> "${text.slice(0, 40)}"`,
      );
    }
    return {
      scrollWidth: document.documentElement.scrollWidth,
      innerWidth: window.innerWidth,
      overflowing,
    };
  });
}

/** The whole page fits the window and no visible text spills out of its own box (UI-24). */
export async function expectFits(page: Page, where: string) {
  const report = await fitReport(page);
  expect(
    report.scrollWidth,
    `${where} scrolls sideways (${report.scrollWidth} > ${report.innerWidth})`,
  ).toBeLessThanOrEqual(report.innerWidth);
  expect(
    report.overflowing,
    `${where} has text spilling out of its box: ${report.overflowing.join(" | ")}`,
  ).toEqual([]);
}

/**
 * A fake AI client (AID-01, AID-02): an agent token plus the machine and folder headers the CLI
 * sends. The AI is the one the folder names, so two connects of one folder are one AI.
 */
export interface AgentClient {
  secret: string;
  ai: AgentInfo;
  machine: string;
  /** The folder header as the CLI sends it (`url.PathEscape`d, so `/` is `%2F`). */
  folder: string;
}

/** Where a new AI goes, as `partyline <harness> --org --position --parent` sends it (ORG-04, POS-07, POS-08). */
export interface Placement {
  /** An organization's address or id. */
  org?: string;
  /** A position's name in that organization. */
  position?: string;
  /** The @handle of the AI that starts this one. */
  parent?: string;
}

export async function connectAI(
  context: BrowserContext,
  instance: Instance,
  folder: string,
  machine = MACHINE,
  placement: Placement = {},
): Promise<AgentClient> {
  const token = await apiCall<CreatedToken>(context.request, instance, "POST", "/tokens", {
    kind: "agent",
    // A token's name is at most 64 characters; a long folder must not stop the AI from connecting.
    name: `e2e ${folder}`.slice(0, 64),
  });
  const folderHeader = encodeURIComponent(folder);
  const res = await context.request.post(`${instance.url}/api/v1/agent/register`, {
    headers: {
      Authorization: `Bearer ${token.secret}`,
      "X-Partyline-Machine": machine,
      "X-Partyline-Folder": folderHeader,
      ...(placement.org ? { "X-Partyline-Org": placement.org } : {}),
      ...(placement.position
        ? { "X-Partyline-Position": encodeURIComponent(placement.position) }
        : {}),
      ...(placement.parent ? { "X-Partyline-Parent": placement.parent } : {}),
    },
  });
  expect(res.ok(), `register ${folder} → ${res.status()}`).toBe(true);
  return {
    secret: token.secret,
    ai: (await res.json()) as AgentInfo,
    machine,
    folder: folderHeader,
  };
}

/** One WebSocket frame of the raw socket, once all of it has arrived. */
interface RawFrame {
  size: number;
  opcode: number;
  payload: Buffer;
}

/** Reads the first frame off `buffer`, or null while it is still incomplete (RFC 6455 §5.2). */
function takeFrame(buffer: Buffer): RawFrame | null {
  if (buffer.length < 2) return null;
  const opcode = buffer.readUInt8(0) & 0x0f;
  const masked = (buffer.readUInt8(1) & 0x80) !== 0;
  let length = buffer.readUInt8(1) & 0x7f;
  let offset = 2;
  if (length === 126) {
    if (buffer.length < 4) return null;
    length = buffer.readUInt16BE(2);
    offset = 4;
  } else if (length === 127) {
    if (buffer.length < 10) return null;
    length = Number(buffer.readBigUInt64BE(2));
    offset = 10;
  }
  const maskOffset = offset;
  if (masked) offset += 4;
  if (buffer.length < offset + length) return null;
  let payload = buffer.subarray(offset, offset + length);
  if (masked) {
    const mask = buffer.subarray(maskOffset, maskOffset + 4);
    payload = Buffer.from(payload.map((byte, i) => byte ^ mask.readUInt8(i % 4)));
  }
  return { size: offset + length, opcode, payload };
}

/** A masked pong for a ping the server sent, so an idle session stays live (RFC 6455 §5.5.2). */
function pongFrame(payload: Buffer): Buffer {
  const mask = randomBytes(4);
  // A ping payload is small enough not to need the 126/127 extended-length forms.
  const header = Buffer.from([0x8a, 0x80 | payload.length]);
  return Buffer.concat([header, mask, payload.map((byte, i) => byte ^ mask.readUInt8(i % 4))]);
}

/** What a live session says about itself besides the AI's machine and folder (SESS-10..SESS-14). */
export interface SessionOptions {
  /** The process id the socket sends as `X-Partyline-Live`; absent sends none. */
  live?: string;
  /** The harness id the session reports (`X-Partyline-Harness`); absent sends none. */
  harness?: string;
  /** The folder of a linked worktree session (a raw path, percent-encoded here); absent is the AI's. */
  folder?: string;
  /** The main worktree folder that identifies the AI (`X-Partyline-MainFolder`, a raw path). */
  mainFolder?: string;
  /** The harness's own session id (`X-Partyline-Harness-Session`): what the principal role is pinned to. */
  harnessSession?: string;
}

/**
 * An AI's live session, opened the way the channel opens it: the agent WebSocket with the machine and
 * folder headers. It resolves once the server's welcome frame arrives, which is when the AI is online;
 * `close()` takes it offline again, and `live` is the process id the socket sent (`""` when none). It
 * is a plain HTTP upgrade, because the WebSocket of Node takes no request headers before Node 22 and
 * the runner's Node is not pinned; its frames are read only to answer the server's pings, so a session
 * kept open for a whole spec does not time out.
 */
export function openSession(instance: Instance, agent: AgentClient, options: SessionOptions = {}) {
  return new Promise<{ close: () => void; live: string }>((done, fail) => {
    const live = options.live ?? "";
    const headers: Record<string, string> = {
      Connection: "Upgrade",
      Upgrade: "websocket",
      "Sec-WebSocket-Version": "13",
      "Sec-WebSocket-Key": randomBytes(16).toString("base64"),
      Authorization: `Bearer ${agent.secret}`,
      "X-Partyline-Machine": agent.machine,
      "X-Partyline-Folder": options.folder ? encodeURIComponent(options.folder) : agent.folder,
    };
    if (live) headers["X-Partyline-Live"] = live;
    if (options.harness) headers["X-Partyline-Harness"] = options.harness;
    if (options.harnessSession) headers["X-Partyline-Harness-Session"] = options.harnessSession;
    if (options.mainFolder) {
      headers["X-Partyline-MainFolder"] = encodeURIComponent(options.mainFolder);
    }
    const req = request(`${instance.url}/api/v1/agent/ws`, { headers });
    const giveUp = setTimeout(() => {
      req.destroy();
      fail(new Error("the AI's session did not open in time"));
    }, 10_000);
    req.on("upgrade", (_res, socket, head) => {
      let opened = false;
      const welcome = () => {
        if (opened) return;
        opened = true;
        clearTimeout(giveUp);
        done({ close: () => socket.destroy(), live });
      };
      // The welcome frame is the first text frame the server writes, once the AI is attached; the
      // pings that follow are answered so the session lasts as long as the test needs it.
      let buffered = head;
      const read = (chunk: Buffer) => {
        buffered = Buffer.concat([buffered, chunk]);
        for (;;) {
          const frame = takeFrame(buffered);
          if (!frame) return;
          buffered = buffered.subarray(frame.size);
          if (frame.opcode === 0x9) socket.write(pongFrame(frame.payload));
          else if (frame.opcode === 0x8) socket.destroy();
          else if (frame.opcode === 0x1) welcome();
        }
      };
      socket.on("data", read);
      read(Buffer.alloc(0));
    });
    req.on("response", (res) => {
      clearTimeout(giveUp);
      fail(new Error(`the AI's session was refused: ${res.statusCode}`));
    });
    req.on("error", (err) => {
      clearTimeout(giveUp);
      fail(err);
    });
    req.end();
  });
}

/**
 * One call of the fake AI client, as its folder and machine (never as a client-supplied id). `live` is
 * the process id of the session the call acts for (`X-Partyline-Live`, SESS-29); absent sends none, as
 * a tools-only process does.
 */
export function agentCall(
  context: BrowserContext,
  instance: Instance,
  agent: AgentClient,
  method: "GET" | "POST" | "PATCH" | "DELETE",
  path: string,
  data?: unknown,
  live?: string,
) {
  return context.request.fetch(`${instance.url}/api/v1${path}`, {
    method,
    headers: {
      Authorization: `Bearer ${agent.secret}`,
      "X-Partyline-Machine": agent.machine,
      "X-Partyline-Folder": agent.folder,
      ...(live ? { "X-Partyline-Live": live } : {}),
    },
    data,
  });
}

export async function agentJSON<T>(
  context: BrowserContext,
  instance: Instance,
  agent: AgentClient,
  method: "GET" | "POST" | "PATCH" | "DELETE",
  path: string,
  data?: unknown,
  live?: string,
): Promise<T> {
  const res = await agentCall(context, instance, agent, method, path, data, live);
  expect(res.ok(), `${method} ${path} → ${res.status()}`).toBe(true);
  return (await res.json()) as T;
}

/**
 * A masked client-to-server WebSocket frame (RFC 6455 §5.3): every frame a client sends must be
 * masked, and the runner's hello is the only one the fake runner writes.
 */
function maskedText(payload: string): Buffer {
  const data = Buffer.from(payload);
  if (data.length >= 126) throw new Error("the fake runner's frames stay under 126 bytes");
  const mask = randomBytes(4);
  const header = Buffer.from([0x81, 0x80 | data.length]);
  return Buffer.concat([header, mask, data.map((byte, i) => byte ^ mask.readUInt8(i % 4))]);
}

/** What a fake runner reports in its hello (CA-02): Docker's version or why it is unusable. */
export interface FakeRunnerHello {
  docker_version?: string;
  docker_error?: string;
  containers?: { name: string; state: string; error?: string }[];
}

/** What the server asked the fake runner to do, for the assertions of a spec. */
export interface RunnerCommand {
  op: "run" | "start" | "stop" | "restart" | "remove" | "logs" | "state";
  name?: string;
  run?: { name: string };
}

/** A live fake `partyline runner`: its socket and the commands it answered. */
export interface FakeRunner {
  close: () => void;
  commands: RunnerCommand[];
}

/**
 * A fake runner client (CA-01, CA-02, CA-09): the runner WebSocket with a runner token, a hello,
 * and an answer to every command the way the real executor answers — a result, plus the state
 * event the runner would report, so the page shows the container change without waiting for a poll.
 * It runs no Docker: `run`, `start`, `stop`, `restart` and `remove` all succeed and report the state
 * they leave behind.
 */
export function startFakeRunner(
  instance: Instance,
  token: string,
  hello: FakeRunnerHello = { docker_version: "27.3.1", containers: [] },
): Promise<FakeRunner> {
  return new Promise<FakeRunner>((done, fail) => {
    const commands: RunnerCommand[] = [];
    const headers: Record<string, string> = {
      Connection: "Upgrade",
      Upgrade: "websocket",
      "Sec-WebSocket-Version": "13",
      "Sec-WebSocket-Key": randomBytes(16).toString("base64"),
      Authorization: `Bearer ${token}`,
    };
    const req = request(`${instance.url}/api/v1/runner/ws`, { headers });
    const giveUp = setTimeout(() => {
      req.destroy();
      fail(new Error("the runner's connection did not open in time"));
    }, 10_000);
    req.on("upgrade", (_res, socket, head) => {
      clearTimeout(giveUp);
      const send = (frame: unknown) => socket.write(maskedText(JSON.stringify(frame)));
      const state = (name: string, value: string) =>
        send({ type: "state", containers: [{ name, state: value }] });
      send({ type: "hello", ...hello });

      let buffered = head;
      const read = (chunk: Buffer) => {
        buffered = Buffer.concat([buffered, chunk]);
        for (;;) {
          const frame = takeFrame(buffered);
          if (!frame) return;
          buffered = buffered.subarray(frame.size);
          if (frame.opcode === 0x9) {
            socket.write(pongFrame(frame.payload));
            continue;
          }
          if (frame.opcode === 0x8) {
            socket.destroy();
            return;
          }
          if (frame.opcode !== 0x1) continue;
          const message = JSON.parse(frame.payload.toString()) as {
            type: string;
            id?: string;
            op?: RunnerCommand["op"];
            name?: string;
            run?: RunnerCommand["run"];
          };
          if (message.type !== "command" || !message.id || !message.op) continue;
          commands.push({ op: message.op, name: message.name, run: message.run });
          const result: Record<string, unknown> = { type: "result", id: message.id, ok: true };
          if (message.op === "logs") result.logs = "fake runner: the container started\n";
          if (message.op === "state") {
            result.containers = hello.containers ?? [];
          }
          send(result);
          // The real runner reports what it sees on `docker events`: the operation's outcome.
          const name = message.run?.name ?? message.name;
          if (!name) continue;
          if (message.op === "run" || message.op === "start" || message.op === "restart") {
            state(name, "running");
          } else if (message.op === "stop") {
            state(name, "stopped");
          }
        }
      };
      socket.on("data", read);
      read(Buffer.alloc(0));
      done({
        commands,
        close: () => socket.destroy(),
      });
    });
    req.on("response", (res) => {
      clearTimeout(giveUp);
      fail(new Error(`the runner's connection was refused: ${res.statusCode}`));
    });
    req.on("error", (err) => {
      clearTimeout(giveUp);
      fail(err);
    });
    req.end();
  });
}
