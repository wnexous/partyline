import { type ChildProcess, spawn } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

export const repoRoot = resolve(fileURLToPath(new URL(".", import.meta.url)), "../..");

/** The binary under test: PARTYLINE_BIN, else the `make build` output. */
export function binaryPath() {
  return resolve(process.env.PARTYLINE_BIN ?? join(repoRoot, "bin/partyline"));
}

export interface Instance {
  /** Public URL, also the browser origin (`http://127.0.0.1:<port>`). */
  url: string;
  /** Setup token captured from the first-run WARN log line. */
  setupToken: string;
  stop: () => Promise<void>;
}

const START_TIMEOUT_MS = 30_000;

function freePort(): Promise<number> {
  return new Promise((done, fail) => {
    const srv = createServer();
    srv.once("error", fail);
    srv.listen(0, "127.0.0.1", () => {
      const addr = srv.address();
      srv.close(() =>
        typeof addr === "object" && addr ? done(addr.port) : fail(new Error("no port")),
      );
    });
  });
}

function stopProcess(proc: ChildProcess): Promise<void> {
  if (proc.exitCode !== null || proc.signalCode !== null) return Promise.resolve();
  return new Promise((done) => {
    const kill = setTimeout(() => proc.kill("SIGKILL"), 10_000);
    proc.once("exit", () => {
      clearTimeout(kill);
      done();
    });
    proc.kill("SIGTERM");
  });
}

/** Starts `partyline serve` on a free port with a fresh data dir and waits until it listens. */
export async function startInstance(): Promise<Instance> {
  const port = await freePort();
  const dataDir = await mkdtemp(join(tmpdir(), "partyline-e2e-"));
  const url = `http://127.0.0.1:${port}`;
  const proc = spawn(binaryPath(), ["serve"], {
    env: {
      ...process.env,
      PARTYLINE_ADDR: `127.0.0.1:${port}`,
      PARTYLINE_DATA_DIR: dataDir,
      PARTYLINE_PUBLIC_URL: url,
      PARTYLINE_LOG_FORMAT: "json",
      PARTYLINE_LOG_LEVEL: "info",
    },
    stdio: ["ignore", "ignore", "pipe"],
  });

  const stop = async () => {
    await stopProcess(proc);
    await rm(dataDir, { recursive: true, force: true });
  };

  try {
    const setupToken = await new Promise<string>((done, fail) => {
      let token = "";
      let buffered = "";
      const timer = setTimeout(
        () => fail(new Error("partyline serve did not start in time")),
        START_TIMEOUT_MS,
      );
      proc.once("error", fail);
      proc.once("exit", (code) => {
        clearTimeout(timer);
        fail(new Error(`partyline serve exited early with code ${code}`));
      });
      proc.stderr?.setEncoding("utf8");
      proc.stderr?.on("data", (chunk: string) => {
        buffered += chunk;
        const lines = buffered.split("\n");
        buffered = lines.pop() ?? "";
        for (const line of lines) {
          let entry: { msg?: string; setup_token?: string };
          try {
            entry = JSON.parse(line);
          } catch {
            continue;
          }
          if (entry.setup_token) token = entry.setup_token;
          if (entry.msg === "listening") {
            clearTimeout(timer);
            if (token) done(token);
            else fail(new Error("no setup_token in the server log"));
          }
        }
      });
    });
    return { url, setupToken, stop };
  } catch (err) {
    await stop();
    throw err;
  }
}
