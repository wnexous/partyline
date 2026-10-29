import type {
  DevicePoll,
  DeviceRequestCreated,
  List,
  Me,
  Runner,
  Secret,
  SetupRequest,
} from "../src/shared/api/types.gen";
import { ADMIN, apiCall, test as base, expect } from "./fixtures";
import {
  expectNoSidewaysScroll,
  type FakeRunner,
  loginAs,
  MACHINE,
  startFakeRunner,
} from "./helpers";
import { type Instance, startInstance } from "./server";

// Container AIs, end to end against the built binary (CA-01..CA-06, CA-10): a fake runner client joins
// the way `partyline runner` does (device approval, a runner token, the WebSocket and its hello), the
// account page lists it online with Docker's version, a secret is written once and never shown again,
// and an AI created in a container is operated from its profile: stop, start, logs and delete. The
// fake runner answers the protocol without Docker, so no host needs it.
//
// The file has a server of its own: another file of the same project may share a worker's `instance`.
const test = base.extend<object, { containers: Instance }>({
  containers: [
    // biome-ignore lint/correctness/noEmptyPattern: Playwright fixtures must destructure their first argument.
    async ({}, use) => {
      const instance = await startInstance();
      await use(instance);
      await instance.stop();
    },
    { scope: "worker" },
  ],
  baseURL: async ({ containers }, use) => {
    await use(containers.url);
  },
});
test.describe.configure({ mode: "serial" });

const RUNNER_NAME = "e2e-runner";
const DOCKER_VERSION = "27.3.1";
/** Synthetic, and not a real key: it must never come back from the API or the page (CA-08). */
const SECRET_VALUE = "sk-e2e-not-a-real-key-0001";
const SECRET_NAME = "E2E_API_KEY";
const CONTAINER_SECRET = "E2E_CONTAINER_KEY";

let runner: Runner;
let fakeRunner: FakeRunner;

test.beforeAll(async ({ browser, containers: instance }) => {
  const context = await browser.newContext();
  try {
    const admin = await apiCall<Me>(context.request, instance, "POST", "/setup", {
      token: instance.setupToken,
      ...ADMIN,
    } satisfies SetupRequest);
    expect(admin.intelligence.handle).toBeTruthy();

    // What `partyline runner` does: ask for a runner token through the device approval.
    const createdResponse = await context.request.post(`${instance.url}/api/v1/devices`, {
      data: { machine: MACHINE, kind: "runner" },
      headers: { Origin: instance.url },
    });
    expect(createdResponse.ok(), `create the device request → ${createdResponse.status()}`).toBe(
      true,
    );
    const created = (await createdResponse.json()) as DeviceRequestCreated;
    await apiCall(context.request, instance, "POST", "/devices/approve", { code: created.code });
    const poll = (await (
      await context.request.post(`${instance.url}/api/v1/devices/poll`, {
        data: { poll_secret: created.poll_secret },
        headers: { Origin: instance.url },
      })
    ).json()) as DevicePoll;
    expect(poll.status).toBe("approved");
    expect(poll.token, "a runner token").toMatch(/^pl_runner_/);

    // Then it registers the machine, as the runner token (not as the person), and keeps its
    // connection.
    const registered = await context.request.post(`${instance.url}/api/v1/runner/register`, {
      data: { name: RUNNER_NAME, machine: MACHINE },
      headers: { Authorization: `Bearer ${poll.token}`, Origin: instance.url },
    });
    expect(registered.ok(), `register the runner → ${registered.status()}`).toBe(true);
    runner = (await registered.json()) as Runner;
    fakeRunner = await startFakeRunner(instance, poll.token ?? "", {
      docker_version: DOCKER_VERSION,
    });
    await expect
      .poll(
        async () => {
          const list = await apiCall<List<Runner>>(context.request, instance, "GET", "/runners");
          return list.items.find((r) => r.id === runner.id)?.online;
        },
        { message: "the runner connects", timeout: 15_000 },
      )
      .toBe(true);
  } finally {
    await context.close();
  }
});

test.afterAll(() => fakeRunner?.close());

test("the account page lists the runner the fake client registered (CA-01, CA-02, CA-10)", async ({
  context,
  page,
  containers: instance,
}, testInfo) => {
  await loginAs(context, instance, ADMIN);
  await page.goto("/account");

  const servers = page.getByRole("region", { name: "Servers" });
  await expect(servers.getByText(RUNNER_NAME)).toBeVisible();
  await expect(servers.getByText("Online")).toBeVisible();
  await expect(servers.getByText(MACHINE)).toBeVisible();
  await expect(servers.getByText(`Docker ${DOCKER_VERSION}`)).toBeVisible();
  await expect(servers.getByText("No AI runs here yet")).toBeVisible();

  // The commands that join another machine, copyable, and the check hint.
  await expect(servers.getByRole("figure", { name: "Run the runner" }).locator("pre")).toHaveText(
    "partyline runner",
  );
  await expect(
    servers.getByRole("figure", { name: "Keep it running after a reboot" }).locator("pre"),
  ).toHaveText("partyline runner install");
  await expect(servers.getByText(/partyline runner --check/)).toBeVisible();

  // The secrets section sits beside it, with nothing in it yet.
  const secrets = page.getByRole("region", { name: "Secrets" });
  await expect(secrets.getByRole("button", { name: "Add secret" })).toBeVisible();
  await expectNoSidewaysScroll(page, `account page (${testInfo.project.name})`);
});

