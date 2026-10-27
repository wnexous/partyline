import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Message, Room } from "@/lib/api/types.gen";
import { useLiveStore } from "@/lib/live/store";
import { apiError, json, mockApi } from "@/test/fetch";
import { makeIntelligence, makeMe, makeMessage, makeRoom } from "@/test/fixtures";
import { mouseDevice } from "@/test/gestures";
import { stubLayout } from "@/test/layout";
import { renderRoute } from "@/test/render";

/*
 * The quick replies under a question in the bubble (QR-10..QR-13): what a member is offered, what a
 * tap posts, how the choice reads while it is sent and once it is stored, who sees no action and why,
 * and what a failed answer does.
 */

vi.mock("@/lib/live", () => ({ startLive: vi.fn(() => () => {}) }));
const toast = vi.hoisted(() => Object.assign(vi.fn(), { error: vi.fn() }));
vi.mock("sonner", async (importOriginal) => ({ ...(await importOriginal<object>()), toast }));

const bob = makeIntelligence({ id: "u2", display_name: "Bob Lima", handle: "bob" });
const QUESTION_ID = "m-r1-1";

/** A question by Bob with three quick replies ("Sim", "Não", "Depois"). */
function question(over: Partial<Message> = {}): Message {
  return makeMessage({
    seq: 1,
    author_id: "u2",
    author_name: "Bob Lima",
    author: bob,
    text: "Vens hoje?",
    interactive: {
      v: 1,
      quick_replies: [
        { id: "r1", label: "Sim" },
        { id: "r2", label: "Não" },
        { id: "r3", label: "Depois" },
      ],
    },
    ...over,
  });
}

function page(items: Message[]) {
  return json(200, { items: [...items].reverse(), has_more: false });
}

function roomApi(
  room: Partial<Room> = {},
  messages: Message[] = [question()],
  extra: Parameters<typeof mockApi>[0] = {},
) {
  return mockApi({
    "GET /me": json(200, makeMe()),
    "GET /rooms/r1": json(200, makeRoom({ id: "r1", name: "deploys", ...room })),
    "GET /rooms/r1/members": json(200, { items: [] }),
    "GET /rooms/r1/messages": page(messages),
    ...extra,
  });
}

/** The viewer's own answer to the question, as the timeline holds it after a reload. */
function answer(over: Partial<Message> = {}): Message {
  return makeMessage({
    seq: 2,
    text: "Sim",
    reply_to: QUESTION_ID,
    answers: { v: 1, quick_reply: "Sim" },
    ...over,
  });
}

const posts = (calls: ReturnType<typeof mockApi>["calls"]) =>
  calls.filter((c) => c.method === "POST" && c.path === "/rooms/r1/messages");
const group = () => screen.getByRole("group", { name: "Quick replies" });
const button = (name: string) => within(group()).getByRole("button", { name });
const status = () => within(group()).queryByRole("status");

stubLayout();
beforeEach(() => {
  useLiveStore.setState({ locale: "en" });
  mouseDevice();
  toast.mockClear();
  toast.error.mockClear();
});
afterEach(() => vi.unstubAllGlobals());

describe("TestOffersTheButtons", () => {
  it("shows one button per quick reply inside a labelled group", async () => {
    roomApi({ my_level: "post" });
    renderRoute("/rooms/r1");
    await screen.findByText("Vens hoje?");
    const offers = group();
    expect(within(offers).getAllByRole("button")).toHaveLength(3);
    for (const label of ["Sim", "Não", "Depois"]) {
      expect(within(offers).getByRole("button", { name: label })).toBeInTheDocument();
    }
  });
});

describe("TestATapPostsOnce", () => {
  it("posts the answer as a reply, holds the buttons while it is sent and marks the chosen one", async () => {
    let resolve!: (response: Response) => void;
    const pending = new Promise<Response>((r) => {
      resolve = r;
    });
    const { calls } = roomApi({ my_level: "post" }, [question()], {
      "POST /rooms/r1/messages": () => pending,
    });
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    await screen.findByText("Vens hoje?");

    await user.click(button("Não"));

    expect(posts(calls)).toHaveLength(1);
    expect(posts(calls)[0]?.body).toEqual({
      text: "",
      reply_to: QUESTION_ID,
      answers: { quick_reply: "r2" },
    });
    expect(status()).toHaveTextContent("Sending your answer…");
    for (const label of ["Sim", "Não", "Depois"]) {
      expect(button(label)).toHaveAttribute("aria-disabled", "true");
    }
    // A second tap while the answer is in flight does nothing.
    await user.click(button("Sim"));
    expect(posts(calls)).toHaveLength(1);

    await act(async () => {
      resolve(json(201, answer({ text: "Não", answers: { v: 1, quick_reply: "Não" } })));
    });

    await waitFor(() => expect(status()).toHaveTextContent("Answer sent: Não"));
    expect(button("Não")).toHaveAttribute("aria-pressed", "true");
    expect(button("Sim")).toHaveAttribute("aria-pressed", "false");
    for (const label of ["Sim", "Não", "Depois"]) {
      expect(button(label)).toHaveAttribute("aria-disabled", "true");
    }
    // The stored message comes through the live event; a tap after it changes nothing more.
    await user.click(button("Sim"));
    expect(posts(calls)).toHaveLength(1);
  });
});

