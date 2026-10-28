import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Message, MessageAnswerItem, Room, WizardQuestion } from "@/shared/api/types.gen";
import { handleLiveEvent } from "@/lib/live/handlers";
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

describe("TestADeletedAnswerIsAnswerableAgain", () => {
  it("lets the quick replies act again when the answer is deleted live", async () => {
    const stored = answer();
    const { calls } = roomApi({ my_level: "post" }, [question()], {
      "POST /rooms/r1/messages": json(201, stored),
    });
    const user = userEvent.setup();
    const { queryClient } = renderRoute("/rooms/r1");
    await screen.findByText("Vens hoje?");

    await user.click(button("Sim"));
    await waitFor(() => expect(status()).toHaveTextContent("Answer sent: Sim"));

    // The stored answer arrives live: the timeline now holds it.
    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "room.message",
        data: stored,
      }),
    );
    await waitFor(() => expect(button("Sim")).toHaveAttribute("aria-pressed", "true"));

    // Its author deletes it: the delete arrives live and the question must be answerable again.
    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "room.message_updated",
        data: { ...stored, text: "", answers: undefined, deleted_at: "2026-10-19T14:00:00Z" },
      }),
    );

    await waitFor(() => expect(button("Sim")).not.toHaveAttribute("aria-disabled", "true"));
    expect(status()).toBeNull();
    await user.click(button("Não"));
    await waitFor(() => expect(posts(calls)).toHaveLength(2));
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

/*
 * The wizard half of the bubble (QR-14..QR-17): one question per step, its validity at the step
 * gate, the focus it moves, the one submission it posts and the summary it leaves behind. The test
 * values are the design's sample wizard: "Quando?" (single) and "O que entra?" (multi, with a note).
 */

const WIZARD_TEXT = "Planejar a entrega";
const QUANDO = single("q1", "Quando?", ["Amanhã", "Na sexta"]);
const O_QUE_ENTRA = multi("q2", "O que entra?", ["API", "Web"], true);

/** One question of a stored wizard, with the server's ids in the order the labels were given. */
function single(id: string, title: string, labels: string[], allowText = false): WizardQuestion {
  return {
    id,
    title,
    kind: "single",
    allow_text: allowText,
    options: labels.map((label, index) => ({ id: `${id}o${index + 1}`, label })),
  };
}

function multi(id: string, title: string, labels: string[], allowText = false): WizardQuestion {
  return {
    ...single(id, title, labels, allowText),
    kind: "multi",
  };
}

/** A question message that carries a wizard. */
function wizardMessage(questions: WizardQuestion[], over: Partial<Message> = {}): Message {
  return makeMessage({
    seq: 1,
    author_id: "u2",
    author_name: "Bob Lima",
    author: bob,
    text: WIZARD_TEXT,
    interactive: { v: 1, wizard: { questions } },
    ...over,
  });
}

/** The viewer's stored answer to the wizard, as the timeline holds it after a reload. */
function wizardAnswer(items: MessageAnswerItem[], over: Partial<Message> = {}): Message {
  return makeMessage({
    seq: 2,
    text: items.map((item) => item.question).join("\n"),
    reply_to: QUESTION_ID,
    answers: { v: 1, answers: items },
    ...over,
  });
}

const row = () => within(screen.getByTestId("message-1"));
const next = () => row().getByRole("button", { name: "Next" });
const send = () => row().getByRole("button", { name: "Send" });
const step = (current: number, total: number) =>
  expect(row().getByText(`Step ${current} of ${total}`)).toBeInTheDocument();

const deletedAt = "2026-10-19T14:00:00Z";

describe("TestTwoWizardsKeepTheirOwnChoices", () => {
  it("keeps the first wizard's choice and answer when the second one is picked", async () => {
    const asked = [single("q1", "Quando?", ["Amanhã", "Na sexta"])];
    const first = wizardMessage(asked, { seq: 1 });
    const second = wizardMessage(asked, { seq: 2 });
    const { calls } = roomApi({ my_level: "post" }, [first, second]);
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    await screen.findAllByText(WIZARD_TEXT);

    const firstRow = within(screen.getByTestId("message-1"));
    const secondRow = within(screen.getByTestId("message-2"));
    // A radio group is the inputs that share a name: the same question id in two messages may not
    // put both wizards in one group.
    const names = (scope: ReturnType<typeof within>) =>
      new Set(scope.getAllByRole("radio").map((radio: HTMLElement) => radio.getAttribute("name")));
    const firstNames = names(firstRow);
    for (const name of names(secondRow)) expect(firstNames.has(name)).toBe(false);

    await user.click(firstRow.getByRole("radio", { name: "Amanhã" }));
    await user.click(secondRow.getByRole("radio", { name: "Na sexta" }));

    // The two wizards are separate radio groups: the second pick leaves the first choice alone.
    expect(firstRow.getByRole("radio", { name: "Amanhã" })).toBeChecked();
    expect(firstRow.getByRole("radio", { name: "Na sexta" })).not.toBeChecked();
    expect(secondRow.getByRole("radio", { name: "Na sexta" })).toBeChecked();
    expect(secondRow.getByRole("radio", { name: "Amanhã" })).not.toBeChecked();

    await user.click(firstRow.getByRole("button", { name: "Send" }));
    await waitFor(() => expect(posts(calls)).toHaveLength(1));
    expect(posts(calls)[0]?.body).toEqual({
      text: "",
      reply_to: "m-r1-1",
      answers: { items: [{ question_id: "q1", option_ids: ["q1o1"] }] },
    });
  });
});

