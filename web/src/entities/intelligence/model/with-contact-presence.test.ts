import { describe, expect, it } from "vitest";
import { makeAIContact } from "@/test/fixtures";
import { withContactPresence } from "./presence";

/** Live presence over the contacts: the socket's word wins over what was fetched. */

describe("withContactPresence", () => {
  it("keeps the contact as fetched when the socket said nothing about it", () => {
    const contact = makeAIContact({ online: true });
    expect(withContactPresence(contact, undefined)).toBe(contact);
  });

  it("shows an AI that came online since the contacts were fetched", () => {
    const contact = makeAIContact({ online: false });
    const live = withContactPresence(contact, { online: true });
    expect(live.online).toBe(true);
    expect(contact.online).toBe(false);
  });

  it("shows an AI that went offline, with when it was last seen", () => {
    const contact = makeAIContact({ online: true });
    const live = withContactPresence(contact, {
      online: false,
      last_seen_at: "2026-10-21T10:00:00Z",
    });
    expect(live.online).toBe(false);
    expect(live.last_seen_at).toBe("2026-10-21T10:00:00Z");
  });

  it("keeps the fetched last seen when the frame carries none", () => {
    const contact = makeAIContact({ last_seen_at: "2026-10-20T08:00:00Z" });
    expect(withContactPresence(contact, { online: true }).last_seen_at).toBe(
      "2026-10-20T08:00:00Z",
    );
  });

  it("returns the same object when the frame changes nothing, so a render is not repeated", () => {
    const contact = makeAIContact({ online: true, last_seen_at: "2026-10-21T10:00:00Z" });
    expect(
      withContactPresence(contact, { online: true, last_seen_at: "2026-10-21T10:00:00Z" }),
    ).toBe(contact);
  });
});
