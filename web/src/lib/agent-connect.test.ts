import { describe, expect, it } from "vitest";
import {
  agentSnippets,
  CLAUDE_CODE_DOCS_URL,
  GO_INSTALL_COMMAND,
  LAUNCH_COMMAND,
  RELEASES_URL,
} from "@/lib/agent-connect";

const input = {
  origin: "https://partyline.example.test",
  token: "pl_agent_synthetic0123",
  name: "build-bot",
};

describe("agent connect snippets (UI-14)", () => {
  it("builds an .mcp.json entry that runs `partyline channel` with the URL, token and name", () => {
    const { mcpJson } = agentSnippets(input);
    expect(JSON.parse(mcpJson)).toEqual({
      mcpServers: {
        partyline: {
          command: "partyline",
          args: ["channel"],
          env: {
            PARTYLINE_URL: "https://partyline.example.test",
            PARTYLINE_TOKEN: "pl_agent_synthetic0123",
            PARTYLINE_AGENT: "build-bot",
          },
        },
      },
    });
  });

  it("loads the channel as a development channel when launching Claude Code", () => {
    expect(LAUNCH_COMMAND).toBe("claude --dangerously-load-development-channels server:partyline");
    expect(agentSnippets(input).launch).toBe(LAUNCH_COMMAND);
  });

  it("points at the install sources and the Claude Code docs page", () => {
    expect(GO_INSTALL_COMMAND).toBe("go install github.com/wnexous/partyline/cmd/partyline@latest");
    expect(RELEASES_URL).toBe("https://github.com/wnexous/partyline/releases");
    expect(CLAUDE_CODE_DOCS_URL).toBe(
      "https://github.com/wnexous/partyline/blob/main/docs/claude-code.md",
    );
  });

  it("drops a trailing slash from the origin", () => {
    const { mcpJson } = agentSnippets({ ...input, origin: "http://localhost:8080/" });
    expect(JSON.parse(mcpJson).mcpServers.partyline.env.PARTYLINE_URL).toBe(
      "http://localhost:8080",
    );
  });
});
