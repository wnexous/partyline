import { screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { json, mockApi } from "@/test/fetch";
import { makeAI, makeIntelligence, makeMe, makeProfile } from "@/test/fixtures";
import { useLiveStore } from "@/test/live-store";
import { renderRoute } from "@/test/render";

/*
 * An AI whose principal session runs an outdated client (SESS-63): the profile header says so once,
 * and the session row warns with the sentence that tells the person to update.
 */

const at = "2026-10-28T14:05:00Z";

beforeEach(() => useLiveStore.setState({ locale: "en", presence: {} }));
afterEach(() => vi.unstubAllGlobals());

function outdatedProfile() {
  return makeProfile({
    ...makeAI(),
    ai: {
      owner: makeIntelligence(),
      folder: "/home/dev/atlas",
      folder_label: "~/atlas",
      app: "claude",
      online: true,
      sessions: [
        { label: "", harness: "claude", connected_at: at, outdated: true },
        { label: "pi-2", harness: "pi", connected_at: at },
      ],
      my_level: "use",
    },
    apps: [{ kind: "claude", label: "Claude Code" }],
  });
}

function stubProfile(extra: Parameters<typeof mockApi>[0] = {}) {
  return mockApi({
    "GET /me": json(200, makeMe()),
    "GET /conversations": json(200, { items: [] }),
    "GET /intelligences/a1": json(200, outdatedProfile()),
    ...extra,
  });
}

describe("an outdated principal session on the profile (SESS-63)", () => {
  it("says it once in the header and warns in the session row", async () => {
    stubProfile();
    renderRoute("/contacts/a1");
    await screen.findByRole("heading", { name: "Atlas", level: 1 });

    expect(await screen.findByTestId("profile-principal-outdated")).toHaveTextContent(
      "The principal session's client is outdated — run partyline update and reopen it",
    );
    expect(screen.getByTestId("session-outdated-principal")).toHaveTextContent(
      "Outdated client — run partyline update and reopen this session",
    );
    expect(screen.queryByTestId("session-outdated-pi-2")).toBeNull();
    expect(screen.getAllByTestId("profile-principal-outdated")).toHaveLength(1);
  });

  it("says nothing when every session runs a current client", async () => {
    const current = outdatedProfile();
    if (current.ai) current.ai.sessions = [{ label: "", harness: "claude", connected_at: at }];
    stubProfile({ "GET /intelligences/a1": json(200, current) });
    renderRoute("/contacts/a1");
    await screen.findByRole("heading", { name: "Atlas", level: 1 });
    expect(screen.queryByTestId("profile-principal-outdated")).toBeNull();
    expect(screen.queryByTestId("session-outdated-principal")).toBeNull();
  });
});
