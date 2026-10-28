import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Composer } from "@/components/composer";
import { matchMentions, mentionAt, mentionCandidates } from "@/components/mention-suggestions";
import { useLiveStore } from "@/lib/live/store";
import { LevelPost, type RoomMember } from "@/shared/api/types.gen";
import { json, mockApi } from "@/test/fetch";
import {
  makeAI,
  makeIntelligence,
  makeMe,
  makeMember,
  makeMessage,
  makePerson,
} from "@/test/fixtures";
import { renderWithProviders } from "@/test/render";

/*
 * Mention suggestions in the composer (MEN-01..MEN-06). The list comes from the conversation's
 * participants that have a handle (AIs, users and the people seen in a chat), filtered by what is
 * typed after the `@`, at most eight, the viewer excluded.
 */

const me = makeMe(); // Ana Souza (@ana), the viewer in these tests
const atlas = makeAI(); // Atlas (@atlas)
const atlasTwo = makeIntelligence({
  id: "a2",
  kind: "ai",
  role: "ai",
  display_name: "Atlas Two",
  handle: "atlas-2",
});
const bruno = makeIntelligence({ id: "u2", display_name: "Bruno Sá", handle: "bruno" });
const lia = makePerson(); // Lia Costa (@lia) on WhatsApp, seen in the chat member
const noHandle = makeIntelligence({ id: "u9", display_name: "Sem Handle", handle: "" });

const members: RoomMember[] = [
  makeMember({ id: "mb-a1", kind: "agent", member_id: "a1", name: "Atlas", intelligence: atlas }),
  makeMember({
    id: "mb-me",
    kind: "user",
    member_id: "u1",
    name: "Ana Souza",
    intelligence: me.intelligence,
  }),
  makeMember({
    id: "mb-a2",
    kind: "agent",
    member_id: "a2",
    name: "Atlas Two",
    intelligence: atlasTwo,
  }),
  makeMember({ id: "mb-u2", kind: "user", member_id: "u2", name: "Bruno Sá", intelligence: bruno }),
  makeMember({
    id: "mb-u9",
    kind: "user",
    member_id: "u9",
    name: "Sem Handle",
    intelligence: noHandle,
  }),
  makeMember({ id: "mb-chat", kind: "chat", member_id: "c1", name: "Ops on call", people: [lia] }),
];

/** A group of ten users with handles, for the cap of MEN-01. */
function crowd(): RoomMember[] {
  return Array.from({ length: 10 }, (_, i) =>
    makeMember({
      id: `mb-guest-${i}`,
      kind: "user",
      member_id: `guest-${i}`,
      name: `Guest ${i}`,
      intelligence: makeIntelligence({
        id: `guest-${i}`,
        display_name: `Guest ${i}`,
        handle: `guest-${i}`,
      }),
    }),
  );
}

function renderComposer(list: RoomMember[] = members) {
  const { calls } = mockApi({
    "GET /me": json(200, me),
    "POST /rooms/r1/messages": json(201, makeMessage({ seq: 9 })),
  });
  const user = userEvent.setup();
  renderWithProviders(<Composer roomId="r1" roomName="deploys" level={LevelPost} members={list} />);
  // Captured before the list opens: with the list open the field is the popup's combobox.
  const box = screen.getByRole("textbox", { name: "Message deploys" });
  return { user, calls, box };
}

const posted = (calls: { method: string; path: string; body: unknown }[]) =>
  calls.filter((c) => c.method === "POST" && c.path === "/rooms/r1/messages");

const listbox = () => screen.getByRole("listbox", { name: "Mention someone" });

beforeEach(() => useLiveStore.setState({ locale: "en" }));
afterEach(() => vi.unstubAllGlobals());

