import { mkdir } from "node:fs/promises";
import { join } from "node:path";
import type { Page, TestInfo } from "@playwright/test";
import { expect, settled } from "./fixtures";

/**
 * Saves the page as `<name>-<viewport width>-<light|dark>.png` in E2E_SHOTS_DIR, once per theme,
 * when that variable is set (a way to look at a flow the suite already drives). Without it this
 * does nothing, so the suite stays as fast and as quiet as before.
 */
export async function shoot(page: Page, name: string, testInfo: TestInfo) {
  await capture(page, name, testInfo.project.use.viewport?.width ?? 1280, false);
}

/**
 * Saves the whole page, scrolled to the top, the way shoot does, named after the page's own window:
 * the phone fit check opens its own windows, narrower than the project's.
 */
export async function shootWhole(page: Page, name: string) {
  if (!process.env.E2E_SHOTS_DIR) return;
  await page.evaluate(() => window.scrollTo(0, 0));
  await capture(page, name, page.viewportSize()?.width ?? 1280, true);
}

async function capture(page: Page, name: string, width: number, fullPage: boolean) {
  const dir = process.env.E2E_SHOTS_DIR;
  if (!dir) return;
  await mkdir(dir, { recursive: true });
  try {
    for (const scheme of ["light", "dark"] as const) {
      await page.emulateMedia({ colorScheme: scheme });
      // The app follows the system theme by default and flips the `dark` class on <html>.
      await expect(page.locator("html")).toHaveClass(
        scheme === "dark" ? /\bdark\b/ : /^(?!.*\bdark\b)/,
      );
      // A dialog's own fade must have finished: the picture is of the settled state.
      await settled(page);
      await page.screenshot({ path: join(dir, `${name}-${width}-${scheme}.png`), fullPage });
    }
  } finally {
    await page.emulateMedia({ colorScheme: null });
  }
}
