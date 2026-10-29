import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { apiError, json, mockApi } from "@/test/fetch";
import { makeContainer } from "@/test/fixtures";
import { useLiveStore } from "@/test/live-store";
import { renderWithProviders } from "@/test/render";
import { ContainerSection } from "./container-section";

/*
 * The container of an AI (CA-06): its state and desired state, its last error, start/stop/restart,
 * the last log lines and deleting it with or without the workspace. It renders for an AI the viewer
 * manages and that has a container; an AI in a terminal has none and the section is simply absent.
 */

const toast = vi.hoisted(() => Object.assign(vi.fn(), { error: vi.fn(), success: vi.fn() }));
vi.mock("sonner", () => ({ toast }));

const CONTAINER_PATH = "GET /agents/a1/container";

beforeEach(() => {
  useLiveStore.setState({ locale: "en" });
  toast.mockClear();
  toast.success.mockClear();
  toast.error.mockClear();
});

function render(routes: Parameters<typeof mockApi>[0] = {}, manages = true) {
  const api = mockApi({ [CONTAINER_PATH]: json(200, makeContainer()), ...routes });
  renderWithProviders(<ContainerSection agentId="a1" name="Atlas" manages={manages} />);
  return { ...api, user: userEvent.setup() };
}

describe("the container section (CA-06)", () => {
  it("shows the state the runner reported and the one the owner wants", async () => {
    render({
      [CONTAINER_PATH]: json(
        200,
        makeContainer({ state: "error", desired: "running", last_error: "no such image" }),
      ),
    });
    expect(await screen.findByRole("heading", { name: "Container" })).toBeInTheDocument();
    expect(screen.getByText("Error")).toBeInTheDocument();
    expect(screen.getByText("should be running")).toBeInTheDocument();
    expect(screen.getByText("no such image")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Start" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Stop" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Restart" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "View logs" })).toBeInTheDocument();
  });

  it("is absent for an AI without a container, and for one the viewer does not manage", async () => {
    const { calls } = render({ [CONTAINER_PATH]: apiError(404, "not_found") });
    await waitFor(() => expect(calls.some((c) => c.path === "/agents/a1/container")).toBe(true));
    expect(screen.queryByRole("heading", { name: "Container" })).toBeNull();

    vi.unstubAllGlobals();
    const second = render({}, false);
    await Promise.resolve();
    expect(second.calls.some((c) => c.path === "/agents/a1/container")).toBe(false);
    expect(screen.queryByRole("heading", { name: "Container" })).toBeNull();
  });
});

describe("operating the container (CA-06)", () => {
  it("starts it and shows the state the server answers with", async () => {
    const { calls, user } = render({
      "POST /agents/a1/container": json(
        200,
        makeContainer({ state: "running", desired: "running" }),
      ),
    });
    await screen.findByRole("heading", { name: "Container" });
    await user.click(screen.getByRole("button", { name: "Start" }));
    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ action: "start" });
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("Container started"));
  });

  it("stops and restarts it with their own actions", async () => {
    const { calls, user } = render({
      "POST /agents/a1/container": json(
        200,
        makeContainer({ state: "stopped", desired: "stopped" }),
      ),
    });
    await screen.findByRole("heading", { name: "Container" });
    await user.click(screen.getByRole("button", { name: "Stop" }));
    await user.click(screen.getByRole("button", { name: "Restart" }));
    await waitFor(() => expect(calls.filter((c) => c.method === "POST")).toHaveLength(2));
    expect(calls.filter((c) => c.method === "POST").map((c) => c.body)).toEqual([
      { action: "stop" },
      { action: "restart" },
    ]);
  });

  it("says a server that is offline", async () => {
    const { user } = render({
      "POST /agents/a1/container": apiError(409, "runner_offline", "the runner is offline"),
    });
    await screen.findByRole("heading", { name: "Container" });
    await user.click(screen.getByRole("button", { name: "Start" }));
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith(
        "The server is offline. Reconnect it and try again.",
      ),
    );
  });

  it("disables the actions while one runs, and says which one", async () => {
    let answer: () => void = () => {};
    const { calls, user } = render({
      "POST /agents/a1/container": () =>
        new Promise<Response>((resolve) => {
          answer = () => resolve(json(200, makeContainer()));
        }),
    });
    await screen.findByRole("heading", { name: "Container" });
    await user.click(screen.getByRole("button", { name: "Start" }));
    const pending = await screen.findByRole("button", { name: "Starting…" });
    expect(pending).toBeDisabled();
    expect(screen.getByRole("button", { name: "Stop" })).toBeDisabled();
    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
    answer();
    await waitFor(() => expect(screen.getByRole("button", { name: "Start" })).toBeEnabled());
  });
});