test("a secret is written once and never comes back (CA-03, CA-08)", async ({
  context,
  page,
  containers: instance,
}) => {
  await loginAs(context, instance, ADMIN);
  await page.goto("/account");

  const secrets = page.getByRole("region", { name: "Secrets" });
  await secrets.getByRole("button", { name: "Add secret" }).click();
  const dialog = page.getByRole("dialog", { name: "New secret" });
  await dialog.getByRole("textbox", { name: "Name" }).fill(SECRET_NAME);
  await dialog.getByLabel("Value", { exact: true }).fill(SECRET_VALUE);
  await dialog.getByRole("button", { name: "Save secret" }).click();

  const row = secrets.getByTestId(`secret-${SECRET_NAME}`);
  await expect(row).toBeVisible();
  await expect(row).toContainText(SECRET_NAME);
  // The value is in the sealed row only: not on the page and not in the API's answer.
  await expect(page.getByText(SECRET_VALUE)).toHaveCount(0);
  const list = await apiCall<List<Secret>>(context.request, instance, "GET", "/secrets");
  expect(JSON.stringify(list)).not.toContain(SECRET_VALUE);

  // Replacing it keeps the name and takes a new value, again without showing either.
  await secrets.getByRole("button", { name: `Replace ${SECRET_NAME}` }).click();
  const replace = page.getByRole("dialog", { name: `Replace ${SECRET_NAME}` });
  await expect(replace.getByRole("textbox", { name: "Name" })).toBeDisabled();
  await replace.getByLabel("Value", { exact: true }).fill("sk-e2e-replaced-0002");
  await replace.getByRole("button", { name: "Replace" }).click();
  await expect(replace).toBeHidden();
  await expect(page.getByText("sk-e2e-replaced-0002")).toHaveCount(0);
});

test("an AI in a container starts, is operated and is deleted from its profile (CA-04, CA-05, CA-06)", async ({
  context,
  page,
  containers: instance,
}, testInfo) => {
  await loginAs(context, instance, ADMIN);
  // A credential for the app inside the container (the secret form is the test above).
  const saved = await context.request.put(`${instance.url}/api/v1/secrets/${CONTAINER_SECRET}`, {
    data: { value: SECRET_VALUE },
    headers: { Origin: instance.url },
  });
  expect(saved.ok(), `save the container secret → ${saved.status()}`).toBe(true);

  await page.goto("/contacts");
  await page.getByRole("button", { name: "New AI in a container" }).click();
  const dialog = page.getByRole("dialog", { name: "New AI in a container" });
  await dialog.getByRole("textbox", { name: "Name" }).fill("Cora");
  await dialog.getByRole("textbox", { name: "Handle" }).fill("cora");
  // The suggested variable of Claude Code, mapped to the stored secret.
  await dialog.getByRole("button", { name: "Map a secret" }).click();
  await dialog.getByRole("combobox", { name: "Secret" }).selectOption(CONTAINER_SECRET);
  await dialog.getByRole("button", { name: "Create AI" }).click();

  // The new AI's profile opens; its container comes up as the fake runner reports it.
  await expect(page).toHaveURL(/\/contacts\/[0-9a-f-]{8,}$/);
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Cora");
  const container = page.getByRole("region", { name: "Container" });
  await expect(container.getByText("Running", { exact: true })).toBeVisible({ timeout: 20_000 });
  expect(fakeRunner.commands.some((c) => c.op === "run")).toBe(true);

  // The logs are the runner's, in monospace, and the page does not scroll sideways with them.
  await container.getByRole("button", { name: "View logs" }).click();
  const logs = page.getByRole("dialog", { name: "Last 200 log lines" });
  await expect(logs.locator("pre")).toContainText("fake runner: the container started");
  await logs.getByRole("button", { name: "Refresh logs" }).click();
  await expectNoSidewaysScroll(page, `logs (${testInfo.project.name})`);
  await page.keyboard.press("Escape");

  // Stop and start go through the runner, which reports the state back.
  await container.getByRole("button", { name: "Stop", exact: true }).click();
  await expect(container.getByText("Stopped", { exact: true })).toBeVisible();
  await container.getByRole("button", { name: "Start", exact: true }).click();
  await expect(container.getByText("Running", { exact: true })).toBeVisible();
  await expect(container.getByRole("button", { name: "Restart", exact: true })).toBeEnabled();

  // Deleting it asks whether the workspace goes too; the AI stays.
  await container.getByRole("button", { name: "Delete container", exact: true }).click();
  const confirm = page.getByRole("alertdialog", { name: "Delete the container of Cora?" });
  await confirm.getByRole("checkbox", { name: "Also remove the workspace volume" }).check();
  await confirm.getByRole("button", { name: "Delete container" }).click();
  await expect(container).toHaveCount(0);
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Cora");
  const ops = fakeRunner.commands.map((c) => c.op);
  expect(ops).toEqual(expect.arrayContaining(["run", "stop", "start", "logs", "remove"]));
  await expectNoSidewaysScroll(page, `container profile (${testInfo.project.name})`);
});
