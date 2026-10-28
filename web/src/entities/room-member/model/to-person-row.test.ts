import { describe, expect, it } from "vitest";
import { makeAI, makeIntelligence, makeMember, makePerson } from "@/test/fixtures";
import { toPersonRow } from "./to-person-row";

describe("toPersonRow", () => {
  it("reads an agent member by its kind and keeps what the server said about it", () => {
    const sessions = [{ label: "", harness: "claude", connected_at: "2026-10-28T14:05:00Z" }];
    const member = makeMember({
      id: "mb-atlas",
      member_id: "a1",
      name: "Atlas",
      intelligence: makeAI(),
      online: true,
      sessions,
    });
    expect(toPersonRow(member)).toMatchObject({
      rowId: "mb-atlas",
      id: "a1",
      kind: "ai",
      name: "Atlas",
      online: true,
      sessions,
      member,
    });
  });

  it("reads a user member, and a participant by its role", () => {
    const bob = makeIntelligence({ id: "u2", display_name: "Bob Lima", handle: "bob" });
    expect(
      toPersonRow(makeMember({ id: "mb-bob", kind: "user", member_id: "u2", intelligence: bob })),
    ).toMatchObject({ kind: "user", id: "u2", intelligence: bob });
    expect(toPersonRow(bob)).toMatchObject({
      rowId: "u2",
      id: "u2",
      kind: "user",
      name: "Bob Lima",
    });
    expect(toPersonRow(makeAI())).toMatchObject({ kind: "ai", id: "a1" });
    expect(toPersonRow(makePerson())).toMatchObject({ kind: "person", id: "p1" });
  });

  it("gives a participant no presence and no member behind it", () => {
    const row = toPersonRow(makeAI());
    expect(row).not.toHaveProperty("member");
    expect(row).not.toHaveProperty("online");
    expect(row).not.toHaveProperty("sessions");
  });

  it("drops a chat, which is no intelligence", () => {
    expect(toPersonRow(makeMember({ id: "mb-ops", kind: "chat", member_id: "c1" }))).toBeNull();
  });

  it("gives a member without a profile a face of its name", () => {
    const row = toPersonRow(makeMember({ member_id: "a9", name: "Ghost" }));
    expect(row?.intelligence).toMatchObject({ id: "a9", display_name: "Ghost", role: "ai" });
  });
});