describe("TestALoadedAnswerIsChosen", () => {
  it("marks the label the viewer answered with and posts nothing more", async () => {
    const { calls } = roomApi({ my_level: "post" }, [question(), answer()]);
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    await screen.findAllByText("Vens hoje?");

    expect(button("Sim")).toHaveAttribute("aria-pressed", "true");
    expect(button("Não")).toHaveAttribute("aria-pressed", "false");
    expect(status()).toHaveTextContent("Answer sent: Sim");

    await user.click(button("Não"));
    expect(posts(calls)).toHaveLength(0);
  });
});

describe("TestAPlainReplyIsNotAnAnswer", () => {
  it("leaves the buttons acting for a reply without an answer", async () => {
    const plain = makeMessage({ seq: 2, text: "posso ir", reply_to: QUESTION_ID });
    const { calls } = roomApi({ my_level: "post" }, [question(), plain]);
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    await screen.findAllByText("Vens hoje?");

    expect(button("Sim")).not.toHaveAttribute("aria-pressed", "true");
    expect(button("Sim")).not.toHaveAttribute("aria-disabled", "true");
    expect(status()).toBeNull();

    await user.click(button("Sim"));
    expect(posts(calls)).toHaveLength(1);
    expect(posts(calls)[0]?.body).toEqual({
      text: "",
      reply_to: QUESTION_ID,
      answers: { quick_reply: "r1" },
    });
  });
});

describe("TestTheAskersAndReadersSeeNoAction", () => {
  it("shows the asker the labels and says the question is theirs", async () => {
    const own = question({ author_id: "u1", author_name: "Ana", author: makeIntelligence() });
    const { calls } = roomApi({ my_level: "post" }, [own]);
    renderRoute("/rooms/r1");
    await screen.findByText("Vens hoje?");

    expect(within(group()).getByText("Sim")).toBeInTheDocument();
    expect(within(group()).queryByRole("button")).toBeNull();
    expect(within(group()).getByText("Your question")).toBeInTheDocument();
    expect(posts(calls)).toHaveLength(0);
  });

  it("shows a reader who may not post the labels and why", async () => {
    const { calls } = roomApi({ my_level: "view" }, [question()]);
    renderRoute("/rooms/r1");
    await screen.findByText("Vens hoje?");

    expect(within(group()).getByText("Sim")).toBeInTheDocument();
    expect(within(group()).queryByRole("button")).toBeNull();
    expect(
      within(group()).getByText("You need permission to post in this conversation"),
    ).toBeInTheDocument();
    expect(posts(calls)).toHaveLength(0);
  });

  it("shows a frozen conversation the labels and why", async () => {
    const frozen: Partial<Room> = {
      kind: "dm",
      my_level: "post",
      participants: [
        makeIntelligence(),
        makeIntelligence({
          id: "p1",
          role: "person",
          platform: "whatsapp",
          display_name: "Lia Costa",
          handle: "lia",
        }),
      ],
    };
    const { calls } = roomApi(frozen, [question()]);
    renderRoute("/rooms/r1");
    await screen.findByTestId("history-notice");
    await screen.findByText("Vens hoje?");

    expect(within(group()).getByText("Sim")).toBeInTheDocument();
    expect(within(group()).queryByRole("button")).toBeNull();
    expect(within(group()).getByText("This conversation is in history")).toBeInTheDocument();
    expect(posts(calls)).toHaveLength(0);
  });
});

describe("TestAFailedAnswerComesBack", () => {
  it("says the answer could not be sent and lets the buttons act again", async () => {
    const { calls } = roomApi({ my_level: "post" }, [question()], {
      "POST /rooms/r1/messages": apiError(500, "internal"),
    });
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    await screen.findByText("Vens hoje?");

    await user.click(button("Não"));

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith("Could not send your answer. Try again."),
    );
    expect(button("Não")).not.toHaveAttribute("aria-disabled", "true");

    await user.click(button("Sim"));
    await waitFor(() => expect(posts(calls)).toHaveLength(2));
  });

  it("refreshes the timeline on a second answer and says the viewer already answered", async () => {
    const { calls } = roomApi({ my_level: "post" }, [question()], {
      "POST /rooms/r1/messages": apiError(409, "already_answered"),
    });
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    await screen.findByText("Vens hoje?");
    const gets = () =>
      calls.filter((c) => c.method === "GET" && c.path === "/rooms/r1/messages").length;
    await waitFor(() => expect(gets()).toBe(1));

    await user.click(button("Não"));

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith("You already answered this question"),
    );
    await waitFor(() => expect(gets()).toBe(2));
    expect(button("Não")).not.toHaveAttribute("aria-disabled", "true");
  });
});

describe("TestTheRowIsNotGrouped", () => {
  it("gives the question its own row with its author, and the answer its own", async () => {
    const earlier = makeMessage({ seq: 1, author_id: "u2", author_name: "Bob Lima", author: bob });
    const asked = question({ seq: 2, id: "m-r1-2", text: "Vens hoje?" });
    const replied = answer({ seq: 3, reply_to: "m-r1-2" });
    roomApi({ my_level: "post" }, [earlier, asked, replied]);
    renderRoute("/rooms/r1");
    await screen.findAllByText("Vens hoje?");

    const row = (seq: number) => screen.getByTestId(`message-${seq}`);
    // The question, right after a message of the same author, keeps its own header (not grouped).
    expect(within(row(2)).getByTestId("intelligence-name")).toHaveTextContent("Bob Lima");
    expect(within(row(2)).getByText("Vens hoje?")).toBeInTheDocument();
    // And the answer is a row of its own, with its author and its text.
    expect(within(row(3)).getByTestId("intelligence-name")).toHaveTextContent("Ana");
    expect(within(row(3)).getByText("Sim")).toBeInTheDocument();
  });
});
