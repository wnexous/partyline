import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MemberList } from "@/components/member-list";
import { useLiveStore } from "@/lib/live/store";
import { json, mockApi } from "@/test/fetch";
import { makeAI, makeIntelligence, makeMe, makeMember } from "@/test/fixtures";
import { renderWithRouter } from "@/test/render";

/*
 * Rooms hold intelligences only (CID-20, CID-22): the member list shows AIs and users and has no
 * chat row, so it also has no per-chat outbound control. A chat member left in old data stays
 * invisible instead of coming back as a switch. AIs and users are one list, in the order they
 * joined, with nothing that marks one kind (AD-024).
 */

const toast = vi.hoisted(() => Object.assign(vi.fn(), { error: vi.fn() }));
vi.mock("sonner", () => ({ toast }));

const atlas = makeAI({ id: "a1", display_name: "Atlas", handle: "atlas" });
const bob = makeIntelligence({ id: "u2", display_name: "Bob Lima", handle: "bob" });
const members = [
  makeMember({ id: "mb-atlas", member_id: "a1", name: "Atlas", intelligence: atlas }),
  makeMember({
    id: "mb-bob",
    kind: "user",
    member_id: "u2",
    name: "Bob Lima",
    intelligence: bob,
    owner: undefined,
  }),
  makeMember({
    id: "mb-ops",
    kind: "chat",
    member_id: "c1",
    name: "Ops on call",
    connector_kind: "telegram",
    chat_kind: "group",
    online: undefined,
    owner: undefined,
  }),
];
const chatOnly = members[2] as (typeof members)[number];

beforeEach(() => {
  useLiveStore.setState({ locale: "en" });
  toast.mockClear();
});
afterEach(() => vi.unstubAllGlobals());

describe("member list (CID-20, CID-22)", () => {
  it("shows AIs and users and neither a chat row nor its outbound control", async () => {
    mockApi({ "GET /me": json(200, makeMe()) });
    renderWithRouter(<MemberList roomId="r1" members={members} canManage />);

    expect(await screen.findByTestId("member-mb-atlas")).toHaveTextContent("Atlas");
    expect(screen.getByTestId("member-mb-bob")).toHaveTextContent("Bob Lima");
    expect(screen.queryByTestId("member-mb-ops")).toBeNull();
    expect(screen.queryByText("Ops on call")).toBeNull();
    expect(screen.queryByRole("switch")).toBeNull();
    expect(screen.queryByText(/Outbound/)).toBeNull();
  });

  it("lists AIs and users together in the order they joined, not split by kind (AD-024)", async () => {
    const joined = [
      makeMember({
        id: "mb-bob",
        kind: "user",
        member_id: "u2",
        name: "Bob Lima",
        intelligence: bob,
        owner: undefined,
      }),
      makeMember({ id: "mb-atlas", member_id: "a1", name: "Atlas", intelligence: atlas }),
      makeMember({
        id: "mb-cora",
        kind: "user",
        member_id: "u3",
        name: "Cora Dias",
        intelligence: makeIntelligence({ id: "u3", display_name: "Cora Dias", handle: "cora" }),
        owner: undefined,
      }),
      makeMember({
        id: "mb-notes",
        member_id: "a2",
        name: "Notes",
        intelligence: makeAI({ id: "a2", display_name: "Notes", handle: "notes" }),
      }),
    ];
    mockApi({ "GET /me": json(200, makeMe()) });
    renderWithRouter(<MemberList roomId="r1" members={joined} canManage={false} />);

    const list = await screen.findByRole("list", { name: "Members" });
    expect(
      within(list)
        .getAllByRole("listitem")
        .map((row) => row.getAttribute("data-testid")),
    ).toEqual(["member-mb-bob", "member-mb-atlas", "member-mb-cora", "member-mb-notes"]);
    // No "Agents" and "Users" sections, headings or counts: one list.
    expect(screen.queryAllByRole("region")).toEqual([]);
    expect(screen.queryByRole("heading")).toBeNull();
    expect(screen.queryByText(/^(Agents|Users)$/)).toBeNull();
  });

  it("puts no kind badge next to a member, and gives an AI and a user the same row", async () => {
    mockApi({ "GET /me": json(200, makeMe()) });
    renderWithRouter(<MemberList roomId="r1" members={members} canManage={false} />);

    const ai = within(await screen.findByTestId("member-mb-atlas"));
    const user = within(screen.getByTestId("member-mb-bob"));
    for (const row of [ai, user]) {
      expect(row.getByTestId("intelligence-avatar")).toBeInTheDocument();
      expect(row.queryByTestId("kind-badge")).toBeNull();
      expect(row.queryByText(/^(AI|IA|Agent|Person|User)$/)).toBeNull();
    }
    // The @handle is what tells two names apart; the AI's presence is a fact only it has.
    expect(ai.getByText("@atlas")).toBeInTheDocument();
    expect(user.getByText("@bob")).toBeInTheDocument();
    expect(ai.getByText("Offline")).toBeInTheDocument();
    expect(user.queryByText(/line$/)).toBeNull();
  });

  it("renders the empty state when the room only still holds a chat", async () => {
    mockApi({ "GET /me": json(200, makeMe()) });
    renderWithRouter(<MemberList roomId="r1" members={[chatOnly]} canManage={false} />);

    expect(
      await screen.findByText("No members yet. Add members so messages have somewhere to go."),
    ).toBeInTheDocument();
    expect(screen.queryByTestId("member-mb-ops")).toBeNull();
  });

  it("undoes a removal by adding the member back with nothing but who it is", async () => {
    const { calls } = mockApi({
      "GET /me": json(200, makeMe()),
      "DELETE /rooms/r1/members/mb-atlas": new Response(null, { status: 204 }),
      "POST /rooms/r1/members": json(201, members[0]),
    });
    renderWithRouter(<MemberList roomId="r1" members={members} canManage />);

    await userEvent.setup().click(await screen.findByRole("button", { name: "Remove Atlas" }));
    await waitFor(() => expect(toast).toHaveBeenCalledTimes(1));
    const options = toast.mock.calls[0]?.[1] as { action: { label: string; onClick: () => void } };
    expect(options.action.label).toBe("Undo");
    options.action.onClick();

    // The retired outbound switch is not sent back (CID-22): a member is an AI or a user, nothing more.
    await waitFor(() => expect(calls.filter((c) => c.method === "POST")).toHaveLength(1));
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({
      kind: "agent",
      member_id: "a1",
    });
  });
});