describe("TestStepsAndValidity", () => {
  it("keeps Next and Send disabled until the step is valid, and shows Send on the last step", async () => {
    roomApi({ my_level: "post" }, [
      wizardMessage([QUANDO, O_QUE_ENTRA, single("q3", "Onde?", ["Casa", "Escritório"])]),
    ]);
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    await screen.findByText(WIZARD_TEXT);

    step(1, 3);
    expect(next()).toBeDisabled();
    await user.click(row().getByRole("radio", { name: "Amanhã" }));
    expect(next()).toBeEnabled();

    await user.click(next());
    step(2, 3);
    expect(row().getByRole("heading", { name: "O que entra?" })).toBeInTheDocument();
    expect(next()).toBeDisabled();
    await user.click(row().getByRole("checkbox", { name: "API" }));
    expect(next()).toBeEnabled();

    await user.click(next());
    step(3, 3);
    expect(send()).toBeDisabled();
    await user.click(row().getByRole("radio", { name: "Casa" }));
    expect(send()).toBeEnabled();
  });
});

describe("TestOneSubmissionWithAllAnswers", () => {
  it("posts every step in one answer when Send is pressed", async () => {
    const stored = wizardAnswer([
      { question: "Quando?", values: ["Na sexta"], text: "" },
      { question: "O que entra?", values: ["API", "Web"], text: "antes do almoço" },
    ]);
    const { calls } = roomApi({ my_level: "post" }, [wizardMessage([QUANDO, O_QUE_ENTRA])], {
      "POST /rooms/r1/messages": json(201, stored),
    });
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    await screen.findByText(WIZARD_TEXT);

    await user.click(row().getByRole("radio", { name: "Na sexta" }));
    await user.click(next());
    await user.click(row().getByRole("checkbox", { name: "API" }));
    await user.click(row().getByRole("checkbox", { name: "Web" }));
    await user.type(row().getByRole("textbox", { name: "Note (optional)" }), "antes do almoço");
    await user.click(send());

    await waitFor(() => expect(posts(calls)).toHaveLength(1));
    expect(posts(calls)[0]?.body).toEqual({
      text: "",
      reply_to: QUESTION_ID,
      answers: {
        items: [
          { question_id: "q1", option_ids: ["q1o2"] },
          { question_id: "q2", option_ids: ["q2o1", "q2o2"], text: "antes do almoço" },
        ],
      },
    });
  });
});

describe("TestFocusFollowsTheWizard", () => {
  it("focuses the new step heading and then the summary of the sent answers", async () => {
    const stored = wizardAnswer([
      { question: "Quando?", values: ["Na sexta"], text: "" },
      { question: "O que entra?", values: ["API", "Web"], text: "antes do almoço" },
    ]);
    roomApi({ my_level: "post" }, [wizardMessage([QUANDO, O_QUE_ENTRA])], {
      "POST /rooms/r1/messages": json(201, stored),
    });
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    await screen.findByText(WIZARD_TEXT);

    await user.click(row().getByRole("radio", { name: "Na sexta" }));
    await user.click(next());
    await waitFor(() => expect(row().getByRole("heading", { name: "O que entra?" })).toHaveFocus());

    await user.click(row().getByRole("checkbox", { name: "API" }));
    await user.click(row().getByRole("checkbox", { name: "Web" }));
    await user.type(row().getByRole("textbox", { name: "Note (optional)" }), "antes do almoço");
    await user.click(send());

    await waitFor(() => expect(row().getByRole("heading", { name: "Your answer" })).toHaveFocus());
    expect(row().getByText("Quando?")).toBeInTheDocument();
    expect(row().getByText("Na sexta")).toBeInTheDocument();
    expect(row().getByText("O que entra?")).toBeInTheDocument();
    expect(row().getByText("API, Web (antes do almoço)")).toBeInTheDocument();
  });
});

