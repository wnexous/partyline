/*
 * What a machine needs to join partyline (WIZ-01, harness-onboarding): the installer served by
 * this instance, `partyline setup --browser` (approve the machine here, once; it saves the
 * credentials in the config file and registers partyline in every harness it finds), and the
 * launcher that starts each harness, `partyline <id>` (HAR-03). The manual entries under
 * "Advanced" never carry a token: the partyline server reads the URL and token from the config
 * file.
 */

export const SETUP_COMMAND = "partyline setup --browser";
/** Where `partyline setup` saves the instance URL and agent token (mode 0600). */
export const CONFIG_FILE = "~/.config/partyline/config.json";
export const RELEASES_URL = "https://github.com/wnexous/partyline/releases";
/** The guide for every harness; Claude Code's channel has its own section. */
export const AGENTS_DOCS_URL = "https://github.com/wnexous/partyline/blob/main/docs/agents.md";
export const CLAUDE_CODE_DOCS_URL = `${AGENTS_DOCS_URL}#claude-code`;

/** `curl … | sh` against this instance's `/install.sh` (ONB-03 pre-fills the instance URL). */
export function installCommand(origin: string): string {
  return `curl -fsSL ${origin.replace(/\/+$/, "")}/install.sh | sh`;
}

export type HarnessId = "claude" | "opencode" | "pi" | "omp" | "codex" | "gemini" | "antigravity";

/** push: messages reach the running session; turn: at turn end or through wait_for_messages. */
export type Delivery = "push" | "turn";

export interface Harness {
  id: HarnessId;
  title: string;
  /** Command that starts a session in a project folder: the launcher `partyline <id>`. */
  start: string;
  delivery: Delivery;
  /** Manual registration: the file to edit and the entry to merge into it. */
  manual: { file: string; entry: string };
}

const json = (value: unknown) =>
  // Keep short arrays on one line, the way people write these files by hand.
  JSON.stringify(value, null, 2).replace(
    /\[\s+((?:"[^"\n]*",?\s*)+)\]/g,
    (_, items: string) => `[${items.trim().split(/,\s*/).join(", ")}]`,
  );

const channel = ["channel"];
const toolsOnly = ["channel", "--tools-only"];
/** omp runs the full channel; its extension frames the messages as <partyline …>. */
const ompChannel = ["channel", "--harness", "omp"];

export const HARNESSES: readonly Harness[] = [
  {
    id: "claude",
    title: "Claude Code",
    start: "partyline claude",
    delivery: "push",
    manual: {
      file: ".mcp.json",
      entry: json({ mcpServers: { partyline: { command: "partyline", args: channel } } }),
    },
  },
  {
    id: "opencode",
    title: "OpenCode",
    start: "partyline opencode",
    delivery: "push",
    manual: {
      file: "~/.config/opencode/opencode.json",
      entry: json({
        mcp: {
          partyline: { type: "local", command: ["partyline", ...toolsOnly], enabled: true },
        },
      }),
    },
  },
  {
    id: "pi",
    title: "pi",
    start: "partyline pi",
    delivery: "push",
    manual: {
      file: "~/.pi/agent/mcp.json",
      entry: json({ mcpServers: { partyline: { command: "partyline", args: toolsOnly } } }),
    },
  },
  {
    id: "omp",
    title: "omp",
    start: "partyline omp",
    delivery: "push",
    manual: {
      file: "~/.omp/agent/mcp.json",
      entry: json({ mcpServers: { partyline: { command: "partyline", args: ompChannel } } }),
    },
  },
  {
    id: "codex",
    title: "Codex",
    start: "partyline codex",
    delivery: "turn",
    manual: {
      file: "~/.codex/config.toml",
      entry: '[mcp_servers.partyline]\ncommand = "partyline"\nargs = ["channel", "--tools-only"]',
    },
  },
  {
    id: "gemini",
    title: "Gemini",
    start: "partyline gemini",
    delivery: "turn",
    manual: {
      file: "~/.gemini/settings.json",
      entry: json({ mcpServers: { partyline: { command: "partyline", args: toolsOnly } } }),
    },
  },
  {
    id: "antigravity",
    title: "Antigravity",
    start: "partyline agy",
    delivery: "turn",
    manual: {
      file: "~/.gemini/config/mcp_config.json",
      entry: json({ mcpServers: { partyline: { command: "partyline", args: toolsOnly } } }),
    },
  },
];
