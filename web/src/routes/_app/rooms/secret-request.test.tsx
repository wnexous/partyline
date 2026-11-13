import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { axe } from "vitest-axe";
import * as matchers from "vitest-axe/matchers";
import type { AIDetail, Message, Room } from "@/shared/api/types.gen";
import { apiError, json, mockApi } from "@/test/fetch";
import {
  makeAI,
  makeIntelligence,
  makeMe,
  makeMessage,
  makeProfile,
  makeRoom,
} from "@/test/fixtures";
import { mouseDevice } from "@/test/gestures";
import { stubLayout } from "@/test/layout";
import { useLiveStore } from "@/test/live-store";
import { renderRoute } from "@/test/render";

/*
 * The card of an AI's secret request (SR-04): who sees the field and who reads that they are waiting for
 * someone else, what Save posts, that the field never keeps the value, and the sentence of each outcome.
 */

expect.extend(matchers);

vi.mock("@/app/live", () => ({ startLive: vi.fn(() => () => {}) }));
const toast = vi.hoisted(() => Object.assign(vi.fn(), { error: vi.fn() }));
vi.mock("sonner", async (importOriginal) => ({ ...(await importOriginal<object>()), toast }));

const atlas = makeAI();
const REQUEST_ID = "m-r1-1";
const VALUE = "example-value-123";

/** Atlas's request for EXAMPLE_API_KEY, with its reason as the text. */
function request(over: Partial<Message> = {}): Message {
  return makeMessage({
    seq: 1,
    author_kind: "agent",
    author_id: atlas.id,
    author_name: "Atlas",
    author: atlas,
    text: "To send the weekly report by email.",
    interactive: { v: 1, secret: { name: "EXAMPLE_API_KEY" } },
    ...over,
  });
}

/** The answer that saved it, by Bia. */
function saved(over: Partial<Message> = {}): Message {
  return makeMessage({
    seq: 2,
    author_id: "u3",
    author_name: "Bia",
    author: makeIntelligence({ id: "u3", display_name: "Bia", handle: "bia" }),
    text: "🔑 EXAMPLE_API_KEY saved",
    reply_to: REQUEST_ID,
    answers: { v: 1, secret: { name: "EXAMPLE_API_KEY", saved: true } },
    ...over,
  });
}

/** Atlas's profile, managed by the identities given (its instructed_by). */
function atlasProfile(managers: string[]) {
  return makeProfile({
    ...atlas,
    ai: {
      owner: makeIntelligence(),
      folder: "/work/atlas",
      folder_label: "~/atlas",
      app: "claude",
      online: true,
      sessions: [],
      instructed_by: managers.map((id) => makeIntelligence({ id })),
    } as AIDetail,
  });
}

function roomApi(
  {
    room = {},
    messages = [request()],
    managers = ["u1"],
  }: { room?: Partial<Room>; messages?: Message[]; managers?: string[] } = {},
  extra: Parameters<typeof mockApi>[0] = {},
) {
  return mockApi({
    "GET /me": json(200, makeMe()),
    "GET /rooms/r1": json(200, makeRoom({ id: "r1", name: "ops", my_level: "post", ...room })),
    "GET /rooms/r1/members": json(200, { items: [] }),
    "GET /rooms/r1/messages": json(200, { items: [...messages].reverse(), has_more: false }),
    "GET /intelligences/a1": json(200, atlasProfile(managers)),
    ...extra,
  });
}

const card = () => screen.getByRole("region", { name: "Secret requested: EXAMPLE_API_KEY" });
const field = () => within(card()).getByLabelText("Value of EXAMPLE_API_KEY");
const saves = (calls: ReturnType<typeof mockApi>["calls"]) =>
  calls.filter((c) => c.method === "POST" && c.path === `/rooms/r1/messages/${REQUEST_ID}/secret`);

stubLayout();
beforeEach(() => {
  useLiveStore.setState({ locale: "en" });
  mouseDevice();
  toast.mockClear();
  toast.error.mockClear();
});
afterEach(() => vi.unstubAllGlobals());

describe("TestAManagerIsOfferedTheField", () => {
  it("shows the name, the reason and a masked field that password managers are told to leave alone", async () => {
    roomApi();
    renderRoute("/rooms/r1");
    await screen.findByText("To send the weekly report by email.");
    expect(within(card()).getByText("EXAMPLE_API_KEY")).toBeInTheDocument();
    const input = await waitFor(field);
    expect(input).toHaveAttribute("type", "password");
    expect(input).toHaveAttribute("autocomplete", "off");
    expect(input).toHaveAccessibleDescription(/never appears in the conversation/);
    expect(within(card()).getByRole("button", { name: "Save" })).toHaveAttribute(
      "aria-disabled",
      "true",
    );
    expect(within(card()).queryByText(/Waiting for/)).not.toBeInTheDocument();
  });

  it("has no axe violations with the field offered", async () => {
    roomApi();
    renderRoute("/rooms/r1");
    await waitFor(field);
    expect(
      await axe(card(), { rules: { "color-contrast": { enabled: false } } }),
    ).toHaveNoViolations();
  });

  it("shows and hides the value with the toggle", async () => {
    roomApi();
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    const input = await waitFor(field);
    await user.click(within(card()).getByRole("button", { name: "Show the value" }));
    expect(input).toHaveAttribute("type", "text");
    await user.click(within(card()).getByRole("button", { name: "Hide the value" }));
    expect(input).toHaveAttribute("type", "password");
  });
});