describe("the logs of the container (CA-06)", () => {
  it("reads the last 200 lines, shows them in monospace and reads again on refresh", async () => {
    const { calls, user } = render({
      "GET /agents/a1/container/logs": json(200, { logs: "line one\nline two" }),
    });
    await screen.findByRole("heading", { name: "Container" });
    await user.click(screen.getByRole("button", { name: "View logs" }));
    const sheet = await screen.findByRole("dialog", { name: "Last 200 log lines" });
    expect(within(sheet).getByText(/line one/).tagName).toBe("PRE");
    expect(within(sheet).getByText(/line one/)).toHaveClass("font-mono");
    expect(calls.find((c) => c.path.endsWith("/logs"))?.url.searchParams.get("tail")).toBe("200");
    await user.click(within(sheet).getByRole("button", { name: "Refresh logs" }));
    await waitFor(() => expect(calls.filter((c) => c.path.endsWith("/logs")).length).toBe(2), {
      timeout: 3000,
    });
  });

  it("says when there are no lines yet", async () => {
    const { user } = render({
      "GET /agents/a1/container/logs": json(200, { logs: "" }),
    });
    await screen.findByRole("heading", { name: "Container" });
    await user.click(screen.getByRole("button", { name: "View logs" }));
    expect(await screen.findByText("No log lines yet.")).toBeInTheDocument();
  });
});

describe("deleting the container (CA-06)", () => {
  it("asks first and removes the container only", async () => {
    let gone = false;
    const { calls, user } = render({
      [CONTAINER_PATH]: () => (gone ? apiError(404, "not_found") : json(200, makeContainer())),
      "DELETE /agents/a1/container": () => {
        gone = true;
        return json(204, undefined);
      },
    });
    await screen.findByRole("heading", { name: "Container" });
    await user.click(screen.getByRole("button", { name: "Delete container" }));
    const dialog = await screen.findByRole("alertdialog", {
      name: "Delete the container of Atlas?",
    });
    await user.click(within(dialog).getByRole("button", { name: "Delete container" }));
    await waitFor(() => expect(calls.some((c) => c.method === "DELETE")).toBe(true));
    expect(calls.find((c) => c.method === "DELETE")?.url.searchParams.get("volume")).toBe("false");
    await waitFor(() => expect(screen.queryByRole("heading", { name: "Container" })).toBeNull());
  });

  it("removes the workspace volume when the box is ticked", async () => {
    let gone = false;
    const { calls, user } = render({
      [CONTAINER_PATH]: () => (gone ? apiError(404, "not_found") : json(200, makeContainer())),
      "DELETE /agents/a1/container": () => {
        gone = true;
        return json(204, undefined);
      },
    });
    await screen.findByRole("heading", { name: "Container" });
    await user.click(screen.getByRole("button", { name: "Delete container" }));
    const dialog = await screen.findByRole("alertdialog", {
      name: "Delete the container of Atlas?",
    });
    await user.click(
      within(dialog).getByRole("checkbox", { name: "Also remove the workspace volume" }),
    );
    await user.click(within(dialog).getByRole("button", { name: "Delete container" }));
    await waitFor(() => expect(calls.some((c) => c.method === "DELETE")).toBe(true));
    expect(calls.find((c) => c.method === "DELETE")?.url.searchParams.get("volume")).toBe("true");
  });
});
