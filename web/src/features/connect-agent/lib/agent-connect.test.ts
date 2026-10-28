import { describe, expect, it } from "vitest";
import {
  CONFIG_FILE,
  HARNESSES,
  installCommand,
  RELEASES_URL,
  SETUP_COMMAND,
} from "./agent-connect";

describe("agent connect commands (WIZ-01)", () => {
  it("installs from this instance's /install.sh", () => {
    expect(installCommand("https://partyline.example.test")).toBe(
      "curl -fsSL https://partyline.example.test/install.sh | sh",
    );
    expect(installCommand("http://localhost:8080/")).toBe(
      "curl -fsSL http://localhost:8080/install.sh | sh",
    );
  });

  it("sets the machine up through browser approval and points at the config file", () => {
    expect(SETUP_COMMAND).toBe("partyline setup --browser");
    expect(CONFIG_FILE).toBe("~/.config/partyline/config.json");
    expect(RELEASES_URL).toBe("https://github.com/wnexous/partyline/releases");
  });

  it("lists the four supported harnesses with their start command (AD-035)", () => {
    expect(HARNESSES.map((h) => [h.title, h.start])).toEqual([
      ["Claude Code", "partyline claude"],
      ["OpenCode", "partyline opencode"],
      ["pi", "partyline pi"],
      ["omp", "partyline omp"],
    ]);
  });
});
