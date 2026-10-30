import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { apiError, json, mockApi } from "@/test/fetch";
import { makeAI, makeRunner, makeSecret } from "@/test/fixtures";
import { useLiveStore } from "@/test/live-store";
import { renderWithRouter } from "@/test/render";
import { NewContainerAgentDialog } from "./new-container-agent-dialog";

/*
 * "New AI in a container" (CA-04, CA-10): the server (only a connected one can be picked), the
 * app, the profile of the new AI, an optional repository, the owner's secrets as environment
 * variables with the suggestion of the chosen app, and the limits. On success the new AI's profile
 * opens; a 409/422 shows the shared error text.
 */

const toast = vi.hoisted(() => Object.assign(vi.fn(), { error: vi.fn(), success: vi.fn() }));
vi.mock("sonner", () => ({ toast }));

const devbox = makeRunner();
const offline = makeRunner({
  id: "r2",
  name: "notebook",
  machine: "notebook.local",
  online: false,
});
const secret = makeSecret();
const created = makeContainerFor("a9");

function makeContainerFor(id: string) {
  return {
    agent: makeAI({ id, display_name: "Atlas", handle: "atlas" }),
    runner_id: "r1",
    harness: "claude",
    image: "ghcr.io/wnexous/partyline-agent:latest",
    volume: "pl-000000000009-ws",
    container_name: "pl-000000000009",
    env: [],
    cpus: 1,
    memory_mb: 2048,
    desired: "running",
    state: "running",
    created_at: "2026-10-29T12:00:00Z",
    updated_at: "2026-10-29T12:00:00Z",
  };
}

beforeEach(() => {
  useLiveStore.setState({ locale: "en" });
  toast.mockClear();
  toast.success.mockClear();
  toast.error.mockClear();
});

function render(routes: Parameters<typeof mockApi>[0] = {}) {
  const api = mockApi({
    "GET /runners": json(200, { items: [devbox, offline] }),
    "GET /secrets": json(200, { items: [secret] }),
    "POST /container-agents": json(201, created),
    ...routes,
  });
  const view = renderWithRouter(<NewContainerAgentDialog open onOpenChange={vi.fn()} />);
  return { ...api, ...view, user: userEvent.setup() };
}

const name = () => screen.getByRole("textbox", { name: "Name" });
const handle = () => screen.getByRole("textbox", { name: "Handle" });
const submit = () => screen.getByRole("button", { name: "Create AI" });
// A create without a credential only restart-loops, so every valid form maps one first.
const mapCredential = async (user: ReturnType<typeof userEvent.setup>) =>
  user.click(screen.getByRole("button", { name: "Map a secret" }));
const posted = (calls: { method: string; path: string; body: unknown }[]) =>
  calls.filter((c) => c.method === "POST" && c.path === "/container-agents");