describe("mentionAt", () => {
  it("MEN-01: finds the @word under the caret when @ starts a word", () => {
    expect(mentionAt("@at", 3)).toEqual({ start: 0, end: 3, query: "at" });
    expect(mentionAt("hi @at there", 6)).toEqual({ start: 3, end: 6, query: "at" });
    // Inside the word the whole word is still the one the suggestion replaces.
    expect(mentionAt("hi @atlas there", 6)).toEqual({ start: 3, end: 9, query: "at" });
    expect(mentionAt("hi @", 4)).toEqual({ start: 3, end: 4, query: "" });
    expect(mentionAt("@", 1)).toEqual({ start: 0, end: 1, query: "" });
  });

  it("MEN-01: ignores an @ glued to a letter, digit or underscore such as an address", () => {
    expect(mentionAt("joe@atlas.example", 8)).toBeNull();
    expect(mentionAt("a@b", 3)).toBeNull();
    expect(mentionAt("olá@at", 6)).toBeNull();
    expect(mentionAt("notes_2@at", 10)).toBeNull();
  });

  it("MEN-04: no mention while the caret is outside an @word", () => {
    expect(mentionAt("@at", 0)).toBeNull();
    expect(mentionAt("@at there", 9)).toBeNull();
    expect(mentionAt("hi there", 8)).toBeNull();
    expect(mentionAt("", 0)).toBeNull();
  });
});

describe("mention candidates", () => {
  it("MEN-01: keeps participants with a handle and drops the viewer and chats", () => {
    expect(mentionCandidates(members, me.id).map((c) => c.intelligence.handle)).toEqual([
      "atlas",
      "atlas-2",
      "bruno",
      "lia",
    ]);
  });

  it("MEN-02: matches the display name or the handle ignoring case and accents", () => {
    const all = mentionCandidates(members, me.id);
    const handles = (query: string) => matchMentions(all, query).map((c) => c.intelligence.handle);
    expect(handles("ATL")).toEqual(["atlas", "atlas-2"]);
    expect(handles("sá")).toEqual(["bruno"]);
    expect(handles("SA")).toEqual(["bruno"]);
    expect(handles("runo")).toEqual(["bruno"]);
    expect(handles("atlas-2")).toEqual(["atlas-2"]);
    expect(handles("zzz")).toEqual([]);
  });

  it("MEN-01: suggests at most eight participants", () => {
    const all = mentionCandidates(crowd(), me.id);
    expect(all).toHaveLength(10);
    expect(matchMentions(all, "")).toHaveLength(8);
  });
});

describe("session candidates (SESS-33)", () => {
  const principalSession = { label: "", harness: "claude", connected_at: "2026-10-19T11:00:00Z" };
  const labelledSession = {
    label: "claude-2",
    harness: "pi",
    connected_at: "2026-10-19T12:00:00Z",
  };
  const sessions = [principalSession, labelledSession];
  const atlasSession = makeMember({
    id: "mb-a1",
    kind: "agent",
    member_id: "a1",
    name: "Atlas",
    intelligence: atlas,
    sessions,
  });
  // A human member never has sessions of its own; a stray one must still add only its handle.
  const withSessions: RoomMember[] = [
    atlasSession,
    makeMember({
      id: "mb-me",
      kind: "user",
      member_id: "u1",
      name: "Ana Souza",
      intelligence: me.intelligence,
    }),
    makeMember({
      id: "mb-u2",
      kind: "user",
      member_id: "u2",
      name: "Bruno Sá",
      intelligence: bruno,
      sessions,
    }),
  ];
  const pairs = (list: { intelligence: { handle: string }; label: string }[]) =>
    list.map((c) => ({ handle: c.intelligence.handle, label: c.label }));

  it("MEN-01: offers the AI once and one candidate per live labelled session", () => {
    expect(pairs(mentionCandidates(withSessions, me.id))).toEqual([
      { handle: "atlas", label: "" },
      { handle: "atlas", label: "claude-2" },
      { handle: "bruno", label: "" },
    ]);
  });

  it("MEN-02: matches the label as well as the display name and the handle", () => {
    const all = mentionCandidates(withSessions, me.id);
    expect(pairs(matchMentions(all, "claude"))).toEqual([{ handle: "atlas", label: "claude-2" }]);
    expect(pairs(matchMentions(all, "atlas"))).toEqual([
      { handle: "atlas", label: "" },
      { handle: "atlas", label: "claude-2" },
    ]);
  });

  it("MEN-01: two AIs with the same label are two candidates", () => {
    const both = [
      makeMember({ id: "mb-a1", kind: "agent", member_id: "a1", intelligence: atlas, sessions }),
      makeMember({
        id: "mb-a2",
        kind: "agent",
        member_id: "a2",
        intelligence: atlasTwo,
        sessions: [labelledSession],
      }),
    ];
    expect(pairs(mentionCandidates(both, me.id))).toEqual([
      { handle: "atlas", label: "" },
      { handle: "atlas", label: "claude-2" },
      { handle: "atlas-2", label: "" },
      { handle: "atlas-2", label: "claude-2" },
    ]);
  });

  it("MEN-01: a session labelled principal and the principal are both candidates", () => {
    const member = makeMember({
      id: "mb-a1",
      kind: "agent",
      member_id: "a1",
      intelligence: atlas,
      sessions: [
        principalSession,
        { label: "principal", harness: "pi", connected_at: "2026-10-19T12:00:00Z" },
      ],
    });
    expect(pairs(mentionCandidates([member], me.id))).toEqual([
      { handle: "atlas", label: "" },
      { handle: "atlas", label: "principal" },
    ]);
  });

  it("MEN-02: handle:label matches the handle before the colon and the label after it", () => {
    const both = [
      makeMember({ id: "mb-a1", kind: "agent", member_id: "a1", intelligence: atlas, sessions }),
      makeMember({
        id: "mb-a2",
        kind: "agent",
        member_id: "a2",
        intelligence: atlasTwo,
        sessions: [labelledSession],
      }),
    ];
    const all = mentionCandidates(both, me.id);
    expect(pairs(matchMentions(all, "atlas:cl"))).toEqual([
      { handle: "atlas", label: "claude-2" },
      { handle: "atlas-2", label: "claude-2" },
    ]);
    expect(pairs(matchMentions(all, "atlas-2:cl"))).toEqual([
      { handle: "atlas-2", label: "claude-2" },
    ]);
    expect(pairs(matchMentions(all, "atlas:zz"))).toEqual([]);
    expect(pairs(matchMentions(all, "bruno:cl"))).toEqual([]);
  });

  it("MEN-01: the option shows the label and leaves the principal plain", async () => {
    const { user, box } = renderComposer(withSessions);
    await user.type(box, "@atlas");
    const options = within(listbox()).getAllByRole("option");
    expect(options).toHaveLength(2);
    const principal = options[0] as HTMLElement;
    expect(within(principal).getByText("@atlas", { exact: true })).toBeInTheDocument();
    expect(within(principal).queryByText(/^:claude-2$/)).toBeNull();
    const labelled = options[1] as HTMLElement;
    expect(within(labelled).getByText("@atlas", { exact: true })).toBeInTheDocument();
    expect(within(labelled).getByText(":claude-2", { exact: true })).toBeInTheDocument();
  });
});

