import { screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MemberList } from "@/components/member-list";
import { useLiveStore } from "@/lib/live/store";
import { json, mockApi } from "@/test/fetch";
import { makeAI, makeIntelligence, makeMe, makeMember } from "@/test/fixtures";
import { renderWithRouter } from "@/test/render";

/*
 * Rooms hold intelligences only (CID-20, CID-22): the member list shows AIs and users and has no
 * chat row, so it also has no per-chat outbound control. A chat member left in old data stays
 * invisible instead of coming back as a switch.
 */

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

beforeEach(() => useLiveStore.setState({ locale: "en" }));
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
    expect(
      screen.getAllByRole("region").map((region) => region.getAttribute("aria-label")),
    ).toEqual(["Agents", "Users"]);
  });

  it("renders the empty state when the room only still holds a chat", async () => {
    mockApi({ "GET /me": json(200, makeMe()) });
    renderWithRouter(<MemberList roomId="r1" members={[chatOnly]} canManage={false} />);

    expect(
      await screen.findByText(
        "No members yet. Add agents and users so messages have somewhere to go.",
      ),
    ).toBeInTheDocument();
    expect(screen.queryByTestId("member-mb-ops")).toBeNull();
  });
});
