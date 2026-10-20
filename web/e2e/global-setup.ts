import { execFileSync } from "node:child_process";
import { existsSync } from "node:fs";
import { binaryPath, repoRoot } from "./server";

/** Makes sure the partyline binary (with the embedded web app) exists; `make e2e` builds it first. */
export default function globalSetup() {
  const bin = binaryPath();
  if (existsSync(bin)) return;
  if (process.env.PARTYLINE_BIN) throw new Error(`PARTYLINE_BIN not found: ${bin}`);
  execFileSync("make", ["build"], { cwd: repoRoot, stdio: "inherit" });
}
