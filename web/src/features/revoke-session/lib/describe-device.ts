import type { Translate } from "@/shared/i18n";

const BROWSERS: [RegExp, string][] = [
  [/Edg\//, "Edge"],
  [/Firefox\//, "Firefox"],
  [/Chrome\//, "Chrome"],
  [/Safari\//, "Safari"],
];
const SYSTEMS: [RegExp, string][] = [
  [/iPhone/, "iPhone"],
  [/iPad/, "iPad"],
  [/Android/, "Android"],
  [/Mac OS X|Macintosh/, "macOS"],
  [/Windows/, "Windows"],
  [/Linux|X11/, "Linux"],
];

/** "Chrome on Linux" from a user agent; good enough to recognise a device. */
export function describeDevice(t: Translate, userAgent: string): string {
  const browser = BROWSERS.find(([re]) => re.test(userAgent))?.[1];
  const os = SYSTEMS.find(([re]) => re.test(userAgent))?.[1];
  if (browser && os) return t("account.device", { browser, os });
  return browser ?? os ?? t("account.unknownDevice");
}
