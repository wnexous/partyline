import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MemberPicker } from "@/components/member-picker";
import { useLiveStore } from "@/lib/live/store";
import { apiError, json, mockApi } from "@/test/fetch";
import {
  makeAI,
  makeAIContact,
  makeChat,
  makeContacts,
  makeIntelligence,
  makeMe,
  makeOrgAIs,
  makeOrgMember,
  makeRoom,
} from "@/test/fixtures";
import { renderWithRouter } from "@/test/render";

/*
 * The add-member picker (UI-10, CID-20): a room holds users and AIs, so it offers exactly those.
 * Chats are not conversation participants any more, even when GET /chats still answers.
 */

const atlas = makeAI({ id: "a1", display_name: "Atlas", handle: "atlas" });
const bob = makeIntelligence({ id: "u2", display_name: "Bob Lima", handle: "bob" });

const contacts = makeContacts({
  org_ais: [makeOrgAIs({ ais: [makeAIContact({ intelligence: atlas })] })],
  users: [bob],
});

beforeEach(() => useLiveStore.setState({ locale: "en" }));
afterEach(() => vi.unstubAllGlobals());

describe("member picker (CID-20)", () => {
  it("offers AIs and users only and never asks for chats", async () => {
    const { calls } = mockApi({
      "GET /contacts": json(200, contacts),
      "GET /chats": json(200, { items: [makeChat({ id: "c1", title: "Ops on call" })] }),
    });
    const user = userEvent.setup();
    renderWithRouter(
      <MemberPicker
        roomId="r1"
        members={[]}
        trigger={<button type="button">Add members</button>}
      />,
    );

    await user.click(await screen.findByRole("button", { name: "Add members" }));
    const dialog = await screen.findByRole("dialog", { name: "Add members" });
    expect(await within(dialog).findByTestId("candidate-agent-a1")).toHaveTextContent("Atlas");
    expect(within(dialog).getByTestId("candidate-user-u2")).toHaveTextContent("Bob Lima");
    expect(within(dialog).queryByTestId(/^candidate-chat-/)).toBeNull();
    expect(calls.some((c) => c.path === "/chats")).toBe(false);
  });

  it("lists AIs and users in one list by name, with no AIs and Users sections or badges (AD-023)", async () => {
    const cora = makeIntelligence({ id: "u3", display_name: "Cora Dias", handle: "cora" });
    const zed = makeAI({ id: "a9", display_name: "Zed", handle: "zed" });
    mockApi({
      "GET /contacts": json(
        200,
        makeContacts({
          org_ais: [
            makeOrgAIs({
              ais: [makeAIContact({ intelligence: zed }), makeAIContact({ intelligence: atlas })],
            }),
          ],
          users: [cora, bob],
        }),
      ),
    });
    const user = userEvent.setup();
    renderWithRouter(
      <MemberPicker
        roomId="r1"
        members={[]}
        trigger={<button type="button">Add members</button>}
      />,
    );

    await user.click(await screen.findByRole("button", { name: "Add members" }));
    const dialog = await screen.findByRole("dialog", { name: "Add members" });
    const list = await within(dialog).findByRole("list", { name: "Contacts" });
    expect(
      within(list)
        .getAllByRole("listitem")
        .map((row) => row.getAttribute("data-testid")),
    ).toEqual([
      "candidate-agent-a1",
      "candidate-user-u2",
      "candidate-user-u3",
      "candidate-agent-a9",
    ]);
    expect(within(dialog).queryAllByRole("region")).toEqual([]);
    expect(within(dialog).queryByText(/^(AIs|Users|IAs)$/)).toBeNull();
    expect(within(dialog).queryByTestId("kind-badge")).toBeNull();
    expect(within(dialog).queryByText(/^(AI|IA)$/)).toBeNull();
  });
});

/*
 * A group is made of the members of its organization (ORG-03): the picker offers the AIs that
 * belong to the group's organization and the users who are members of it. When the members cannot
 * be read it offers the instance's users, and the server refuses the ones who are not members.
 */
