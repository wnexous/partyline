import { defineConfig } from "@playwright/test";

// Smoke suite against the built binary (`make e2e`). Each worker starts its own
// `partyline serve` on a free port with a temp data dir (e2e/fixtures.ts), so every
// project begins on a fresh instance that still needs setup.
//
// PLAYWRIGHT_CHROMIUM_EXECUTABLE points at an already installed Chromium (or headless
// shell) to skip `playwright install`; CI installs the bundled one instead.
const executablePath = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE || undefined;

export default defineConfig({
  testDir: "./e2e",
  globalSetup: "./e2e/global-setup.ts",
  forbidOnly: Boolean(process.env.CI),
  retries: 0,
  timeout: 60_000,
  expect: { timeout: 10_000 },
  reporter: process.env.CI ? [["list"], ["github"]] : "list",
  use: {
    browserName: "chromium",
    locale: "en-US",
    trace: "retain-on-failure",
    launchOptions: {
      executablePath,
      // A fake microphone, for the voice test that records with the browser's own MediaRecorder
      // (mobile.spec.ts): no permission prompt and a synthetic stream. Nothing else asks for one.
      args: ["--use-fake-ui-for-media-stream", "--use-fake-device-for-media-stream"],
    },
  },
  projects: [
    { name: "desktop-1280", use: { viewport: { width: 1280, height: 800 } } },
    { name: "mobile-360", use: { viewport: { width: 360, height: 780 } } },
  ],
});
