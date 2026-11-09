import { createHash, randomBytes } from "node:crypto";
import type {
  Message,
  Page as MessagePage,
  Room,
  SetupRequest,
  Upload,
} from "../src/shared/api/types.gen";
import { apiCall, test as base, type Credentials, expect } from "./fixtures";
import { expectNoSidewaysScroll, loginAs } from "./helpers";
import { type Instance, startInstance } from "./server";
import { shoot } from "./shots";

// A file larger than one request (LF-01) through the real server: this instance takes 1 MB per
// request (PARTYLINE_MAX_UPLOAD_MB=1), so the 3 MB file the web app sends is refused as one
// multipart post and goes up through an upload session instead, 1 MB a chunk, its card showing the
// progress. The message then carries the attachment, and it downloads whole, byte for byte.
const test = base.extend<object, { small: Instance }>({
  small: [
    // biome-ignore lint/correctness/noEmptyPattern: Playwright fixtures must destructure their first argument.
    async ({}, use) => {
      const instance = await startInstance({ PARTYLINE_MAX_UPLOAD_MB: "1" });
      await use(instance);
      await instance.stop();
    },
    { scope: "worker" },
  ],
  baseURL: async ({ small }, use) => {
    await use(small.url);
  },
});

const OWNER: Credentials & { display_name: string } = {
  username: "large-files-owner",
  password: "e2e-large-files-owner-password",
  display_name: "Files Owner",
};

const MB = 1 << 20;

let room: Room;

test.describe.configure({ mode: "serial" });

test.beforeAll(async ({ browser, small }) => {
  const context = await browser.newContext();
  try {
    await apiCall(context.request, small, "POST", "/setup", {
      token: small.setupToken,
      ...OWNER,
    } satisfies SetupRequest);
    room = await apiCall<Room>(context.request, small, "POST", "/groups", {
      name: "e2e-large-files",
      members: [],
    });
  } finally {
    await context.close();
  }
});

test("a file larger than one request goes up in chunks with its progress and downloads whole (LF-01)", async ({
  context,
  page,
  small,
}, testInfo) => {
  await loginAs(context, small, OWNER);
  const content = randomBytes(3 * MB + 12_345);
  const name = "footage.bin";

  // Each chunk takes a moment, so the card's progress can be seen on the way.
  const chunks: number[] = [];
  await page.route(/\/api\/v1\/uploads\/[^/?]+\?offset=/, async (route) => {
    if (route.request().method() === "PUT") {
      chunks.push(Number(new URL(route.request().url()).searchParams.get("offset")));
      await new Promise((done) => setTimeout(done, 400));
    }
    await route.continue();
  });

  await page.goto(`/rooms/${room.id}`);
  await page.getByLabel("Attach files").setInputFiles({
    name,
    mimeType: "application/octet-stream",
    buffer: content,
  });
  await page.getByRole("textbox", { name: /^Message/ }).fill("the whole footage");
  await page.getByRole("button", { name: "Send" }).click();

  const bar = page.getByRole("progressbar", { name: `Uploading ${name}` });
  await expect(bar).toBeVisible();
  await expect.poll(async () => Number(await bar.getAttribute("aria-valuenow"))).toBeGreaterThan(0);
  await expect(page.getByRole("button", { name: `Cancel the upload of ${name}` })).toBeVisible();
  // The card with its bar fits a 360 px phone like the rest of the composer.
  await expectNoSidewaysScroll(page, "the composer while a file uploads");
  await shoot(page, "large-file-uploading", testInfo);

  // The message arrives with the file as its attachment, with the size it has.
  const timeline = page.getByRole("log");
  await expect(timeline.getByText("the whole footage", { exact: true })).toBeVisible({
    timeout: 30_000,
  });
  const link = timeline.getByRole("link", { name: new RegExp(name.replace(".", "\\.")) });
  await expect(link).toBeVisible();
  await expect(link).toContainText("3 MB");
  await expect(bar).toHaveCount(0);
  // 1 MB a chunk (the server's chunk_size, its single-request size here), in order.
  expect(chunks).toEqual([0, MB, 2 * MB, 3 * MB]);

  const sent = (
    await apiCall<MessagePage<Message>>(context.request, small, "GET", `/rooms/${room.id}/messages`)
  ).items.find((m) => m.text === "the whole footage");
  expect(sent?.attachments).toHaveLength(1);
  expect(sent?.attachments[0]).toMatchObject({ name, size: content.length, status: "stored" });

  // The download is the file, whole.
  const href = await link.getAttribute("href");
  expect(href).toBe(`/api/v1/attachments/${sent?.attachments[0]?.id}`);
  const download = await context.request.get(`${small.url}${href}`);
  expect(download.ok()).toBe(true);
  const body = await download.body();
  expect(body.length).toBe(content.length);
  const digest = (b: Buffer) => createHash("sha256").update(b).digest("hex");
  expect(digest(body)).toBe(digest(content));
});

test("a cancelled upload stops and its session is deleted on the server (LF-01)", async ({
  context,
  page,
  small,
}) => {
  await loginAs(context, small, OWNER);
  const name = "cancelled.bin";
  // Slow chunks, so the cancel lands while the file is on its way.
  await page.route(/\/api\/v1\/uploads\/[^/?]+\?offset=/, async (route) => {
    if (route.request().method() === "PUT") await new Promise((done) => setTimeout(done, 600));
    await route.continue();
  });
  const created = page.waitForResponse(
    (res) => res.request().method() === "POST" && res.url().endsWith("/api/v1/uploads"),
  );
  // The DELETE that removed it: a 409 upload_busy first, while the server ends the aborted
  // chunk, is tried again.
  const deleted = page.waitForResponse(
    (res) =>
      res.request().method() === "DELETE" &&
      res.url().includes("/api/v1/uploads/") &&
      res.status() === 204,
  );

  await page.goto(`/rooms/${room.id}`);
  await page.getByLabel("Attach files").setInputFiles({
    name,
    mimeType: "application/octet-stream",
    buffer: randomBytes(3 * MB),
  });
  await page.getByRole("button", { name: "Send" }).click();
  const session = (await (await created).json()) as Upload;
  await expect(page.getByRole("progressbar", { name: `Uploading ${name}` })).toBeVisible();
  await page.getByRole("button", { name: `Cancel the upload of ${name}` }).click();
  await expect(page.getByText(name)).toHaveCount(0);

  // The session is gone at once, not a day later.
  expect((await deleted).url()).toContain(`/api/v1/uploads/${session.id}`);
  await expect
    .poll(async () =>
      (
        await context.request.get(`${small.url}/api/v1/uploads/${session.id}`, {
          headers: { Origin: small.url },
        })
      ).status(),
    )
    .toBe(404);
  // And nothing was posted.
  const messages = await apiCall<MessagePage<Message>>(
    context.request,
    small,
    "GET",
    `/rooms/${room.id}/messages`,
  );
  expect(messages.items.some((m) => m.attachments.some((a) => a.name === name))).toBe(false);
});
