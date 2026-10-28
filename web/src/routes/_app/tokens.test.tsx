import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { CreatedToken, Token } from "@/shared/api/types.gen";
import { apiError, json, mockApi } from "@/test/fetch";
import { makeMe } from "@/test/fixtures";
import { useLiveStore } from "@/test/live-store";
import { renderRoute } from "@/test/render";

vi.mock("@/app/live", () => ({ startLive: vi.fn(() => () => {}) }));

const me = makeMe();
const BASE = Date.parse("2026-10-19T12:00:00Z");
const DAY = 86_400_000;
const SECRET = "pl_user_Q8ztSyntheticSecret0123456789";

const active: Token = {
  id: "t1",
  kind: "user",
  name: "ci deploy script",
  prefix: "pl_user_Q8zt",
  created_at: "2026-10-01T12:00:00Z",
  last_used_at: "2026-10-19T11:00:00Z",
};
const revoked: Token = {
  id: "t2",
  kind: "agent",
  name: "old laptop",
  prefix: "pl_agent_Mm1c",
  created_at: "2026-09-01T12:00:00Z",
  revoked_at: "2026-09-20T12:00:00Z",
};

beforeEach(() => {
  window.localStorage.clear();
  useLiveStore.setState({ locale: "en" });
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(BASE);
});
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

const row = (name: string) => screen.getByRole("listitem", { name });

