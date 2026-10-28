import { describe, expect, it } from "vitest";
import { atReactionLimit, reactedWith, withReaction } from "@/entities/message";
import { MaxMessageEmoji, type Reaction } from "@/shared/api/types.gen";
import { MORE_REACTIONS, PICKER_COLUMNS, QUICK_REACTIONS } from "./reactions";

/*
 * The emoji a message can be reacted with, and what one reaction does to the list a message carries
 * (RCT-01, RCT-02, RCT-04, RCT-08).
 */

const ME = "u1";
const THEM = "u2";
const OTHER = "u3";

describe("the emoji at hand (RCT-02)", () => {
  it("are six, in the order a bar shows them", () => {
    expect(QUICK_REACTIONS).toEqual(["👍", "❤️", "😂", "😮", "😢", "🙏"]);
  });

  it("are not repeated in the rest, which sits beside them", () => {
    for (const emoji of QUICK_REACTIONS) expect(MORE_REACTIONS).not.toContain(emoji);
  });

  it("have no emoji twice in the rest", () => {
    expect(new Set(MORE_REACTIONS).size).toBe(MORE_REACTIONS.length);
  });

  it("are each exactly one emoji, which the server accepts (validation.OneEmoji)", () => {
    const graphemes = new Intl.Segmenter("en", { granularity: "grapheme" });
    for (const emoji of MORE_REACTIONS) {
      expect([...graphemes.segment(emoji)], emoji).toHaveLength(1);
    }
  });

  it("fill the rows of the picker, which has eight columns", () => {
    expect(PICKER_COLUMNS).toBe(8);
    expect(MORE_REACTIONS.length % PICKER_COLUMNS).toBe(0);
    expect(MORE_REACTIONS.length).toBe(48);
  });
});

describe("who reacted with what", () => {
  const list: Reaction[] = [
    { emoji: "👍", by: [THEM, ME] },
    { emoji: "🎉", by: [THEM] },
  ];

  it("says whether the viewer used an emoji", () => {
    expect(reactedWith(list, "👍", ME)).toBe(true);
    expect(reactedWith(list, "🎉", ME)).toBe(false);
    expect(reactedWith(list, "😂", ME)).toBe(false);
  });

  it("is not fooled by the emoji of someone else's reaction", () => {
    expect(reactedWith(list, "🎉", THEM)).toBe(true);
    expect(reactedWith([], "👍", ME)).toBe(false);
  });
});

describe("what a reaction does to the list (RCT-01, RCT-08)", () => {
  it("puts a new emoji at the end, with the viewer alone", () => {
    const list: Reaction[] = [{ emoji: "👍", by: [THEM] }];
    expect(withReaction(list, "❤️", ME, true)).toEqual([
      { emoji: "👍", by: [THEM] },
      { emoji: "❤️", by: [ME] },
    ]);
  });

  it("puts the viewer after the others who used the emoji, and the emoji where it was", () => {
    const list: Reaction[] = [
      { emoji: "👍", by: [THEM, OTHER] },
      { emoji: "❤️", by: [THEM] },
    ];
    expect(withReaction(list, "👍", ME, true)).toEqual([
      { emoji: "👍", by: [THEM, OTHER, ME] },
      { emoji: "❤️", by: [THEM] },
    ]);
  });

  it("changes nothing when the viewer already used the emoji", () => {
    const list: Reaction[] = [{ emoji: "👍", by: [ME] }];
    expect(withReaction(list, "👍", ME, true)).toBe(list);
  });

  it("takes the viewer out of an emoji and keeps the others in it", () => {
    const list: Reaction[] = [
      { emoji: "👍", by: [THEM, ME] },
      { emoji: "❤️", by: [ME] },
    ];
    expect(withReaction(list, "👍", ME, false)).toEqual([
      { emoji: "👍", by: [THEM] },
      { emoji: "❤️", by: [ME] },
    ]);
  });

  it("drops an emoji that nobody uses any more", () => {
    const list: Reaction[] = [
      { emoji: "👍", by: [ME] },
      { emoji: "❤️", by: [THEM] },
    ];
    expect(withReaction(list, "👍", ME, false)).toEqual([{ emoji: "❤️", by: [THEM] }]);
  });

  it("changes nothing when there is no reaction of the viewer to take back", () => {
    const list: Reaction[] = [{ emoji: "👍", by: [THEM] }];
    expect(withReaction(list, "👍", ME, false)).toBe(list);
    expect(withReaction(list, "🎉", ME, false)).toBe(list);
  });

  it("never changes the list it is given", () => {
    const list: Reaction[] = Object.freeze([
      Object.freeze({ emoji: "👍", by: Object.freeze([THEM]) as string[] }),
    ]) as Reaction[];
    expect(() => withReaction(list, "👍", ME, true)).not.toThrow();
    expect(() => withReaction(list, "👍", THEM, false)).not.toThrow();
    expect(() => withReaction(list, "❤️", ME, true)).not.toThrow();
    expect(list).toEqual([{ emoji: "👍", by: [THEM] }]);
  });
});

describe("the limit of different emoji (RCT-04)", () => {
  const full: Reaction[] = MORE_REACTIONS.slice(0, MaxMessageEmoji).map((emoji) => ({
    emoji,
    by: [THEM],
  }));

  it("is twelve, as the server's", () => {
    expect(MaxMessageEmoji).toBe(12);
  });

  it("is reached by a new emoji on a message that has twelve", () => {
    expect(atReactionLimit(full, MORE_REACTIONS[12] as string)).toBe(true);
  });

  it("is not reached by an emoji the message has already, or by a message with room", () => {
    expect(atReactionLimit(full, MORE_REACTIONS[0] as string)).toBe(false);
    expect(atReactionLimit(full.slice(1), MORE_REACTIONS[12] as string)).toBe(false);
    expect(atReactionLimit([], "👍")).toBe(false);
  });
});
