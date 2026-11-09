import { createHash, randomBytes } from "node:crypto";
import type { Message, Page as MessagePage, Room, SetupRequest } from "../src/shared/api/types.gen";
import { apiCall, test as base, type Credentials, expect } from "./fixtures";
import { expectNoSidewaysScroll } from "./helpers";
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

test("a file larger than one request goes up in chunks with its progress and downloads whole (LF-01)", async ({
  context,
  page,
  small,
}, testInfo) => {
  await apiCall(context.request, small, "POST", "/setup", {
    token: small.setupToken,
    ...OWNER,
  } satisfies SetupRequest);
  const room = await apiCall<Room>(context.request, small, "POST", "/groups", {
    name: "e2e-large-files",
    members: [],
  });
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