describe("TestATextOnlySingleAnswerIsValid", () => {
  it("lets a single question with allow_text be answered by the text alone", async () => {
    const stored = wizardAnswer([{ question: "Quando?", values: [], text: "depois das 10h" }]);
    const { calls } = roomApi(
      { my_level: "post" },
      [wizardMessage([single("q1", "Quando?", ["Amanhã", "Na sexta"], true)])],
      {
        "POST /rooms/r1/messages": json(201, stored),
      },
    );
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    await screen.findByText(WIZARD_TEXT);

    expect(send()).toBeDisabled();
    await user.type(
      row().getByRole("textbox", { name: "Your answer (or pick an option)" }),
      "depois das 10h",
    );
    expect(send()).toBeEnabled();
    await user.click(send());

    await waitFor(() => expect(posts(calls)).toHaveLength(1));
    expect(posts(calls)[0]?.body).toEqual({
      text: "",
      reply_to: QUESTION_ID,
      answers: {
        items: [{ question_id: "q1", option_ids: [], text: "depois das 10h" }],
      },
    });
  });
});

describe("TestAFailedSendComesBack", () => {
  it("returns the editable step and the failure toast when the answer is not stored", async () => {
    roomApi({ my_level: "post" }, [wizardMessage([QUANDO])], {
      "POST /rooms/r1/messages": apiError(500, "internal"),
    });
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    await screen.findByText(WIZARD_TEXT);

    await user.click(row().getByRole("radio", { name: "Na sexta" }));
    await user.click(send());

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith("Could not send your answer. Try again."),
    );
    expect(row().getByRole("heading", { name: "Quando?" })).toBeInTheDocument();
    expect(row().queryByRole("heading", { name: "Your answer" })).toBeNull();
    expect(send()).toBeEnabled();
    expect(row().getByRole("radio", { name: "Na sexta" })).toBeChecked();
  });
});

describe("TestAnAnsweredWizardIsReadOnly", () => {
  it("shows the stored answer as the summary and offers no step control", async () => {
    const stored = wizardAnswer([
      { question: "Quando?", values: ["Na sexta"], text: "" },
      { question: "O que entra?", values: ["API", "Web"], text: "antes do almoço" },
    ]);
    roomApi({ my_level: "post" }, [wizardMessage([QUANDO, O_QUE_ENTRA]), stored]);
    renderRoute("/rooms/r1");
    await screen.findAllByText(WIZARD_TEXT);

    expect(await row().findByRole("heading", { name: "Your answer" })).toBeInTheDocument();
    expect(row().getByText("Na sexta")).toBeInTheDocument();
    expect(row().getByText("API, Web (antes do almoço)")).toBeInTheDocument();
    expect(row().queryByRole("button", { name: "Next" })).toBeNull();
    expect(row().queryByRole("button", { name: "Send" })).toBeNull();
    expect(row().queryByRole("radio")).toBeNull();
    expect(row().queryByRole("checkbox")).toBeNull();
  });
});

describe("TestADeletedWizardAnswerIsAnswerableAgain", () => {
  it("returns the step controls when the stored answer is deleted live", async () => {
    const stored = wizardAnswer([{ question: "Quando?", values: ["Na sexta"], text: "" }]);
    roomApi({ my_level: "post" }, [wizardMessage([QUANDO])], {
      "POST /rooms/r1/messages": json(201, stored),
    });
    const user = userEvent.setup();
    const { queryClient } = renderRoute("/rooms/r1");
    await screen.findByText(WIZARD_TEXT);

    await user.click(row().getByRole("radio", { name: "Na sexta" }));
    await user.click(send());
    await waitFor(() =>
      expect(row().getByRole("heading", { name: "Your answer" })).toBeInTheDocument(),
    );

    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "room.message",
        data: stored,
      }),
    );
    await waitFor(() =>
      expect(row().getByRole("heading", { name: "Your answer" })).toBeInTheDocument(),
    );

    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "room.message_updated",
        data: { ...stored, text: "", answers: undefined, deleted_at: deletedAt },
      }),
    );

    await waitFor(() => expect(row().getByRole("radio", { name: "Na sexta" })).toBeInTheDocument());
    expect(row().queryByRole("heading", { name: "Your answer" })).toBeNull();
    expect(row().getByRole("heading", { name: "Quando?" })).toBeInTheDocument();
    expect(send()).toBeEnabled();
  });
});