describe("TestOthersWaitForAManager", () => {
  it("shows a member who does not manage the AI who may give it, without a field", async () => {
    roomApi({ managers: ["u9"] });
    renderRoute("/rooms/r1");
    await waitFor(() =>
      expect(
        within(card()).getByText("Waiting for someone who manages @atlas"),
      ).toBeInTheDocument(),
    );
    expect(within(card()).queryByRole("textbox")).not.toBeInTheDocument();
    expect(within(card()).queryByLabelText("Value of EXAMPLE_API_KEY")).not.toBeInTheDocument();
  });

  it("offers no field to a manager who may only read the conversation", async () => {
    const { calls } = roomApi({ room: { my_level: "view" } });
    renderRoute("/rooms/r1");
    await waitFor(() =>
      expect(
        within(card()).getByText("Waiting for someone who manages @atlas"),
      ).toBeInTheDocument(),
    );
    expect(within(card()).queryByLabelText("Value of EXAMPLE_API_KEY")).not.toBeInTheDocument();
    expect(calls.some((c) => c.path === "/intelligences/a1")).toBe(false);
  });
});

describe("TestSaveGivesTheValueOnce", () => {
  it("posts the value once, empties the field at once, and says who saved it", async () => {
    let resolve!: (response: Response) => void;
    const { calls } = roomApi(
      {},
      {
        [`POST /rooms/r1/messages/${REQUEST_ID}/secret`]: () =>
          new Promise<Response>((r) => {
            resolve = r;
          }),
      },
    );
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    await user.type(await waitFor(field), VALUE);
    await user.click(within(card()).getByRole("button", { name: "Save" }));

    expect(saves(calls)).toHaveLength(1);
    expect(saves(calls)[0]?.body).toEqual({ value: VALUE });
    expect(field()).toHaveValue("");
    expect(within(card()).getByRole("button", { name: "Saving…" })).toHaveAttribute(
      "aria-disabled",
      "true",
    );
    await user.click(within(card()).getByRole("button", { name: "Saving…" }));
    expect(saves(calls)).toHaveLength(1);

    await act(async () => {
      resolve(
        json(
          201,
          saved({
            author_id: "u1",
            author_name: "Ana",
            author: makeIntelligence({ display_name: "Ana" }),
          }),
        ),
      );
    });
    await waitFor(() =>
      expect(within(card()).getByRole("status")).toHaveTextContent("Saved by Ana"),
    );
    expect(within(card()).queryByLabelText("Value of EXAMPLE_API_KEY")).not.toBeInTheDocument();
    expect(document.body.textContent).not.toContain(VALUE);
  });

  it("shows a request someone saved as saved, by them, to everyone", async () => {
    roomApi({ messages: [request(), saved()] });
    renderRoute("/rooms/r1");
    await waitFor(() =>
      expect(within(card()).getByRole("status")).toHaveTextContent("Saved by Bia"),
    );
    expect(within(card()).queryByLabelText("Value of EXAMPLE_API_KEY")).not.toBeInTheDocument();
  });
});

describe("TestAFailedSaveSaysWhy", () => {
  for (const [code, status, sentence] of [
    ["ai_offline", 409, "@atlas is not online; try again when it is"],
    ["client_outdated", 409, "@atlas’s Partyline is too old to receive it; it must update"],
    ["not_saved", 504, "@atlas did not save it. Try again."],
    ["already_answered", 409, "Someone already saved this secret"],
    ["internal", 500, "Could not save it. Try again."],
  ] as const) {
    it(`says what ${code} means and keeps the field, empty`, async () => {
      roomApi(
        {},
        {
          [`POST /rooms/r1/messages/${REQUEST_ID}/secret`]: apiError(
            status,
            code,
            "the server's words",
          ),
        },
      );
      const user = userEvent.setup();
      renderRoute("/rooms/r1");
      await user.type(await waitFor(field), VALUE);
      await user.click(within(card()).getByRole("button", { name: "Save" }));
      await waitFor(() => expect(within(card()).getByRole("alert")).toHaveTextContent(sentence));
      expect(field()).toHaveValue("");
      expect(field()).toHaveAttribute("aria-invalid", "true");
      expect(toast.error).not.toHaveBeenCalled();
      expect(document.body.textContent).not.toContain(VALUE);
    });
  }

  it("refuses a value over 16384 bytes before posting it", async () => {
    const { calls } = roomApi();
    renderRoute("/rooms/r1");
    const input = await waitFor(field);
    const user = userEvent.setup();
    // Typing 16 kB key by key is slow; a paste is what a person does.
    await user.click(input);
    await user.paste("é".repeat(8193));
    await user.click(within(card()).getByRole("button", { name: "Save" }));
    expect(within(card()).getByRole("alert")).toHaveTextContent(
      "The value is longer than 16384 bytes",
    );
    expect(saves(calls)).toHaveLength(0);
    expect(field()).toHaveValue("");
  });
});
