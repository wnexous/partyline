import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Runner } from "@/shared/api/types.gen";
import { apiError, json, mockApi } from "@/test/fetch";
import { makeRunner } from "@/test/fixtures";
import { useLiveStore } from "@/test/live-store";
import { renderWithProviders } from "@/test/render";
import { ServersSection } from "./servers-section";

/*
 * "Servers" in the account settings (CA-01, CA-02): one row per machine with its online state,
 * Docker and container count, renaming in place, removing after a confirmation (a server that
 * still runs containers is refused with its own message) and the two commands that add one.
 */

const devbox = makeRunner();
const laptop = makeRunner({
  id: "r2",
  name: "notebook",
  machine: "notebook.local",
  online: false,
  docker_version: undefined,
  docker_error: "docker: command not found",
  last_seen_at: "2026-10-28T09:00:00Z",
  container_count: 2,
});

const list = (items: Runner[]) => json(200, { items });

beforeEach(() => useLiveStore.setState({ locale: "en" }));
afterEach(() => vi.unstubAllGlobals());

/** A read that follows what the tests change on the server (a rename, a removal). */
function render(routes: Parameters<typeof mockApi>[0] = {}) {
  const rows: Runner[] = [devbox, laptop];
  const api = mockApi({ "GET /runners": () => list(rows), ...routes });
  renderWithProviders(<ServersSection />);
  return { ...api, rows, user: userEvent.setup() };
}

describe("the servers list", () => {
  it("shows each server with its state, Docker and container count", async () => {
    render();
    expect(await screen.findByText("devbox")).toBeInTheDocument();
    expect(screen.getByText("Online")).toBeInTheDocument();
    expect(screen.getByText("devbox.local")).toBeInTheDocument();
    expect(screen.getByText("Docker 27.3.1")).toBeInTheDocument();
    expect(screen.getByText("No AI runs here yet")).toBeInTheDocument();

    const offline = screen.getByTestId("runner-r2");
    expect(within(offline).getByText("Offline")).toBeInTheDocument();
    expect(within(offline).getByText("notebook.local")).toBeInTheDocument();
    // Why Docker cannot be used is the important line of a server that cannot run anything.
    expect(
      within(offline).getByText(/Docker unusable: docker: command not found/),
    ).toBeInTheDocument();
    expect(within(offline).getByText("2 container AIs")).toBeInTheDocument();
    expect(within(offline).getByText(/last seen/)).toBeInTheDocument();
  });

  it("says where the servers come from while there are none", async () => {
    mockApi({ "GET /runners": list([]) });
    renderWithProviders(<ServersSection />);
    expect(await screen.findByText("No servers yet")).toBeInTheDocument();
    expect(
      screen.getByText(
        "Run the two commands below on the machine with Docker and it shows up here.",
      ),
    ).toBeInTheDocument();
  });

  it("gives the two commands that add a server and the check hint", async () => {
    render();
    await screen.findByText("devbox");
    expect(screen.getByRole("figure", { name: "Run the runner" })).toHaveTextContent(
      "partyline runner",
    );
    expect(
      screen.getByRole("figure", { name: "Keep it running after a reboot" }),
    ).toHaveTextContent("partyline runner install");
    expect(screen.getByText(/partyline runner --check/)).toBeInTheDocument();
  });

  it("tells a failed read instead of an empty list", async () => {
    render({ "GET /runners": apiError(500, "internal") });
    expect(await screen.findByText("Couldn’t load your servers.")).toBeInTheDocument();
  });
});

describe("renaming a server (CA-01)", () => {
  it("saves the new name in place and shows it", async () => {
    const { calls, rows, user } = render({
      "PATCH /runners/r1": () => {
        rows[0] = { ...devbox, name: "build box" };
        return json(200, rows[0]);
      },
    });
    await screen.findByText("devbox");
    await user.click(screen.getByRole("button", { name: "Rename devbox" }));
    const field = screen.getByRole("textbox", { name: "Rename devbox" });
    await user.clear(field);
    await user.type(field, "build box");
    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(calls.filter((c) => c.method === "PATCH")).toHaveLength(1));
    expect(calls.find((c) => c.method === "PATCH")?.body).toEqual({ name: "build box" });
    expect(await screen.findByText("build box")).toBeInTheDocument();
  });

  it("says when another server already has the name", async () => {
    const { user } = render({
      "PATCH /runners/r1": apiError(409, "conflict", "another runner already has this name"),
    });
    await screen.findByText("devbox");
    await user.click(screen.getByRole("button", { name: "Rename devbox" }));
    const field = screen.getByRole("textbox", { name: "Rename devbox" });
    await user.clear(field);
    await user.type(field, "notebook");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(
      await screen.findByText("Another of your servers already has that name."),
    ).toBeInTheDocument();
  });
});

describe("removing a server (CA-01)", () => {
  it("asks first, then removes it and its row", async () => {
    const { calls, rows, user } = render({
      "DELETE /runners/r1": () => {
        rows.splice(0, 1);
        return json(204, undefined);
      },
    });
    await screen.findByText("devbox");
    await user.click(screen.getByRole("button", { name: "Remove devbox" }));
    const dialog = await screen.findByRole("alertdialog", { name: "Remove devbox?" });
    await user.click(within(dialog).getByRole("button", { name: "Remove server" }));
    await waitFor(() => expect(calls.some((c) => c.method === "DELETE")).toBe(true));
    await waitFor(() => expect(screen.queryByText("devbox")).toBeNull());
  });

  it("shows the server that still runs containers and keeps the dialog open", async () => {
    const { user } = render({
      "DELETE /runners/r1": apiError(
        409,
        "runner_in_use",
        "the runner still runs containers: delete them first",
      ),
    });
    await screen.findByText("devbox");
    await user.click(screen.getByRole("button", { name: "Remove devbox" }));
    const dialog = await screen.findByRole("alertdialog", { name: "Remove devbox?" });
    await user.click(within(dialog).getByRole("button", { name: "Remove server" }));
    expect(
      await within(dialog).findByText(
        "This server still runs container AIs. Delete their containers first.",
      ),
    ).toBeInTheDocument();
    expect(screen.getByRole("alertdialog", { name: "Remove devbox?" })).toBeInTheDocument();
  });
});
