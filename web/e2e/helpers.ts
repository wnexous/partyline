import { randomBytes } from "node:crypto";
import { request } from "node:http";
import type { BrowserContext, Page, TestInfo } from "@playwright/test";
import type { AgentInfo, CreatedToken, Me } from "../src/lib/api/types.gen";
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

/**
 * An AI's live session, opened the way the channel opens it: the agent WebSocket with the machine and
 * folder headers. It resolves once the server's welcome frame arrives, which is when the AI is online;
 * `close()` takes it offline again. It is a plain HTTP upgrade and reads nothing of the frames: the
 * WebSocket of Node takes no request headers before Node 22, and the runner's Node is not pinned.
 */
export function openSession(instance: Instance, agent: AgentClient) {
  return new Promise<{ close: () => void }>((done, fail) => {
    const req = request(`${instance.url}/api/v1/agent/ws`, {
      headers: {
        Connection: "Upgrade",
        Upgrade: "websocket",
        "Sec-WebSocket-Version": "13",
        "Sec-WebSocket-Key": randomBytes(16).toString("base64"),
        Authorization: `Bearer ${agent.secret}`,
        "X-Partyline-Machine": agent.machine,
        "X-Partyline-Folder": agent.folder,
      },
    });
    const giveUp = setTimeout(() => {
      req.destroy();
      fail(new Error("the AI's session did not open in time"));
    }, 10_000);
    req.on("upgrade", (_res, socket, head) => {
      const opened = () => {
        clearTimeout(giveUp);
        done({ close: () => socket.destroy() });
      };
      // The welcome frame is the first thing the server writes, once the AI is attached.
      if (head.length > 0) opened();
      else socket.once("data", opened);
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

/** One call of the fake AI client, as its folder and machine (never as a client-supplied id). */
export function agentCall(
  context: BrowserContext,
  instance: Instance,
  agent: AgentClient,
  method: "GET" | "POST",
  path: string,
  data?: unknown,
) {
  return context.request.fetch(`${instance.url}/api/v1${path}`, {
    method,
    headers: {
      Authorization: `Bearer ${agent.secret}`,
      "X-Partyline-Machine": agent.machine,
      "X-Partyline-Folder": agent.folder,
    },
    data,
  });
}

export async function agentJSON<T>(
  context: BrowserContext,
  instance: Instance,
  agent: AgentClient,
  method: "GET" | "POST",
  path: string,
  data?: unknown,
): Promise<T> {
  const res = await agentCall(context, instance, agent, method, path, data);
  expect(res.ok(), `${method} ${path} → ${res.status()}`).toBe(true);
  return (await res.json()) as T;
}
