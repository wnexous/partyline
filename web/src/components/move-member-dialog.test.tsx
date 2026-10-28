import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MoveMemberDialog, type MoveTarget } from "@/components/move-member-dialog";
import { json, mockApi } from "@/test/fetch";
import { makeAI, makeIntelligence, makePosition } from "@/test/fixtures";
import { useLiveStore } from "@/test/live-store";
import { renderWithProviders } from "@/test/render";

/*
 * Moving a member to another position (POS-04). Before the move the dialog asks the server who
 * would stop managing the member and says it, so nobody loses the management of an AI by
 * surprise; the warning never blocks, and a preview that is loading or broken shows nothing.
 */

const atlas = makeAI({ id: "a1", display_name: "Atlas", handle: "atlas" });
const ana = makeIntelligence({ id: "u1", display_name: "Ana Souza", handle: "ana" });
const bob = makeIntelligence({ id: "u2", display_name: "Bob Lima", handle: "bob" });
const carol = makeIntelligence({ id: "u3", display_name: "Carol Dias", handle: "carol" });

const positions = [
  makePosition({ id: "pos1", name: "Admin" }),
  makePosition({ id: "pos2", name: "Ops" }),
];

/** Atlas, an AI of Acme, sitting in the Admin position unless a test moves it. */
const target: MoveTarget = { id: atlas.id, name: atlas.display_name, positionId: "pos1" };

const PREVIEW = "GET /orgs/o1/members/a1/move-preview";
const PATCH = "PATCH /orgs/o1/members/a1";

const warn = (names: string) =>
  `After this move, ${names} will no longer manage Atlas. Atlas will stop treating their messages as coming from someone who manages it.`;

beforeEach(() => useLiveStore.setState({ locale: "en" }));
afterEach(() => vi.unstubAllGlobals());

async function openDialog(routes: Parameters<typeof mockApi>[0] = {}) {
  const api = mockApi(routes);
  const onClose = vi.fn();
  const user = userEvent.setup();
  const utils = renderWithProviders(
    <MoveMemberDialog
      orgId="o1"
      orgName="Acme"
      target={target}
      positions={positions}
      viewer={{ admin: true }}
      onClose={onClose}
    />,
  );
  const dialog = await screen.findByRole("dialog");
  const select = within(dialog).getByLabelText("Position");
  const move = within(dialog).getByRole("button", { name: "Move" });
  const { queryClient } = utils;
  /** Waits until `count` previews were asked and all of them have been answered. */
  const previewSettled = async (count = 1) => {
    await waitFor(() => expect(previewCalls(api.calls)).toHaveLength(count));
    await waitFor(() => expect(queryClient.isFetching()).toBe(0));
  };
  return { ...api, ...utils, user, dialog, select, move, onClose, previewSettled };
}

const previewCalls = (calls: { method: string; path: string; url: URL }[]) =>
  calls.filter((c) => `${c.method} ${c.path}` === PREVIEW);

describe("moving a member warns who stops managing it (POS-04)", () => {
  it("names who would stop managing the member and still lets the move through", async () => {
    const { user, select, move, dialog, calls, onClose } = await openDialog({
      [PREVIEW]: json(200, { lose_management: [ana] }),
      [PATCH]: json(200, { intelligence: atlas, permission: "member", can_move: true }),
    });

    // The position the member already holds asks nothing and warns nothing.
    expect(previewCalls(calls)).toHaveLength(0);
    expect(move).toBeDisabled();

    await user.selectOptions(select, "pos2");

    expect(await within(dialog).findByText(warn("Ana Souza"))).toBeVisible();
    expect(move).toBeEnabled();

    await user.click(move);

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(calls.filter((c) => `${c.method} ${c.path}` === PATCH)).toHaveLength(1);
    expect(calls.find((c) => `${c.method} ${c.path}` === PATCH)?.body).toEqual({
      position_id: "pos2",
    });
  });

  it("joins several names with the conjunction of the locale", async () => {
    const { user, select, dialog } = await openDialog({
      [PREVIEW]: json(200, { lose_management: [ana, bob, carol] }),
    });

    await user.selectOptions(select, "pos2");

    expect(
      await within(dialog).findByText(warn("Ana Souza, Bob Lima, and Carol Dias")),
    ).toBeVisible();
  });

  it("shows no warning when nobody loses the management", async () => {
    const { user, select, dialog, move, previewSettled } = await openDialog({
      [PREVIEW]: json(200, { lose_management: [] }),
    });

    await user.selectOptions(select, "pos2");
    await previewSettled();

    expect(move).toBeEnabled();
    expect(within(dialog).queryByText(/will no longer manage/)).toBeNull();
  });

  it("asks nothing and warns nothing for the position the member already holds", async () => {
    const { user, select, dialog, calls, move, previewSettled } = await openDialog({
      [PREVIEW]: json(200, { lose_management: [ana] }),
    });

    await user.selectOptions(select, "pos2");
    expect(await within(dialog).findByText(warn("Ana Souza"))).toBeVisible();

    await user.selectOptions(select, "pos1");
    await previewSettled(1);

    expect(move).toBeDisabled();
    expect(within(dialog).queryByText(/will no longer manage/)).toBeNull();
    expect(previewCalls(calls).map((c) => c.url.searchParams.get("position_id"))).toEqual(["pos2"]);
  });

  it("shows nothing when the preview fails and the move still works", async () => {
    const { user, select, dialog, calls, onClose, previewSettled } = await openDialog({
      [PREVIEW]: json(500, { error: { code: "internal", message: "internal" } }),
      [PATCH]: json(200, { intelligence: atlas, permission: "member", can_move: true }),
    });

    await user.selectOptions(select, "pos2");
    await previewSettled();

    const move = within(dialog).getByRole("button", { name: "Move" });
    expect(move).toBeEnabled();
    expect(within(dialog).queryByText(/will no longer manage/)).toBeNull();

    await user.click(move);

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(calls.filter((c) => `${c.method} ${c.path}` === PATCH)).toHaveLength(1);
  });
});
