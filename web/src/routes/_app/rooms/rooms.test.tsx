import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useLiveStore } from "@/lib/live/store";
import { apiError, json, mockApi } from "@/test/fetch";
import { makeMe, makeRoom } from "@/test/fixtures";
import { renderRoute } from "@/test/render";

vi.mock("@/lib/live", () => ({ startLive: vi.fn(() => () => {}) }));

const me = makeMe();

beforeEach(() => useLiveStore.setState({ locale: "en" }));
afterEach(() => vi.unstubAllGlobals());

describe("create room dialog", () => {
  it("posts the trimmed name and description, then opens the new room", async () => {
    const created = makeRoom({ id: "r9", name: "night-shift" });
    const { calls } = mockApi({
      "GET /me": json(200, me),
      "GET /conversations": json(200, { items: [] }),
      "POST /rooms": json(201, created),
      "GET /rooms/r9": json(200, created),
      "GET /rooms/r9/messages": json(200, { items: [], has_more: false }),
      "GET /rooms/r9/members": json(200, { items: [] }),
    });
    const user = userEvent.setup();
    const { router } = renderRoute("/");

    await user.click(
      (await screen.findAllByRole("button", { name: "New room" }))[0] as HTMLElement,
    );
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("Name"), "  night-shift ");
    await user.type(within(dialog).getByLabelText(/Description/), "on-call agents");
    await user.click(within(dialog).getByRole("button", { name: "Create room" }));

    await waitFor(() => expect(router.state.location.pathname).toBe("/rooms/r9"));
    expect(calls.find((c) => c.method === "POST" && c.path === "/rooms")?.body).toEqual({
      name: "night-shift",
      description: "on-call agents",
    });
  });

  it("requires a name before posting", async () => {
    const { calls } = mockApi({
      "GET /me": json(200, me),
      "GET /conversations": json(200, { items: [] }),
    });
    const user = userEvent.setup();
    renderRoute("/");
    await user.click(
      (await screen.findAllByRole("button", { name: "New room" }))[0] as HTMLElement,
    );
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: "Create room" }));
    expect(await within(dialog).findByText("Required")).toBeInTheDocument();
    expect(calls.some((c) => c.method === "POST")).toBe(false);
  });

  it("shows a 422 reason under the name field", async () => {
    mockApi({
      "GET /me": json(200, me),
      "GET /conversations": json(200, { items: [] }),
      "POST /rooms": apiError(422, "invalid", "validation failed", { name: "too long" }),
    });
    const user = userEvent.setup();
    renderRoute("/");
    await user.click(
      (await screen.findAllByRole("button", { name: "New room" }))[0] as HTMLElement,
    );
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("Name"), "x");
    await user.click(within(dialog).getByRole("button", { name: "Create room" }));
    expect(await within(dialog).findByText("too long")).toBeInTheDocument();
  });
});
