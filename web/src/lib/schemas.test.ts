import { describe, expect, it } from "vitest";
import {
  acceptInviteSchema,
  loginSchema,
  passwordSchema,
  setupSchema,
  usernameSchema,
} from "./schemas";

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