describe("composer mentions", () => {
  it("MEN-01: typing @ lists participants with avatar, name and handle, and no kind badge (AD-024)", async () => {
    const { user, box } = renderComposer();
    await user.type(box, "@");
    const list = listbox();
    expect(within(list).getAllByRole("option")).toHaveLength(4);

    const atlasRow = within(list).getByRole("option", { name: /@atlas$/ });
    expect(within(atlasRow).getByTestId("intelligence-avatar")).toBeInTheDocument();
    expect(within(atlasRow).queryByTestId("kind-badge")).toBeNull();
    expect(within(atlasRow).queryByText(/^(AI|IA)$/)).toBeNull();
    expect(within(atlasRow).getByText("Atlas")).toBeInTheDocument();
    expect(within(atlasRow).getByText("@atlas", { exact: true })).toBeInTheDocument();

    // A person comes from the participants the chat member carries, and reads like the others.
    const personRow = within(list).getByRole("option", { name: /@lia$/ });
    expect(within(personRow).getByText("Lia Costa")).toBeInTheDocument();
    expect(within(personRow).queryByTestId("kind-badge")).toBeNull();
    expect(within(personRow).queryByText("WhatsApp")).toBeNull();

    // The viewer and a participant without a handle are never suggested.
    expect(within(list).queryByText("@ana")).toBeNull();
    expect(within(list).queryByText("Sem Handle")).toBeNull();
  });

  it("MEN-01: an @ inside a word does not open the list", async () => {
    const { user, box } = renderComposer();
    await user.type(box, "joe@atlas.example");
    expect(screen.queryByRole("listbox")).toBeNull();
  });

  it("MEN-02: typing after @ narrows the list by name or handle, case and accents ignored", async () => {
    const { user, box } = renderComposer();
    await user.type(box, "@BRU");
    expect(within(listbox()).getAllByRole("option")).toHaveLength(1);
    expect(within(listbox()).getByText("@bruno", { exact: true })).toBeInTheDocument();

    await user.clear(box);
    await user.type(box, "@sá");
    expect(within(listbox()).getAllByRole("option")).toHaveLength(1);
    expect(within(listbox()).getByText("@bruno", { exact: true })).toBeInTheDocument();

    await user.clear(box);
    await user.type(box, "@ATLAS-2");
    expect(within(listbox()).getAllByRole("option")).toHaveLength(1);
    expect(within(listbox()).getByText("@atlas-2", { exact: true })).toBeInTheDocument();
  });

  it("MEN-03: Enter completes the @word with @handle and a space without sending", async () => {
    const { user, box, calls } = renderComposer();
    await user.type(box, "@at");
    await user.keyboard("{Enter}");
    expect(box).toHaveValue("@atlas ");
    expect(posted(calls)).toHaveLength(0);
    expect(screen.queryByRole("listbox")).toBeNull();
    // The caret sits after the trailing space, so the next word starts cleanly.
    expect((box as HTMLTextAreaElement).selectionStart).toBe(7);
  });

  it("MEN-03: Tab completes the mention without sending", async () => {
    const { user, box, calls } = renderComposer();
    await user.type(box, "@lia");
    await user.keyboard("{Tab}");
    expect(box).toHaveValue("@lia ");
    expect(posted(calls)).toHaveLength(0);
    expect(box).toHaveFocus();
  });

  it("MEN-03: tapping a suggestion completes the mention without sending (mobile)", async () => {
    const { user, box, calls } = renderComposer();
    await user.type(box, "@bru");
    await user.click(screen.getByRole("option", { name: /@bruno$/ }));
    expect(box).toHaveValue("@bruno ");
    expect(posted(calls)).toHaveLength(0);
    await waitFor(() => expect(box).toHaveFocus());
  });

  it("MEN-04: Escape closes the list without changing the text", async () => {
    const { user, box } = renderComposer();
    await user.type(box, "@at");
    expect(screen.getByRole("listbox")).toBeInTheDocument();
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("listbox")).toBeNull();
    expect(box).toHaveValue("@at");
  });

  it("MEN-04: moving the caret out of the @word closes the list", async () => {
    const { user, box } = renderComposer();
    await user.type(box, "@at");
    expect(screen.getByRole("listbox")).toBeInTheDocument();
    await user.keyboard("{ArrowLeft}{ArrowLeft}{ArrowLeft}");
    expect(screen.queryByRole("listbox")).toBeNull();
    expect(box).toHaveValue("@at");
  });

  it("MEN-05: no match shows no list and Enter sends the text as usual", async () => {
    const { user, box, calls } = renderComposer();
    await user.type(box, "@zzz");
    expect(screen.queryByRole("listbox")).toBeNull();
    await user.keyboard("{Enter}");
    await waitFor(() => expect(posted(calls)).toHaveLength(1));
    expect(posted(calls)[0]?.body).toEqual({ text: "@zzz" });
  });

  it("MEN-06: the field is a combobox and the active option is announced", async () => {
    const { user, box } = renderComposer();
    await user.type(box, "@");
    const combo = screen.getByRole("combobox", { name: "Message deploys" });
    expect(combo).toHaveAttribute("aria-expanded", "true");
    expect(combo).toHaveAttribute("aria-controls", listbox().id);
    expect(combo).toHaveAttribute("aria-autocomplete", "list");
    expect(combo).toHaveAttribute("aria-haspopup", "listbox");

    const options = within(listbox()).getAllByRole("option");
    expect(options[0]).toHaveAttribute("aria-selected", "true");
    expect(combo).toHaveAttribute("aria-activedescendant", options[0]?.id ?? "");
    for (const option of options) {
      expect(option).toHaveAccessibleName(/@/);
    }

    await user.keyboard("{ArrowDown}");
    const active = within(listbox()).getAllByRole("option")[1];
    expect(active).toHaveAttribute("aria-selected", "true");
    expect(options[0]).toHaveAttribute("aria-selected", "false");
    expect(combo).toHaveAttribute("aria-activedescendant", active?.id ?? "");

    await user.keyboard("{ArrowUp}");
    expect(within(listbox()).getAllByRole("option")[0]).toHaveAttribute("aria-selected", "true");

    await user.keyboard("{Escape}");
    expect(screen.getByRole("textbox", { name: "Message deploys" })).toBeInTheDocument();
  });
});
