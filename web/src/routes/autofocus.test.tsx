import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { json, mockApi } from "@/test/fetch";
import { makeContacts, makeMe } from "@/test/fixtures";
import { useLiveStore } from "@/test/live-store";
import { stubPointer } from "@/test/media";
import { renderRoute } from "@/test/render";

vi.mock("@/app/live", () => ({ startLive: vi.fn(() => () => {}) }));

// UI-24: on phones, focusing a field on mount pops the on-screen keyboard over the form.
// Fields focus themselves only with a fine pointer (mouse, trackpad).

beforeEach(() => useLiveStore.setState({ locale: "en" }));
afterEach(() => vi.unstubAllGlobals());

const loginApi = () =>
  mockApi({
    "GET /me": json(401, { error: { code: "unauthorized", message: "unauthorized" } }),
    "GET /setup/status": json(200, { needs_setup: false }),
  });

const sidebarApi = () =>
  mockApi({
    "GET /me": json(200, makeMe()),
    "GET /conversations": json(200, { items: [] }),
    "GET /contacts": json(200, makeContacts()),
  });

async function openDialog(name: "New group" | "New conversation") {
  const user = userEvent.setup();
  await user.click((await screen.findAllByRole("button", { name }))[0] as HTMLElement);
  return screen.findByRole("dialog", { name });
}

describe("autofocus by pointer (UI-24)", () => {
  it("focuses the login username with a fine pointer", async () => {
    stubPointer("fine");
    loginApi();
    renderRoute("/login");
    const username = await screen.findByLabelText("Username");
    await waitFor(() => expect(username).toHaveFocus());
  });

  it("leaves the login form unfocused on a coarse pointer", async () => {
    stubPointer("coarse");
    loginApi();
    renderRoute("/login");
    const username = await screen.findByLabelText("Username");
    expect(username).not.toHaveFocus();
    expect(screen.getByLabelText("Password")).not.toHaveFocus();
  });

  it("focuses the first dialog field with a fine pointer", async () => {
    stubPointer("fine");
    sidebarApi();
    renderRoute("/");
    const dialog = await openDialog("New group");
    await waitFor(() => expect(within(dialog).getByLabelText("Name")).toHaveFocus());
  });

  it("focuses the dialog itself, not its first field, on a coarse pointer", async () => {
    stubPointer("coarse");
    sidebarApi();
    renderRoute("/");
    const dialog = await openDialog("New group");
    await waitFor(() => expect(dialog).toHaveFocus());
    expect(within(dialog).getByLabelText("Name")).not.toHaveFocus();
  });

  it("focuses the contact search with a fine pointer", async () => {
    stubPointer("fine");
    sidebarApi();
    renderRoute("/");
    const dialog = await openDialog("New conversation");
    await waitFor(() =>
      expect(within(dialog).getByRole("combobox", { name: "Search contacts" })).toHaveFocus(),
    );
  });

  it("focuses the new conversation dialog itself, not its search, on a coarse pointer", async () => {
    stubPointer("coarse");
    sidebarApi();
    renderRoute("/");
    const dialog = await openDialog("New conversation");
    await waitFor(() => expect(dialog).toHaveFocus());
    expect(within(dialog).getByRole("combobox", { name: "Search contacts" })).not.toHaveFocus();
  });
});
