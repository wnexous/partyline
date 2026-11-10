import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { apiError, json, mockApi } from "@/test/fetch";
import { makeAI, makeIntelligence, makeOrgMember } from "@/test/fixtures";
import { useLiveStore } from "@/test/live-store";
import { renderWithProviders } from "@/test/render";
import { OrgMemberActions } from "./org-member-actions";
import { ShareAccessDialog } from "./share-access-dialog";

/*
 * "Give the same access as me" (ST-02..ST-04): the confirmation says plainly that the person will
 * hold the viewer's position, manage what it manages and be trusted by its AIs, names from the
 * move preview which AIs will trust them and what stays out, and then sends the ordinary position
 * change. A refusal stays inside the dialog.
 */

const atlas = makeAI({ id: "a1", display_name: "Atlas", handle: "atlas" });
const bolt = makeAI({ id: "a2", display_name: "Bolt", handle: "bolt" });
const bob = makeIntelligence({ id: "u2", display_name: "Bob Lima", handle: "bob" });
const mia = makeIntelligence({ id: "u3", display_name: "Mia Reis", handle: "mia" });

const PREVIEW = "GET /orgs/o1/members/u3/move-preview";
const PATCH = "PATCH /orgs/o1/members/u3";

const BODY =
  "Mia Reis will hold your position, Admin, in Acme. They will manage the people and AIs you manage through it, and those AIs will treat their messages as trusted: they may act on them as requests.";

const preview = (over: Record<string, unknown> = {}) =>
  json(200, {
    lose_management: [],
    will_manage: [atlas],
    will_be_trusted_by: [atlas],
    not_shared: [],
    ...over,
  });

beforeEach(() => useLiveStore.setState({ locale: "en" }));
afterEach(() => vi.unstubAllGlobals());

async function openDialog(routes: Parameters<typeof mockApi>[0]) {
  const api = mockApi(routes);
  const onClose = vi.fn();
  const user = userEvent.setup();
  renderWithProviders(
    <ShareAccessDialog
      orgId="o1"
      orgName="Acme"
      target={{ id: mia.id, name: mia.display_name, permission: "member" }}
      position={{ id: "pos1", name: "Admin" }}
      onClose={onClose}
    />,
  );
  const dialog = await screen.findByRole("alertdialog");
  const confirm = within(dialog).getByRole("button", { name: "Give the same access" });
  return { ...api, user, dialog, confirm, onClose };
}

describe("giving someone the same access (ST-03, ST-04)", () => {
  it("says what it means, names the AIs that will trust them, and sends the position change", async () => {
    const { user, dialog, confirm, calls, onClose } = await openDialog({
      [PREVIEW]: preview(),
      [PATCH]: json(200, makeOrgMember({ intelligence: mia, position_id: "pos1" })),
    });

    expect(
      within(dialog).getByRole("heading", { name: "Give Mia Reis the same access as you?" }),
    ).toBeVisible();
    expect(within(dialog).getByText(BODY)).toBeVisible();
    expect(
      await within(dialog).findByText("AIs that will take their instructions: Atlas."),
    ).toBeVisible();
    expect(
      within(dialog).getByText(
        "Their permission stays member. To undo this, move Mia Reis to another position.",
      ),
    ).toBeVisible();
    // The preview asked about the viewer's own position.
    expect(
      calls.find((c) => `${c.method} ${c.path}` === PREVIEW)?.url.searchParams.get("position_id"),
    ).toBe("pos1");
    expect(calls.filter((c) => c.method === "PATCH")).toHaveLength(0);

    await waitFor(() => expect(confirm).toBeEnabled());
    await user.click(confirm);

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    const patches = calls.filter((c) => `${c.method} ${c.path}` === PATCH);
    expect(patches).toHaveLength(1);
    expect(patches[0]?.body).toEqual({ position_id: "pos1" });
  });

  it("names what stays out and who stops managing them", async () => {
    const { dialog } = await openDialog({
      [PREVIEW]: preview({
        will_be_trusted_by: [],
        not_shared: [atlas, bolt],
        lose_management: [bob],
      }),
    });

    expect(
      await within(dialog).findByText("No AI of Acme will take their instructions yet."),
    ).toBeVisible();
    expect(
      within(dialog).getByText(
        "Not included: Atlas and Bolt. You manage them because you created or invited them, and that is not shared. To include them, move them to a position below Admin.",
      ),
    ).toBeVisible();
    expect(within(dialog).getByText("Bob Lima will no longer manage Mia Reis.")).toBeVisible();
  });

  it("keeps the dialog open with the reason when the server refuses", async () => {
    const { user, dialog, confirm, onClose } = await openDialog({
      [PREVIEW]: preview(),
      [PATCH]: apiError(403, "forbidden"),
    });

    await waitFor(() => expect(confirm).toBeEnabled());
    await user.click(confirm);

    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "Only an admin can give someone your position.",
    );
    expect(onClose).not.toHaveBeenCalled();
    expect(screen.getByRole("alertdialog")).toBeVisible();
  });

  it("waits for the preview before it can be confirmed, and still works when it fails", async () => {
    let answer: (r: Response) => void = () => {};
    const { user, dialog, confirm, calls, onClose } = await openDialog({
      [PREVIEW]: () =>
        new Promise<Response>((resolve) => {
          answer = resolve;
        }) as unknown as Response,
      [PATCH]: json(200, makeOrgMember({ intelligence: mia, position_id: "pos1" })),
    });

    expect(within(dialog).getByText(BODY)).toBeVisible();
    expect(within(dialog).getByText("Checking who this affects…")).toBeVisible();
    expect(confirm).toBeDisabled();
    await waitFor(() => expect(calls.some((c) => `${c.method} ${c.path}` === PREVIEW)).toBe(true));
    answer(apiError(500, "internal"));
    expect(
      await within(dialog).findByText(
        "Couldn’t check who this affects. The change itself still works.",
      ),
    ).toBeVisible();
    await waitFor(() => expect(confirm).toBeEnabled());
    await user.click(confirm);
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(calls.filter((c) => `${c.method} ${c.path}` === PATCH)).toHaveLength(1);
  });
});

describe("the row action (ST-02)", () => {
  const renderRow = (canShare: boolean, onShare?: () => void) =>
    renderWithProviders(
      <OrgMemberActions
        member={makeOrgMember({ intelligence: mia, can_share_access: canShare })}
        name="Mia Reis"
        orgName="Acme"
        variant="table"
        onMove={vi.fn()}
        {...(onShare ? { onShare } : {})}
        onRemove={vi.fn()}
        onDelete={vi.fn()}
      />,
    );

  it("is offered only where the server says so", async () => {
    const onShare = vi.fn();
    renderRow(true, onShare);
    await userEvent
      .setup()
      .click(screen.getByRole("button", { name: "Give the same access as me" }));
    expect(onShare).toHaveBeenCalledTimes(1);
  });

  it("is absent without the flag, or when the page does not handle it", () => {
    const { unmount } = renderRow(false, vi.fn());
    expect(screen.queryByRole("button", { name: "Give the same access as me" })).toBeNull();
    unmount();
    renderRow(true);
    expect(screen.queryByRole("button", { name: "Give the same access as me" })).toBeNull();
  });

  it("reads in Portuguese", () => {
    useLiveStore.setState({ locale: "pt-BR" });
    renderRow(true, vi.fn());
    expect(screen.getByRole("button", { name: "Dar o mesmo acesso que o meu" })).toBeVisible();
  });
});