describe("tokens page (UI-19)", () => {
  it("lists tokens by prefix with kind and state, revoked ones without a revoke button", async () => {
    mockApi({ "GET /me": json(200, me), "GET /tokens": json(200, { items: [active, revoked] }) });
    const user = userEvent.setup();
    renderRoute("/tokens");
    await screen.findByRole("listitem", { name: "ci deploy script" });
    // Revoked tokens are hidden by default (TOKH-04).
    await user.click(screen.getByRole("switch", { name: /Show revoked/ }));
    expect(within(row("ci deploy script")).getByText(/pl_user_Q8zt/)).toBeInTheDocument();
    expect(within(row("ci deploy script")).getByText("User")).toBeInTheDocument();
    expect(within(row("old laptop")).getByText("Agent")).toBeInTheDocument();
    expect(within(row("old laptop")).getByText("Revoked")).toBeInTheDocument();
    expect(
      within(row("ci deploy script")).getByRole("button", { name: "Revoke ci deploy script" }),
    ).toBeInTheDocument();
    expect(within(row("old laptop")).queryByRole("button", { name: /Revoke/ })).toBeNull();
  });

  it("creates a token and shows the secret once with a warning, then only the prefix", async () => {
    let items: Token[] = [];
    const created: CreatedToken = {
      token: { ...active, id: "t9", name: "ci", created_at: new Date(BASE).toISOString() },
      secret: SECRET,
    };
    const { calls } = mockApi({
      "GET /me": json(200, me),
      "GET /tokens": () => json(200, { items }),
      "POST /tokens": () => {
        items = [created.token];
        return json(201, created);
      },
    });
    const user = userEvent.setup();
    renderRoute("/tokens");
    await user.click(await screen.findByRole("button", { name: "New token" }));
    const dialog = await screen.findByRole("dialog", { name: "New token" });
    await user.click(within(dialog).getByRole("radio", { name: /User/ }));
    await user.type(within(dialog).getByLabelText("Name"), "ci");
    await user.click(within(dialog).getByRole("radio", { name: "30 days" }));
    await user.click(within(dialog).getByRole("button", { name: "Create token" }));

    const secret = await screen.findByRole("dialog", { name: "Copy your new token" });
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({
      kind: "user",
      name: "ci",
      expires_at: new Date(BASE + 30 * DAY).toISOString(),
    });
    expect(
      within(secret).getByRole("figure", { name: "ci" }).querySelector("pre")?.textContent,
    ).toBe(SECRET);
    expect(within(secret).getByText(/won’t be shown again/)).toBeInTheDocument();
    await user.click(within(secret).getByRole("button", { name: "Copy ci" }));
    expect(await navigator.clipboard.readText()).toBe(SECRET);

    await user.click(within(secret).getByRole("button", { name: "I copied it" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(await screen.findByRole("listitem", { name: "ci" })).toHaveTextContent("pl_user_Q8zt");
    expect(document.body.textContent).not.toContain(SECRET);
  });

  it("creates a token that never expires by default", async () => {
    const { calls } = mockApi({
      "GET /me": json(200, me),
      "GET /tokens": json(200, { items: [active] }),
      "POST /tokens": json(201, { token: active, secret: SECRET }),
    });
    const user = userEvent.setup();
    renderRoute("/tokens");
    await user.click(await screen.findByRole("button", { name: "New token" }));
    const dialog = await screen.findByRole("dialog", { name: "New token" });
    await user.type(within(dialog).getByLabelText("Name"), "laptop session");
    await user.click(within(dialog).getByRole("button", { name: "Create token" }));
    await screen.findByRole("dialog", { name: "Copy your new token" });
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({
      kind: "agent",
      name: "laptop session",
    });
  });

  it("requires a name and shows 422 reasons", async () => {
    const { calls } = mockApi({
      "GET /me": json(200, me),
      "GET /tokens": json(200, { items: [active] }),
      "POST /tokens": apiError(422, "invalid", "validation failed", {
        name: "must have 1 to 64 characters",
      }),
    });
    const user = userEvent.setup();
    renderRoute("/tokens");
    await user.click(await screen.findByRole("button", { name: "New token" }));
    const dialog = await screen.findByRole("dialog", { name: "New token" });
    await user.click(within(dialog).getByRole("button", { name: "Create token" }));
    expect(await within(dialog).findByText("Required")).toBeInTheDocument();
    expect(calls.some((c) => c.method === "POST")).toBe(false);
    await user.type(within(dialog).getByLabelText("Name"), "x");
    await user.click(within(dialog).getByRole("button", { name: "Create token" }));
    expect(await within(dialog).findByText("must have 1 to 64 characters")).toBeInTheDocument();
  });

  it("revokes a token after confirmation", async () => {
    const { calls } = mockApi({
      "GET /me": json(200, me),
      "GET /tokens": json(200, { items: [active] }),
      "DELETE /tokens/t1": new Response(null, { status: 204 }),
    });
    const user = userEvent.setup();
    renderRoute("/tokens");
    await user.click(await screen.findByRole("button", { name: "Revoke ci deploy script" }));
    const confirm = await screen.findByRole("alertdialog");
    await user.click(within(confirm).getByRole("button", { name: "Revoke token" }));
    await waitFor(() =>
      expect(calls.some((c) => c.method === "DELETE" && c.path === "/tokens/t1")).toBe(true),
    );
  });
});

describe("revoked tokens (TOKH-04)", () => {
  const expired: Token = {
    id: "t3",
    kind: "user",
    name: "old export",
    prefix: "pl_user_Xx9a",
    created_at: "2026-08-01T12:00:00Z",
    expires_at: "2026-09-01T12:00:00Z",
  };

  it("hides revoked tokens by default and shows them with the remembered toggle", async () => {
    const { calls } = mockApi({
      "GET /me": json(200, me),
      "GET /tokens": json(200, { items: [active, revoked, expired] }),
    });
    const user = userEvent.setup();
    const first = renderRoute("/tokens");
    await screen.findByRole("listitem", { name: "ci deploy script" });
    expect(calls.find((c) => c.path === "/tokens")?.url.searchParams.get("include_revoked")).toBe(
      "true",
    );
    expect(screen.queryByRole("listitem", { name: "old laptop" })).toBeNull();
    // Expired but not revoked stays listed.
    expect(row("old export")).toHaveTextContent("Expired");

    const toggle = screen.getByRole("switch", { name: /Show revoked/ });
    expect(toggle).not.toBeChecked();
    expect(toggle.closest("div")).toHaveTextContent("1");
    await user.click(toggle);
    expect(await screen.findByRole("listitem", { name: "old laptop" })).toBeInTheDocument();
    expect(window.localStorage.getItem("partyline.tokens.showRevoked")).toBe("1");
    // Delete is offered only on revoked rows: an expired token that was never revoked has none
    // (the server answers 409 for it).
    expect(within(row("old laptop")).getByRole("button", { name: /Delete/ })).toBeInTheDocument();
    expect(within(row("old export")).queryByRole("button", { name: /Delete/ })).toBeNull();

    // The choice is remembered for this browser.
    first.unmount();
    renderRoute("/tokens");
    expect(await screen.findByRole("listitem", { name: "old laptop" })).toBeInTheDocument();
    expect(screen.getByRole("switch", { name: /Show revoked/ })).toBeChecked();
    await user.click(screen.getByRole("switch", { name: /Show revoked/ }));
    await waitFor(() => expect(screen.queryByRole("listitem", { name: "old laptop" })).toBeNull());
    expect(window.localStorage.getItem("partyline.tokens.showRevoked")).toBeNull();
  });

  it("offers no toggle when nothing is revoked", async () => {
    mockApi({ "GET /me": json(200, me), "GET /tokens": json(200, { items: [active] }) });
    renderRoute("/tokens");
    await screen.findByRole("listitem", { name: "ci deploy script" });
    expect(screen.queryByRole("switch", { name: /Show revoked/ })).toBeNull();
    expect(screen.queryByRole("button", { name: /^Delete/ })).toBeNull();
  });

  it("deletes a revoked token after confirmation and removes its row", async () => {
    window.localStorage.setItem("partyline.tokens.showRevoked", "1");
    let items = [active, revoked];
    const { calls } = mockApi({
      "GET /me": json(200, me),
      "GET /tokens": () => json(200, { items }),
      "DELETE /tokens/t2/purge": () => {
        items = [active];
        return new Response(null, { status: 204 });
      },
    });
    const user = userEvent.setup();
    renderRoute("/tokens");
    await screen.findByRole("listitem", { name: "old laptop" });
    // Only revoked rows can be deleted.
    expect(within(row("ci deploy script")).queryByRole("button", { name: /Delete/ })).toBeNull();

    await user.click(within(row("old laptop")).getByRole("button", { name: "Delete old laptop" }));
    const confirm = await screen.findByRole("alertdialog", { name: "Delete old laptop?" });
    await user.click(within(confirm).getByRole("button", { name: "Delete token" }));
    await waitFor(() => expect(screen.queryByRole("listitem", { name: "old laptop" })).toBeNull());
    expect(calls.filter((c) => c.method === "DELETE").map((c) => c.path)).toEqual([
      "/tokens/t2/purge",
    ]);
    expect(screen.queryByRole("alertdialog")).toBeNull();
    expect(row("ci deploy script")).toBeInTheDocument();
  });

  it("keeps the row and says so when the delete fails", async () => {
    window.localStorage.setItem("partyline.tokens.showRevoked", "1");
    mockApi({
      "GET /me": json(200, me),
      "GET /tokens": json(200, { items: [active, revoked] }),
      "DELETE /tokens/t2/purge": apiError(409, "conflict"),
    });
    const user = userEvent.setup();
    renderRoute("/tokens");
    await user.click(await screen.findByRole("button", { name: "Delete old laptop" }));
    const confirm = await screen.findByRole("alertdialog");
    await user.click(within(confirm).getByRole("button", { name: "Delete token" }));
    expect(await screen.findByText("Couldn’t delete the token.")).toBeInTheDocument();
    // The confirmation stays open over the list, which still has the row.
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
    expect(screen.getByRole("listitem", { name: "old laptop", hidden: true })).toBeInTheDocument();
  });
});
