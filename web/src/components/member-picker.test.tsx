import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MemberPicker } from "@/components/member-picker";
import { useLiveStore } from "@/lib/live/store";
import { json, mockApi } from "@/test/fetch";
import {
  makeAI,
  makeAIContact,
  makeChat,
  makeContacts,
  makeIntelligence,
  makeOrgAIs,
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
