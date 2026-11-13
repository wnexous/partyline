import { mkdir, mkdtemp, readFile, rename, rm, stat, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type {
  AgentSecretAckFrame,
  AgentSecretFrame,
  Me,
  Message,
  Page as MessagePage,
  PostMessageRequest,
  Room,
  SetupRequest,
} from "../src/shared/api/types.gen";
import { apiCall, test as base, type Credentials, expect } from "./fixtures";
import { agentJSON, connectAI, loginAs, MACHINE, openSession } from "./helpers";
import { type Instance, startInstance } from "./server";

// A secret given through the web app (SR-13): an AI asks for EXAMPLE_API_KEY, the owner, who manages it,
// pastes the value into the card and presses Save, and the live session that asked writes it to its file.
// The session is this test over the raw agent socket, acting as the client does (SR-08): it writes the file
// with mode 0600 and answers with the path. The value must appear in that file and in the one secret frame,
// and in no API response, no live frame of the web app and no line of the server's log (SR-07). The file has
// a server of its own, at debug level, so its log says everything it would ever say.
const test = base.extend<object, { secrets: Instance }>({
  secrets: [
    // biome-ignore lint/correctness/noEmptyPattern: Playwright fixtures must destructure their first argument.
    async ({}, use) => {
      const instance = await startInstance({ PARTYLINE_LOG_LEVEL: "debug" });
      await use(instance);
      await instance.stop();
    },
    { scope: "worker" },
  ],
  baseURL: async ({ secrets }, use) => {
    await use(secrets.url);
  },
});
test.describe.configure({ mode: "serial" });

const OWNER: Credentials & { display_name: string } = {
  username: "secrets-owner",
  password: "e2e-secrets-owner-password",
  display_name: "Secrets Owner",
};
const NAME = "EXAMPLE_API_KEY";
const REASON = "To send the weekly report by email.";
/** The value the owner pastes: synthetic, and distinctive enough to search for anywhere. */
const VALUE = "e2e-example-secret-6b1d";

let room: Room;
let request: Message;
let sessionDir = "";
/** Every text frame the session received, the secret frame among them. */
const sessionFrames: string[] = [];
let closeSession: () => void = () => {};

test.beforeAll(async ({ browser, secrets }) => {
  sessionDir = await mkdtemp(join(tmpdir(), "partyline-e2e-secrets-"));
  const context = await browser.newContext();
  try {
    await apiCall<Me>(context.request, secrets, "POST", "/setup", {
      token: secrets.setupToken,
      ...OWNER,
    } satisfies SetupRequest);
    const agent = await connectAI(context, secrets, "/work/secrets", MACHINE);
    room = await apiCall<Room>(context.request, secrets, "POST", "/groups", {
      name: "Report",
      members: [agent.ai.id],
    });
    // The AI's live session, with a client that knows the secret frame (SR-10). It saves a secret the way
    // the channel does: a private file in the AI's own folder, renamed into place, then the answer.
    const session = await openSession(secrets, agent, {
      live: "e2e-secrets-live",
      clientVersion: "dev",
      onText: (text, reply) => {
        sessionFrames.push(text);
        const frame = JSON.parse(text) as { type: string };
        if (frame.type !== "secret") return;
        const secret = frame as AgentSecretFrame;
        void (async () => {
          const dir = join(sessionDir, agent.ai.id);
          await mkdir(dir, { recursive: true, mode: 0o700 });
          const path = join(dir, secret.name);
          await writeFile(`${path}.tmp`, secret.value, { mode: 0o600 });
          await rename(`${path}.tmp`, path);
          reply(
            JSON.stringify({
              type: "secret_ack",
              request_id: secret.request_id,
              ok: true,
              path,
            } satisfies AgentSecretAckFrame),
          );
        })();
      },
    });
    closeSession = session.close;
    request = await agentJSON<Message>(
      context,
      secrets,
      agent,
      "POST",
      `/agent/rooms/${room.id}/messages`,
      { text: REASON, secret: { name: NAME } } satisfies PostMessageRequest,
      "e2e-secrets-live",
    );
    expect(request.interactive?.secret?.name).toBe(NAME);
  } finally {
    await context.close();
  }
});

test.afterAll(async () => {
  closeSession();
  await rm(sessionDir, { recursive: true, force: true });
});

test("a secret pasted in the card reaches the session's file and nothing else (SR-13)", async ({
  context,
  page,
  secrets,
}) => {
  // Everything the web app reads while the secret is given: each API answer and each live frame.
  const bodies: Promise<string>[] = [];
  page.on("response", (response) => {
    if (response.url().includes("/api/")) bodies.push(response.text().catch(() => ""));
  });
  const liveFrames: string[] = [];
  page.on("websocket", (ws) => {
    ws.on("framereceived", ({ payload }) => liveFrames.push(String(payload)));
  });

  await loginAs(context, secrets, OWNER);
  await page.goto(`/rooms/${room.id}`);
  const log = page.getByRole("log");
  await expect(log.getByText(REASON)).toBeVisible();
  const card = log.getByRole("region", { name: `Secret requested: ${NAME}` });
  const field = card.getByLabel(`Value of ${NAME}`);
  await expect(field).toHaveAttribute("type", "password");
  await field.fill(VALUE);

  const saved = page.waitForResponse(
    (response) =>
      response.url() === `${secrets.url}/api/v1/rooms/${room.id}/messages/${request.id}/secret` &&
      response.request().method() === "POST",
  );
  await card.getByRole("button", { name: "Save" }).click();
  expect((await saved).status()).toBe(201);
  await expect(field).toHaveCount(0);
  await expect(card.getByRole("status")).toHaveText(`Saved by ${OWNER.display_name}`);
  // The conversation keeps only that it was saved: one reply with the name.
  await expect(log.getByText(`🔑 ${NAME} saved`)).toBeVisible();

  // The session wrote the value to its private file (SR-08).
  const path = join(sessionDir, request.author_id ?? "", NAME);
  expect(await readFile(path, "utf8")).toBe(VALUE);
  expect((await stat(path)).mode & 0o777).toBe(0o600);
  // It came in exactly one frame, the secret frame; the answer reached the session without it.
  const carrying = sessionFrames.filter((text) => text.includes(VALUE));
  expect(carrying).toHaveLength(1);
  expect(JSON.parse(carrying[0] ?? "{}").type).toBe("secret");
  await expect
    .poll(() => sessionFrames.some((text) => text.includes(`🔑 ${NAME} saved`)))
    .toBe(true);

  // Nowhere else (SR-07): the answers the web app read, its live frames, the room's history and the log.
  const history = await apiCall<MessagePage<Message>>(
    context.request,
    secrets,
    "GET",
    `/rooms/${room.id}/messages`,
  );
  expect(JSON.stringify(history)).not.toContain(VALUE);
  for (const body of await Promise.all(bodies)) expect(body).not.toContain(VALUE);
  expect(liveFrames.length).toBeGreaterThan(0);
  for (const frame of liveFrames) expect(frame).not.toContain(VALUE);
  expect(secrets.log()).not.toContain(VALUE);
});
