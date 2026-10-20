import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { SharingDialog } from "@/components/sharing-dialog";
import { Button } from "@/components/ui/button";
import type { SharingResource } from "@/lib/api/queries";
import type { Sharing } from "@/lib/api/types.gen";
import { useLiveStore } from "@/lib/live/store";
import { json, mockApi } from "@/test/fetch";
import { renderWithProviders } from "@/test/render";

const owner = { id: "u1", username: "ana", display_name: "Ana Souza" };
const directory = [
  owner,
  { id: "u2", username: "bruno", display_name: "Bruno Lima" },
  { id: "u3", username: "carla", display_name: "Carla Dias" },
];

function setup(kind: SharingResource, id: string, current: Sharing) {
  const api = mockApi({
    [`GET /${kind}/${id}/sharing`]: json(200, current),
    [`PUT /${kind}/${id}/sharing`]: async (_url, init) => {
      const body = JSON.parse(String(init?.body)) as Sharing;
      return json(200, {
        ...body,
        users: body.users.map((u) => ({
          ...u,
          ...directory.find((d) => d.id === u.user_id),
          id: undefined,
        })),
      });
    },
    "GET /users": json(200, { items: directory }),
  });
  renderWithProviders(
    <SharingDialog
      kind={kind}
      id={id}
      name="deploys"
      owner={owner}
      trigger={<Button>Share</Button>}
    />,
  );
  return api;
}

async function open() {
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "Share" }));
  const dialog = await screen.findByRole("dialog");
  await within(dialog).findByRole("radiogroup", { name: "Visibility" });
  return { user, dialog };
}

const put = (calls: { method: string; body: unknown }[]) =>
  calls.filter((c) => c.method === "PUT").at(-1)?.body;

beforeEach(() => useLiveStore.setState({ locale: "en" }));
afterEach(() => vi.unstubAllGlobals());

describe("sharing dialog (UI-11)", () => {
  it("shows the current sharing and who can access", async () => {
    setup("rooms", "r1", {
      mode: "shared",
      users: [{ user_id: "u2", level: "post", username: "bruno", display_name: "Bruno Lima" }],
    });
    const { dialog } = await open();
    expect(within(dialog).getByRole("radio", { name: /Shared/ })).toBeChecked();
    const summary = within(dialog).getByRole("region", { name: "Who can access" });
    expect(summary).toHaveTextContent("Ana Souza");
    expect(summary).toHaveTextContent("Owner");
    expect(summary).toHaveTextContent("Bruno Lima");
    expect(summary).toHaveTextContent("Can post");
  });

  it("submits private with no users", async () => {
    const { calls } = setup("rooms", "r1", {
      mode: "shared",
      users: [{ user_id: "u2", level: "post" }],
    });
    const { user, dialog } = await open();
    await user.click(within(dialog).getByRole("radio", { name: /Private/ }));
    expect(within(dialog).getByRole("region", { name: "Who can access" })).toHaveTextContent(
      "Only Ana Souza",
    );
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(put(calls)).toEqual({ mode: "private", users: [] }));
  });

  it("submits shared with each picked user and level", async () => {
    const { calls } = setup("rooms", "r1", {
      mode: "shared",
      users: [{ user_id: "u2", level: "post", username: "bruno", display_name: "Bruno Lima" }],
    });
    const { user, dialog } = await open();
    const search = within(dialog).getByRole("searchbox", { name: "Add people" });
    // The owner always has access and is never offered.
    await user.type(search, "ana");
    expect(within(dialog).queryByRole("button", { name: "Add Ana Souza" })).toBeNull();
    await user.clear(search);
    await user.type(search, "carl");
    await user.click(within(dialog).getByRole("button", { name: "Add Carla Dias" }));
    await user.selectOptions(
      within(dialog).getByRole("combobox", { name: "Level for Carla Dias" }),
      "manage",
    );
    expect(within(dialog).getByRole("region", { name: "Who can access" })).toHaveTextContent(
      "Carla Dias",
    );
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(put(calls)).toEqual({
        mode: "shared",
        users: [
          { user_id: "u2", level: "post" },
          { user_id: "u3", level: "manage" },
        ],
      }),
    );
  });

  it("removes a person from a shared list", async () => {
    const { calls } = setup("rooms", "r1", {
      mode: "shared",
      users: [
        { user_id: "u2", level: "post", username: "bruno", display_name: "Bruno Lima" },
        { user_id: "u3", level: "view", username: "carla", display_name: "Carla Dias" },
      ],
    });
    const { user, dialog } = await open();
    await user.click(within(dialog).getByRole("button", { name: "Remove Bruno Lima" }));
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(put(calls)).toEqual({ mode: "shared", users: [{ user_id: "u3", level: "view" }] }),
    );
  });

  it("does not save shared with nobody picked", async () => {
    const { calls } = setup("rooms", "r1", { mode: "private", users: [] });
    const { user, dialog } = await open();
    await user.click(within(dialog).getByRole("radio", { name: /Shared/ }));
    expect(within(dialog).getByRole("button", { name: "Save" })).toBeDisabled();
    expect(calls.some((c) => c.method === "PUT")).toBe(false);
  });

  it("submits public with the chosen level for rooms", async () => {
    const { calls } = setup("rooms", "r1", { mode: "private", users: [] });
    const { user, dialog } = await open();
    await user.click(within(dialog).getByRole("radio", { name: /Public/ }));
    await user.selectOptions(
      within(dialog).getByRole("combobox", { name: "Level for everyone" }),
      "post",
    );
    expect(within(dialog).getByRole("region", { name: "Who can access" })).toHaveTextContent(
      "Everyone on this instance",
    );
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(put(calls)).toEqual({ mode: "public", users: [], public_level: "post" }),
    );
  });

  it("uses the `use` level for agents and connectors", async () => {
    const { calls } = setup("agents", "a1", { mode: "private", users: [] });
    const { user, dialog } = await open();
    await user.click(within(dialog).getByRole("radio", { name: /Shared/ }));
    await user.type(within(dialog).getByRole("searchbox", { name: "Add people" }), "carla");
    await user.click(within(dialog).getByRole("button", { name: "Add Carla Dias" }));
    // A single level: nothing to choose.
    expect(within(dialog).queryByRole("combobox", { name: "Level for Carla Dias" })).toBeNull();
    expect(within(dialog).getByRole("region", { name: "Who can access" })).toHaveTextContent(
      "Can use",
    );
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(put(calls)).toEqual({ mode: "shared", users: [{ user_id: "u3", level: "use" }] }),
    );

    // Saving closes the dialog; open it again to go public.
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await user.click(screen.getByRole("button", { name: "Share" }));
    const again = await screen.findByRole("dialog");
    await user.click(await within(again).findByRole("radio", { name: /Public/ }));
    await user.click(within(again).getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(put(calls)).toEqual({ mode: "public", users: [], public_level: "use" }),
    );
  });

  it("PUTs to the connector sharing endpoint", async () => {
    const { calls } = setup("connectors", "k1", { mode: "private", users: [] });
    const { user, dialog } = await open();
    await user.click(within(dialog).getByRole("radio", { name: /Public/ }));
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(calls.find((c) => c.method === "PUT")?.path).toBe("/connectors/k1/sharing"),
    );
  });
});
