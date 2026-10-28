import { describe, expect, it } from "vitest";
import { profileEditSchema } from "@/features/edit-profile/model/schema";
import { loginSchema } from "@/pages/auth/model/schema";
import { acceptInviteSchema } from "@/pages/invite/model/schema";
import { setupSchema } from "@/pages/setup/model/schema";
import { normalizeHandle, passwordSchema, usernameSchema } from "@/shared/lib/validation";

const firstMessage = (r: { success: boolean; error?: { issues: { message: string }[] } }) =>
  r.error?.issues[0]?.message;

describe("username rule (UI-03, same as API)", () => {
  it.each(["ab", "UPPER", "has space", "a".repeat(33), "dots.no", ""])("rejects %j", (value) => {
    const r = usernameSchema.safeParse(value);
    expect(r.success).toBe(false);
    expect(firstMessage(r)).toBe("validation.username");
  });

  it.each(["abc", "ana_dev", "bot-01", "a".repeat(32)])("accepts %j", (value) => {
    expect(usernameSchema.safeParse(value).success).toBe(true);
  });
});

describe("password rule (UI-03, same as API)", () => {
  it("rejects a 9-character password", () => {
    const r = passwordSchema.safeParse("123456789");
    expect(r.success).toBe(false);
    expect(firstMessage(r)).toBe("validation.passwordMin");
  });

  it("accepts 10 characters", () => {
    expect(passwordSchema.safeParse("1234567890").success).toBe(true);
  });
});

describe("form schemas", () => {
  it("setup requires the token and applies the account rules (UI-01)", () => {
    const r = setupSchema.safeParse({ token: " ", username: "UPPER", password: "short" });
    expect(r.success).toBe(false);
    const paths = r.error?.issues.map((i) => i.path.join(".")).sort();
    expect(paths).toEqual(["password", "token", "username"]);
  });

  it("invite accept drops an empty display name", () => {
    const r = acceptInviteSchema.parse({
      username: "ana",
      password: "correct horse",
      display_name: "  ",
    });
    expect(r).toEqual({ username: "ana", password: "correct horse", display_name: undefined });
  });

  it("login only checks presence (UI-02)", () => {
    expect(loginSchema.safeParse({ username: "UPPER", password: "x" }).success).toBe(true);
    expect(firstMessage(loginSchema.safeParse({ username: "", password: "x" }))).toBe(
      "validation.required",
    );
  });
});

describe("profile rules (PROF-02, PROF-03, same as API)", () => {
  const parse = (over: Partial<Record<"display_name" | "handle" | "bio", string>> = {}) =>
    profileEditSchema.safeParse({ display_name: "Atlas", handle: "atlas", bio: "", ...over });
  const issues = (r: ReturnType<typeof parse>) =>
    r.success ? [] : r.error.issues.map((i) => [i.path[0], i.message]);

  it("accepts a plain profile and trims the name and the bio", () => {
    const r = parse({ display_name: "  Atlas Prime  ", bio: "  Ships.  " });
    expect(r.success && r.data).toEqual({
      display_name: "Atlas Prime",
      handle: "atlas",
      bio: "Ships.",
    });
  });

  it("needs a name, counted after trimming", () => {
    expect(issues(parse({ display_name: "" }))).toEqual([["display_name", "validation.required"]]);
    expect(issues(parse({ display_name: "   " }))).toEqual([
      ["display_name", "validation.required"],
    ]);
  });

  it("allows 64 characters of name and refuses 65, counting code points", () => {
    expect(parse({ display_name: "n".repeat(64) }).success).toBe(true);
    expect(issues(parse({ display_name: "n".repeat(65) }))).toEqual([
      ["display_name", "validation.displayName"],
    ]);
    // An emoji is one character, however many UTF-16 units it takes.
    expect(parse({ display_name: "😀".repeat(64) }).success).toBe(true);
    expect(parse({ display_name: "😀".repeat(65) }).success).toBe(false);
  });

  it("allows 280 characters of bio and refuses 281, counting code points", () => {
    expect(parse({ bio: "x".repeat(280) }).success).toBe(true);
    expect(issues(parse({ bio: "x".repeat(281) }))).toEqual([["bio", "validation.bio"]]);
    expect(parse({ bio: "😀".repeat(280) }).success).toBe(true);
    expect(parse({ bio: "😀".repeat(281) }).success).toBe(false);
  });

  it("accepts the bio empty", () => {
    expect(parse({ bio: "" }).success).toBe(true);
  });

  it.each(["a", "atlas-2", "a1", `a${"b".repeat(39)}`])("accepts the handle %j", (handle) => {
    expect(parse({ handle }).success).toBe(true);
  });

  it.each(["", "1abc", "-abc", "ab c", "ab_c", "ab.c", "ábc", `a${"b".repeat(40)}`, "@"])(
    "refuses the handle %j",
    (handle) => {
      expect(issues(parse({ handle }))).toEqual([["handle", "validation.handle"]]);
    },
  );

  it("reads the handle as the server does: without a leading @, trimmed, in lower case", () => {
    expect(normalizeHandle("  @Atlas-Two ")).toBe("atlas-two");
    expect(normalizeHandle("@@x")).toBe("x");
    const r = parse({ handle: " @Atlas " });
    expect(r.success && r.data.handle).toBe("atlas");
  });

  it("reports every broken field at once", () => {
    expect(issues(parse({ display_name: "", handle: "1", bio: "x".repeat(281) }))).toEqual([
      ["display_name", "validation.required"],
      ["handle", "validation.handle"],
      ["bio", "validation.bio"],
    ]);
  });
});
