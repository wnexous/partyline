/*
 * What a Claude Code session needs to join partyline (UI-14, agent-channel AGT-01): the
 * `.mcp.json` server entry that runs `partyline channel` with the instance URL, an agent token
 * and the agent name, and the command that starts Claude Code with that development channel.
 */

export const LAUNCH_COMMAND = "claude --dangerously-load-development-channels server:partyline";
export const GO_INSTALL_COMMAND = "go install github.com/wnexous/partyline/cmd/partyline@latest";
export const RELEASES_URL = "https://github.com/wnexous/partyline/releases";
export const CLAUDE_CODE_DOCS_URL =
  "https://github.com/wnexous/partyline/blob/main/docs/claude-code.md";

export interface AgentSnippetInput {
  /** Instance URL as the browser sees it (`window.location.origin`). */
  origin: string;
  /** Agent token secret, shown once. */
  token: string;
  /** Agent name sent as PARTYLINE_AGENT. */
  name: string;
}

export function agentSnippets({ origin, token, name }: AgentSnippetInput) {
  const config = {
    mcpServers: {
      partyline: {
        command: "partyline",
        args: ["channel"],
        env: {
          PARTYLINE_URL: origin.replace(/\/+$/, ""),
          PARTYLINE_TOKEN: token,
          PARTYLINE_AGENT: name,
        },
      },
    },
  };
  // Keep short arrays on one line, the way people write .mcp.json by hand.
  const mcpJson = JSON.stringify(config, null, 2).replace(
    /\[\s+("[^"\n]*")\s+\]/g,
    (_, item: string) => `[${item}]`,
  );
  return { mcpJson, launch: LAUNCH_COMMAND };
}