describe("the new container AI form (CA-04)", () => {
  it("offers the connected server and the app, with the limits every container starts with", async () => {
    const { user } = render();
    const dialog = await screen.findByRole("dialog", { name: "New AI in a container" });
    const runner = within(dialog).getByRole("combobox", { name: "Server" });
    await waitFor(() => expect(runner).toHaveValue("r1"));
    expect(within(runner).getByRole("option", { name: /notebook \(offline\)/ })).toBeDisabled();
    expect(within(dialog).getByRole("combobox", { name: "App" })).toHaveValue("claude");
    expect(within(dialog).getByRole("combobox", { name: "CPU" })).toHaveValue("1");
    expect(within(dialog).getByRole("combobox", { name: "Memory" })).toHaveValue("2048");
    expect(within(dialog).getByRole("textbox", { name: "Name" })).toBeInTheDocument();
    expect(within(dialog).getByRole("textbox", { name: "Handle" })).toBeInTheDocument();
    // The repository is optional, and the form asks for a credential before it can be sent.
    expect(within(dialog).getByRole("textbox", { name: /Repository URL/ })).toHaveValue("");
    expect(within(dialog).queryByRole("textbox", { name: "Variable" })).toBeNull();
    expect(submit()).toBeDisabled();
    expect(
      within(dialog).getByText("Map at least one credential before creating the AI."),
    ).toBeInTheDocument();
    await mapCredential(user);
    expect(submit()).toBeEnabled();
  });

  it("creates the AI with the suggested variable of its app and opens its profile", async () => {
    const { calls, router, user } = render();
    const dialog = await screen.findByRole("dialog", { name: "New AI in a container" });
    await waitFor(() =>
      expect(within(dialog).getByRole("combobox", { name: "Server" })).toHaveValue("r1"),
    );
    await user.type(name(), "Atlas");
    await user.type(handle(), "atlas");
    await user.click(within(dialog).getByRole("button", { name: "Map a secret" }));
    // Claude Code's first documented variable, mapped to the first stored secret.
    expect(within(dialog).getByRole("textbox", { name: "Variable" })).toHaveValue(
      "CLAUDE_CODE_OAUTH_TOKEN",
    );
    expect(within(dialog).getByRole("combobox", { name: "Secret" })).toHaveValue(
      "OPENCODE_API_KEY",
    );
    await user.click(submit());

    await waitFor(() => expect(posted(calls)).toHaveLength(1));
    expect(posted(calls)[0]?.body).toEqual({
      runner_id: "r1",
      harness: "claude",
      display_name: "Atlas",
      handle: "atlas",
      env: [{ var: "CLAUDE_CODE_OAUTH_TOKEN", secret_name: "OPENCODE_API_KEY" }],
      cpus: 1,
      memory_mb: 2048,
    });
    await waitFor(() => expect(router.state.location.pathname).toBe("/contacts/a9"));
    expect(toast.success).toHaveBeenCalledWith("Atlas is being created");
  });

  it("suggests pi's variable when pi is the app", async () => {
    const { user } = render();
    const dialog = await screen.findByRole("dialog", { name: "New AI in a container" });
    await user.selectOptions(within(dialog).getByRole("combobox", { name: "App" }), "pi");
    await user.click(within(dialog).getByRole("button", { name: "Map a secret" }));
    expect(within(dialog).getByRole("textbox", { name: "Variable" })).toHaveValue(
      "OPENCODE_API_KEY",
    );
  });

  it("sends the repository and the branch when they are given", async () => {
    const { calls, user } = render();
    const dialog = await screen.findByRole("dialog", { name: "New AI in a container" });
    await waitFor(() =>
      expect(within(dialog).getByRole("combobox", { name: "Server" })).toHaveValue("r1"),
    );
    await user.type(name(), "Atlas");
    await user.type(handle(), "atlas");
    await user.type(
      within(dialog).getByRole("textbox", { name: /Repository URL/ }),
      "https://github.com/acme/atlas.git",
    );
    await user.type(within(dialog).getByRole("textbox", { name: /Branch or tag/ }), "next");
    await mapCredential(user);
    await user.click(submit());
    await waitFor(() => expect(posted(calls)).toHaveLength(1));
    expect(posted(calls)[0]?.body).toMatchObject({
      repo_url: "https://github.com/acme/atlas.git",
      repo_ref: "next",
    });
  });

  it("asks for a name, a handle and a valid repository before sending anything", async () => {
    const { calls, user } = render();
    const dialog = await screen.findByRole("dialog", { name: "New AI in a container" });
    await waitFor(() =>
      expect(within(dialog).getByRole("combobox", { name: "Server" })).toHaveValue("r1"),
    );
    await mapCredential(user);
    await user.click(submit());
    expect(await within(dialog).findByText("Required")).toBeInTheDocument();
    expect(
      within(dialog).getByText("Start with a letter; then letters, digits or hyphens, up to 40."),
    ).toBeInTheDocument();
    await user.type(name(), "Atlas");
    await user.type(handle(), "atlas");
    await user.type(
      within(dialog).getByRole("textbox", { name: /Repository URL/ }),
      "ftp://example.com/x",
    );
    await user.click(submit());
    expect(
      await within(dialog).findByText("An https:// or ssh:// git repository URL"),
    ).toBeInTheDocument();
    expect(posted(calls)).toHaveLength(0);
  });

  it("says that no server is connected and does not send", async () => {
    const { calls } = render({
      "GET /runners": json(200, { items: [{ ...offline, online: false }] }),
    });
    const dialog = await screen.findByRole("dialog", { name: "New AI in a container" });
    expect(await within(dialog).findByText(/No server is connected right now/)).toBeInTheDocument();
    expect(submit()).toBeDisabled();
    expect(posted(calls)).toHaveLength(0);
  });

  it("shows the shared sentence for a server that dropped", async () => {
    const { user } = render({
      "POST /container-agents": apiError(409, "runner_offline", "the runner is offline"),
    });
    const dialog = await screen.findByRole("dialog", { name: "New AI in a container" });
    await waitFor(() =>
      expect(within(dialog).getByRole("combobox", { name: "Server" })).toHaveValue("r1"),
    );
    await user.type(name(), "Atlas");
    await user.type(handle(), "atlas");
    await mapCredential(user);
    await user.click(submit());
    expect(
      await within(dialog).findByText("The server is offline. Reconnect it and try again."),
    ).toBeInTheDocument();
  });

  it("shows the credential refusal inline with its own sentence", async () => {
    // A stale form (a credential the server no longer accepts) still needs a sentence that says what
    // to do, not the generic failure (CA-04).
    const { user } = render({
      "POST /container-agents": apiError(
        422,
        "credentials_required",
        "the claude app needs a credential",
        { env: "the claude app needs a credential" },
      ),
    });
    const dialog = await screen.findByRole("dialog", { name: "New AI in a container" });
    await waitFor(() =>
      expect(within(dialog).getByRole("combobox", { name: "Server" })).toHaveValue("r1"),
    );
    await user.type(name(), "Atlas");
    await user.type(handle(), "atlas");
    await mapCredential(user);
    await user.click(submit());
    expect(
      await within(dialog).findByText(
        "Map one of the variables the app reads to a secret: without a credential it can’t sign in.",
      ),
    ).toBeInTheDocument();
  });

  it("puts a refused field under its input", async () => {
    const { user } = render({
      "POST /container-agents": apiError(422, "invalid", "invalid", {
        handle: "must match the handle pattern",
      }),
    });
    const dialog = await screen.findByRole("dialog", { name: "New AI in a container" });
    await waitFor(() =>
      expect(within(dialog).getByRole("combobox", { name: "Server" })).toHaveValue("r1"),
    );
    await user.type(name(), "Atlas");
    await user.type(handle(), "atlas");
    await mapCredential(user);
    await user.click(submit());
    expect(await within(dialog).findByText("must match the handle pattern")).toBeInTheDocument();
    expect(handle()).toHaveAttribute("aria-invalid", "true");
  });

  it("tells that a secret is needed before it can be mapped", async () => {
    render({ "GET /secrets": json(200, { items: [] }) });
    const dialog = await screen.findByRole("dialog", { name: "New AI in a container" });
    expect(await within(dialog).findByText(/You have no secrets yet/)).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "Map a secret" })).toBeDisabled();
  });
});