describe("member picker in a group of an organization (ORG-03)", () => {
  const lab = makeAI({ id: "a3", display_name: "Lab", handle: "lab" });
  const orgs = makeContacts({
    org_ais: [
      makeOrgAIs({ ais: [makeAIContact({ intelligence: atlas })] }),
      makeOrgAIs({
        org: { id: "o2", name: "Globex", slug: "globex" },
        ais: [makeAIContact({ intelligence: lab, org_id: "o2" })],
      }),
    ],
    users: [bob],
  });

  async function open(room: Response | (() => Response)) {
    mockApi({ "GET /contacts": json(200, orgs), "GET /rooms/r1": room });
    const user = userEvent.setup();
    renderWithRouter(
      <MemberPicker
        roomId="r1"
        members={[]}
        trigger={<button type="button">Add members</button>}
      />,
    );
    await user.click(await screen.findByRole("button", { name: "Add members" }));
    return screen.findByRole("dialog", { name: "Add members" });
  }

  it("says whose members it offers: the group's organization's, not what its user owns or was shown", async () => {
    const dialog = await open(json(200, makeRoom({ id: "r1", org_id: "o2" })));
    expect(
      within(dialog).getByText(
        "Everyone in this group’s organization. Only members of the organization can join.",
      ),
    ).toBeInTheDocument();
  });

  it("offers the AIs of the group's organization and not those of another", async () => {
    const dialog = await open(json(200, makeRoom({ id: "r1", org_id: "o2" })));
    expect(await within(dialog).findByTestId("candidate-agent-a3")).toHaveTextContent("Lab");
    expect(within(dialog).queryByTestId("candidate-agent-a1")).toBeNull();
    expect(within(dialog).getByTestId("candidate-user-u2")).toHaveTextContent("Bob Lima");
  });

  it("offers the AIs of the other organization to a group that belongs to it", async () => {
    const dialog = await open(json(200, makeRoom({ id: "r1", org_id: "o1" })));
    expect(await within(dialog).findByTestId("candidate-agent-a1")).toHaveTextContent("Atlas");
    expect(within(dialog).queryByTestId("candidate-agent-a3")).toBeNull();
  });

  it("offers every AI of the contacts when the group's organization is not known", async () => {
    const dialog = await open(apiError(500, "internal", "boom"));
    expect(await within(dialog).findByTestId("candidate-agent-a1")).toBeInTheDocument();
    expect(within(dialog).getByTestId("candidate-agent-a3")).toBeInTheDocument();
  });

  it("waits for the group, so the list is not the whole contacts first", async () => {
    let release: () => void = () => {};
    mockApi({
      "GET /contacts": json(200, orgs),
      "GET /rooms/r1": () =>
        new Promise<Response>((resolve) => {
          release = () => resolve(json(200, makeRoom({ id: "r1", org_id: "o2" })));
        }),
    });
    const user = userEvent.setup();
    renderWithRouter(
      <MemberPicker
        roomId="r1"
        members={[]}
        trigger={<button type="button">Add members</button>}
      />,
    );
    await user.click(await screen.findByRole("button", { name: "Add members" }));
    const dialog = await screen.findByRole("dialog", { name: "Add members" });
    expect(within(dialog).queryByTestId(/^candidate-/)).toBeNull();
    release();
    expect(await within(dialog).findByTestId("candidate-agent-a3")).toBeInTheDocument();
    expect(within(dialog).queryByTestId("candidate-agent-a1")).toBeNull();
  });

  describe("the users offered (T39)", () => {
    const cora = makeIntelligence({ id: "u3", display_name: "Cora Dias", handle: "cora" });
    const withUsers = makeContacts({ ...orgs, users: [bob, cora] });
    const memberOf = (...who: ReturnType<typeof makeIntelligence>[]) =>
      json(200, { items: who.map((intelligence) => makeOrgMember({ intelligence })) });

    async function openWith(extra: Parameters<typeof mockApi>[0]) {
      mockApi({
        "GET /contacts": json(200, withUsers),
        "GET /rooms/r1": json(200, makeRoom({ id: "r1", org_id: "o1" })),
        "GET /me": json(200, makeMe()),
        ...extra,
      });
      const user = userEvent.setup();
      renderWithRouter(
        <MemberPicker
          roomId="r1"
          members={[]}
          trigger={<button type="button">Add members</button>}
        />,
      );
      await user.click(await screen.findByRole("button", { name: "Add members" }));
      return screen.findByRole("dialog", { name: "Add members" });
    }

    it("are the members of the group's organization, not every user of the instance", async () => {
      const dialog = await openWith({ "GET /orgs/o1/members": memberOf(makeIntelligence(), bob) });
      expect(await within(dialog).findByTestId("candidate-user-u2")).toHaveTextContent("Bob Lima");
      expect(within(dialog).queryByTestId("candidate-user-u3")).toBeNull();
      // The viewer is a member too, and is not offered to themselves.
      expect(within(dialog).queryByTestId("candidate-user-u1")).toBeNull();
    });

    it("are those of the instance when the members cannot be read", async () => {
      const dialog = await openWith({ "GET /orgs/o1/members": apiError(500, "internal", "boom") });
      expect(await within(dialog).findByTestId("candidate-user-u2")).toBeInTheDocument();
      expect(within(dialog).getByTestId("candidate-user-u3")).toBeInTheDocument();
    });

    it("wait for the members, so the whole instance is not listed first", async () => {
      let release: () => void = () => {};
      const dialog = await openWith({
        "GET /orgs/o1/members": () =>
          new Promise<Response>((resolve) => {
            release = () => resolve(memberOf(bob));
          }),
      });
      expect(within(dialog).queryByTestId(/^candidate-/)).toBeNull();
      release();
      expect(await within(dialog).findByTestId("candidate-user-u2")).toBeInTheDocument();
      expect(within(dialog).queryByTestId("candidate-user-u3")).toBeNull();
    });
  });
});