describe("TestSendKeepsFocus", () => {
  it("keeps focus on Send while the answer is sent and after it fails", async () => {
    let resolve!: (response: Response) => void;
    const pending = new Promise<Response>((r) => {
      resolve = r;
    });
    const { calls } = roomApi({ my_level: "post" }, [wizardMessage([QUANDO])], {
      "POST /rooms/r1/messages": () => pending,
    });
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    await screen.findByText(WIZARD_TEXT);
    await user.click(row().getByRole("radio", { name: "Na sexta" }));

    const control = send();
    control.focus();
    await user.keyboard("{Enter}");

    expect(posts(calls)).toHaveLength(1);
    // A real disabled control loses focus to the body; aria-disabled keeps the pressed control
    // focusable and the guard is what stops a second send.
    await waitFor(() => expect(control).toHaveAttribute("aria-disabled", "true"));
    expect(control).not.toBeDisabled();
    expect(control).toHaveFocus();

    await act(async () => {
      resolve(apiError(500, "internal"));
    });
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith("Could not send your answer. Try again."),
    );
    expect(control).toHaveFocus();
  });
});

describe("TestAnAnsweredWizardDoesNotTakeFocus", () => {
  it("leaves focus alone when the room loads an old answered wizard", async () => {
    const stored = wizardAnswer([{ question: "Quando?", values: ["Na sexta"], text: "" }]);
    roomApi({ my_level: "post" }, [wizardMessage([QUANDO]), stored]);
    renderRoute("/rooms/r1");

    const heading = await screen.findByRole("heading", { name: "Your answer" });
    expect(heading).not.toHaveFocus();
    expect(document.activeElement).toBe(document.body);
  });
});

describe("TestTheNoteIsOutsideTheRadioGroup", () => {
  it("keeps the radios alone in the group and the note after it", async () => {
    roomApi({ my_level: "post" }, [
      wizardMessage([single("q1", "Quando?", ["Amanhã", "Na sexta"], true)]),
    ]);
    renderRoute("/rooms/r1");
    await screen.findByText(WIZARD_TEXT);

    const radios = row().getByRole("radiogroup");
    expect(within(radios).getAllByRole("radio")).toHaveLength(2);
    expect(within(radios).queryByRole("textbox")).toBeNull();
    expect(
      row().getByRole("textbox", { name: "Your answer (or pick an option)" }),
    ).toBeInTheDocument();
  });
});

describe("TestTheNoteLabelSaysItCanBeTheAnswer", () => {
  it("labels the field of a single question with allow_text as the answer in Portuguese", async () => {
    roomApi(
      { my_level: "post" },
      [wizardMessage([single("q1", "Quando?", ["Amanhã", "Na sexta"], true)])],
      { "GET /me": json(200, makeMe({ locale: "pt-BR" })) },
    );
    renderRoute("/rooms/r1");
    await screen.findByText(WIZARD_TEXT);

    expect(
      row().getByRole("textbox", { name: "Sua resposta (ou escolha uma opção)" }),
    ).toBeInTheDocument();
    expect(row().queryByRole("textbox", { name: "Observação (opcional)" })).toBeNull();
  });
});

describe("TestEightQuestionsWithLongLabels", () => {
  it("renders all eight questions and each of their eight labelled options", async () => {
    const longLabel = (question: number, option: number) =>
      `Q${question}O${option} ${"long label ".repeat(8)}`.slice(0, 48);
    const questions = Array.from({ length: 8 }, (_, question) =>
      single(
        `q${question + 1}`,
        `Question ${question + 1}`,
        Array.from({ length: 8 }, (_, option) => longLabel(question + 1, option + 1)),
      ),
    );
    roomApi({ my_level: "post" }, [wizardMessage(questions)]);
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    await screen.findByText(WIZARD_TEXT);

    for (const [index, question] of questions.entries()) {
      expect(row().getByRole("heading", { name: question.title })).toBeInTheDocument();
      for (const option of question.options) {
        expect(row().getByRole("radio", { name: option.label })).toBeInTheDocument();
        expect(row().getByText(option.label)).toBeInTheDocument();
      }
      await user.click(row().getByRole("radio", { name: question.options[0]?.label ?? "" }));
      if (index < questions.length - 1) await user.click(next());
    }
    expect(send()).toBeEnabled();
  });
});

describe("TestKeyboardOnly", () => {
  it("selects the first option with Space, reaches Next with Tab and moves on with Enter", async () => {
    roomApi({ my_level: "post" }, [wizardMessage([QUANDO, O_QUE_ENTRA])]);
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    await screen.findByText(WIZARD_TEXT);

    const first = row().getByRole("radio", { name: "Amanhã" });
    for (let presses = 0; presses < 100 && document.activeElement !== first; presses += 1) {
      await user.tab();
    }
    expect(first).toHaveFocus();
    await user.keyboard(" ");
    expect(first).toBeChecked();

    for (let presses = 0; presses < 20 && document.activeElement !== next(); presses += 1) {
      await user.tab();
    }
    expect(next()).toHaveFocus();
    await user.keyboard("{Enter}");
    await waitFor(() => expect(row().getByRole("heading", { name: "O que entra?" })).toHaveFocus());
  });
});
