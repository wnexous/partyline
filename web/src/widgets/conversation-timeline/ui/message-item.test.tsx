import { screen, within } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import type { Message } from "@/shared/api/types.gen";
import { makeIntelligence, makeMessage } from "@/test/fixtures";
import { useLiveStore } from "@/test/live-store";
import { renderWithRouter } from "@/test/render";
import { MessageItem } from "./message-item";

/*
 * Which session of an AI wrote a row (SESS-30, SESS-34): "via :label" next to the author for an
 * agent message with a non-empty label, and nothing for the principal or for a person. The label is
 * a fact of the message, so it is plain text: no link, no menu, and it wraps instead of widening
 * the row.
 */

const atlas = makeIntelligence({
  id: "a1",
  kind: "ai",
  role: "ai",
  display_name: "Atlas",
  handle: "atlas",
});

/** An agent message written by Atlas; `author_label` names the session when it is not the principal. */
function agent(over: Partial<Message> = {}) {
  return makeMessage({
    author_kind: "agent",
    author_id: "a1",
    author_name: "Atlas",
    author: atlas,
    text: "deploying now",
    ...over,
  });
}

function renderItem(message: Message, grouped = false) {
  return renderWithRouter(
    <MessageItem message={message} meId="u1" grouped={grouped} onOpenImage={() => undefined} />,
  );
}

const row = (seq = 1) => screen.getByTestId(`message-${seq}`);

beforeEach(() => useLiveStore.setState({ locale: "en" }));

describe("the author's session (SESS-30)", () => {
  it("shows the label of the session that wrote an agent message next to its name", async () => {
    renderItem(agent({ author_label: "claude-2" }));
    await screen.findByText("Atlas");

    expect(within(row()).getByText("via :claude-2")).toBeInTheDocument();
  });

  it("shows nothing for the principal session", async () => {
    renderItem(agent({ author_label: "" }));
    await screen.findByText("Atlas");

    expect(within(row()).queryByText(/via :/)).toBeNull();
  });

  it("shows nothing for a person, even when a label is set", async () => {
    renderItem(makeMessage({ author_kind: "user", author_label: "claude-2" }));
    await screen.findByText("Ana");

    expect(within(row()).queryByText(/via :/)).toBeNull();
  });

  it("exposes the label to a screen reader on a grouped row", async () => {
    renderItem(agent({ author_label: "claude-2" }), true);
    await screen.findByText("deploying now");

    const spoken = row().querySelector(".sr-only");
    expect(spoken?.textContent).toContain("Atlas");
    expect(spoken?.textContent).toContain("claude-2");
  });

  it("keeps a 40-character label inside the row that holds it", async () => {
    const label = "a".repeat(40);
    renderItem(agent({ author_label: label }));
    await screen.findByText("Atlas");

    const chip = within(row()).getByText(`via :${label}`);
    // The row's text column keeps min-w-0, so the label wraps instead of widening the row.
    expect(chip.closest(".min-w-0")).not.toBeNull();
    expect(row().querySelector("div.min-w-0")).not.toBeNull();
  });
});
