import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { CreatedToken, Token } from "@/lib/api/types.gen";
import { useLiveStore } from "@/lib/live/store";
import { apiError, json, mockApi } from "@/test/fetch";
import { makeMe } from "@/test/fixtures";
import { renderRoute } from "@/test/render";

vi.mock("@/lib/live", () => ({ startLive: vi.fn(() => () => {}) }));

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
    renderRoute("/tokens");
    await screen.findByRole("listitem", { name: "ci deploy script" });
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
