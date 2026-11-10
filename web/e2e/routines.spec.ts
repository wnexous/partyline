import type {
  List,
  Me,
  ScheduleList,
  ScheduleRun,
  SetupRequest,
} from "../src/shared/api/types.gen";
import { apiCall, test as base, type Credentials, expect } from "./fixtures";
import { type AgentClient, connectAI, expectNoSidewaysScroll, loginAs, MACHINE } from "./helpers";
import { type Instance, startInstance } from "./server";

// The owner sets a routine of an AI by hand on its profile (HB-13): creates one from a preset, sees the
// rule in words, pauses it, runs it now, sees that run in its history and deletes it. Each step is read
// back through the API, so what the page shows is what the server stored. No session of the AI is open, so the run waits for the
// principal. At 360 px the page and the dialog must not scroll sideways.
//
// The file has a server of its own, like session-notifications.spec.ts.
const test = base.extend<object, { routines: Instance }>({
  routines: [
    // biome-ignore lint/correctness/noEmptyPattern: Playwright fixtures must destructure their first argument.
    async ({}, use) => {
      const instance = await startInstance();
      await use(instance);
      await instance.stop();
    },
    { scope: "worker" },
  ],
  baseURL: async ({ routines }, use) => {
    await use(routines.url);
  },
});
test.describe.configure({ mode: "serial" });

const OWNER: Credentials & { display_name: string } = {
  username: "routines-owner",
  password: "e2e-routines-owner-password",
  display_name: "Routines Owner",
};
const AI_FOLDER = "/work/atlas-routines";
const NAME = "Morning summary";
const PROMPT = "Summarize what happened overnight in the deploys room.";

let agent: AgentClient;

test.beforeAll(async ({ browser, routines: instance }) => {
  const context = await browser.newContext();
  try {
    await apiCall<Me>(context.request, instance, "POST", "/setup", {
      token: instance.setupToken,
      ...OWNER,
    } satisfies SetupRequest);
    agent = await connectAI(context, instance, AI_FOLDER, MACHINE);
  } finally {
    await context.close();
  }
});

/** The AI's routines as the server stores them. */
function stored(context: Parameters<typeof loginAs>[0], instance: Instance) {
  return apiCall<ScheduleList>(
    context.request,
    instance,
    "GET",
    `/agents/${agent.ai.id}/schedules`,
  );
}

test("the owner creates, pauses, runs and deletes a routine of an AI (HB-13)", async ({
  context,
  page,
  routines: instance,
}) => {
  await loginAs(context, instance, OWNER);
  await page.goto(`/contacts/${agent.ai.id}`);
  await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
  const section = page.getByRole("region", { name: "Routines" });
  await expect(section.getByText(/No routines yet/)).toBeVisible();

  // Create: every day at a time, said in words as it is picked.
  await section.getByRole("button", { name: "New routine" }).click();
  const dialog = page.getByRole("dialog", { name: /New routine for/ });
  await expect(dialog).toBeVisible();
  await dialog.getByLabel("Name", { exact: true }).fill(NAME);
  await dialog.getByLabel("What to ask").fill(PROMPT);
  await dialog.getByLabel("Repeat").selectOption("weekdays");
  await dialog.getByLabel("Time", { exact: true }).fill("08:30");
  await expect(dialog.getByTestId("routine-preview")).toHaveText(
    /^Runs: Monday to Friday at 08:30\sAM$/,
  );
  await expectNoSidewaysScroll(page, "the routine dialog");
  await dialog.getByRole("button", { name: "Create routine" }).click();
  await expect(dialog).toBeHidden();

  const row = section.getByRole("listitem").filter({ hasText: NAME });
  await expect(row.getByTestId("routine-rule")).toHaveText(/^Monday to Friday at 08:30\sAM/);
  await expect(row.getByText(PROMPT)).toBeVisible();
  await expect(row.getByTestId("routine-next")).toHaveText(/^Next: /);
  await expect(row.getByText(`Set by ${OWNER.display_name}`)).toBeVisible();
  await expectNoSidewaysScroll(page, "the profile with a routine");
  let list = await stored(context, instance);
  expect(list.items).toHaveLength(1);
  expect(list.items[0]).toMatchObject({ name: NAME, cron: "30 8 * * 1-5", enabled: true });
  const id = list.items[0]?.id ?? "";

  // Pause: the switch turns it off and the row says it is paused, by whoever manages the AI.
  const toggle = row.getByRole("switch", { name: `Routine ${NAME} active` });
  await toggle.click();
  await expect(toggle).not.toBeChecked();
  await expect(row.getByTestId("routine-paused")).toHaveText("Paused");
  await expect(row.getByTestId("routine-next")).toHaveText(/^Paused by whoever manages /);
  await expect.poll(async () => (await stored(context, instance)).items[0]?.enabled).toBe(false);

  // Run now: a run due at once, waiting for the principal session (none is open).
  await row.getByRole("button", { name: `Run ${NAME} now` }).click();
  await expect(page.getByText(new RegExp(`“${NAME}” was sent to`))).toBeVisible();
  await expect(row.getByTestId("routine-last")).toHaveText(
    /^Last: .* · waiting for the principal session$/,
  );
  const runs = await apiCall<List<ScheduleRun>>(
    context.request,
    instance,
    "GET",
    `/agents/${agent.ai.id}/schedules/${id}/runs`,
  );
  expect(runs.items[0]).toMatchObject({ manual: true, status: "pending" });

  // History: read when it is opened, it shows that manual run, still waiting for the principal.
  const historyToggle = row.getByRole("button", { name: `History of ${NAME}` });
  await expect(historyToggle).toHaveAttribute("aria-expanded", "false");
  await historyToggle.click();
  const runList = row.getByRole("list", { name: `History of ${NAME}` });
  await expect(runList.getByRole("listitem")).toHaveCount(1);
  const manual = runList.getByTestId(`routine-run-${runs.items[0]?.id}`);
  await expect(manual.getByTestId("routine-run-status")).toHaveText(
    "waiting for the principal session",
  );
  await expect(manual.getByTestId("routine-run-manual")).toHaveText("Manual (Run now)");
  await expect(manual.getByTestId("routine-run-due")).toHaveText(/^Due (now|.* ago) · /);
  await expect(manual.getByTestId("routine-run-ack")).toHaveText(/ hasn’t received it yet$/);
  await expectNoSidewaysScroll(page, "the profile with a routine's history open");

  // Delete, after the question.
  await row.getByRole("button", { name: `Delete ${NAME}` }).click();
  const confirm = page.getByRole("alertdialog", { name: `Delete the routine “${NAME}”?` });
  await confirm.getByRole("button", { name: "Delete" }).click();
  await expect(confirm).toBeHidden();
  await expect(section.getByText(/No routines yet/)).toBeVisible();
  list = await stored(context, instance);
  expect(list.count).toBe(0);
});
