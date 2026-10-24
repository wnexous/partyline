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
  makeOrgAIs,
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
    expect(
      within(dialog)
        .getAllByRole("region")
        .map((region) => region.getAttribute("aria-label")),
    ).toEqual(["AIs", "Users"]);
    expect(within(dialog).queryByTestId(/^candidate-chat-/)).toBeNull();
    expect(calls.some((c) => c.path === "/chats")).toBe(false);
  });
});

/*
 * A group is made of the members of its organization (ORG-03): the picker offers the AIs that
 * belong to the group's organization. The users are the instance's, and the server refuses the
 * ones who are not members.
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
});
