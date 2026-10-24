import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError, api, setUnauthorizedHandler } from "./client";

function jsonResponse(status: number, body: unknown) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

describe("api client", () => {
  const fetchMock = vi.fn<typeof fetch>();

  beforeEach(() => {
    vi.stubGlobal("fetch", fetchMock);
    window.history.replaceState(null, "", "/rooms/r1?tab=members");
  });

  afterEach(() => {
    fetchMock.mockReset();
    vi.unstubAllGlobals();
    setUnauthorizedHandler(null);
  });

  it("prefixes /api/v1, sends JSON and decodes the response", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { id: "r1" }));
    const out = await api<{ id: string }>("POST", "/rooms", { name: "ops" });
    expect(out).toEqual({ id: "r1" });
    const [url, init] = fetchMock.mock.calls[0] ?? [];
    expect(url).toBe("/api/v1/rooms");
    expect(init?.method).toBe("POST");
    expect(init?.body).toBe(JSON.stringify({ name: "ops" }));
    expect(new Headers(init?.headers).get("Content-Type")).toBe("application/json");
  });

  it("parses the error envelope into ApiError status, code, message and fields", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse(422, {
        error: { code: "invalid", message: "validation failed", fields: { username: "taken" } },
      }),
    );
    const err = await api("POST", "/setup", {}).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({
      status: 422,
      code: "invalid",
      message: "validation failed",
      fields: { username: "taken" },
    });
  });

  it("carries the details of a refusal, such as the counts of a non-empty organization", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse(409, {
        error: {
          code: "organization_not_empty",
          message: "organization still owns resources",
          details: { agents: "2", connectors: "0", groups: "1" },
        },
      }),
    );
    const err = await api("DELETE", "/orgs/o1").catch((e: unknown) => e);
    expect(err).toMatchObject({
      status: 409,
      code: "organization_not_empty",
      details: { agents: "2", connectors: "0", groups: "1" },
    });
  });

  it("has no details when the refusal names none", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse(404, { error: { code: "not_found", message: "x" } }),
    );
    const err = await api("GET", "/orgs/o9").catch((e: unknown) => e);
    expect(err).toMatchObject({ status: 404, details: {} });
  });

  it("falls back to a status-derived code when the body is not an envelope", async () => {
    fetchMock.mockResolvedValueOnce(new Response("boom", { status: 503 }));
    const err = await api("GET", "/rooms").catch((e: unknown) => e);
    expect(err).toMatchObject({ status: 503, code: "unavailable", fields: {} });
  });

  it("calls the unauthorized handler with the current path on 401", async () => {
    const handler = vi.fn();
    setUnauthorizedHandler(handler);
    fetchMock.mockResolvedValueOnce(
      jsonResponse(401, { error: { code: "unauthorized", message: "login required" } }),
    );
    const err = await api("GET", "/rooms").catch((e: unknown) => e);
    expect(err).toMatchObject({ status: 401, code: "unauthorized" });
    expect(handler).toHaveBeenCalledWith("/rooms/r1?tab=members");
  });

  it("does not redirect on 401 when the caller opts out (bad login)", async () => {
    const handler = vi.fn();
    setUnauthorizedHandler(handler);
    fetchMock.mockResolvedValueOnce(
      jsonResponse(401, { error: { code: "unauthorized", message: "bad credentials" } }),
    );
    await api("POST", "/auth/login", {}, { skipAuthRedirect: true }).catch(() => undefined);
    expect(handler).not.toHaveBeenCalled();
  });

  it("returns undefined for 204 and encodes query parameters", async () => {
    fetchMock.mockResolvedValueOnce(new Response(null, { status: 204 }));
    const out = await api("DELETE", "/tokens/t1", undefined, {
      query: { before_seq: 40, limit: 50, after_seq: undefined },
    });
    expect(out).toBeUndefined();
    expect(fetchMock.mock.calls[0]?.[0]).toBe("/api/v1/tokens/t1?before_seq=40&limit=50");
  });

  it("sends FormData untouched for multipart uploads", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(201, { id: "m1" }));
    const form = new FormData();
    form.append("message", JSON.stringify({ text: "hi" }));
    await api("POST", "/rooms/r1/messages", form);
    const init = fetchMock.mock.calls[0]?.[1];
    expect(init?.body).toBe(form);
    expect(new Headers(init?.headers).get("Content-Type")).toBeNull();
  });

  it("sends a Blob body as it is, with its own content type (avatar upload, PROF-07)", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { id: "i1" }));
    const image = new Blob(["\u0089PNG"], { type: "image/png" });
    await api("PUT", "/intelligences/i1/avatar", image);
    const init = fetchMock.mock.calls[0]?.[1];
    expect(init?.body).toBe(image);
    expect(new Headers(init?.headers).get("Content-Type")).toBe("image/png");
  });

  it("sends a Blob without a type as an opaque binary", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { id: "i1" }));
    await api("PUT", "/intelligences/i1/avatar", new Blob(["x"]));
    expect(new Headers(fetchMock.mock.calls[0]?.[1]?.headers).get("Content-Type")).toBe(
      "application/octet-stream",
    );
  });

  it("keeps the refused members of a members_refused answer, in the order sent (LAY-04)", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse(422, {
        error: {
          code: "members_refused",
          message: "some members cannot be added",
          refused: [
            { id: "i7", reason: "not_found" },
            { id: "i3", reason: "person" },
          ],
        },
      }),
    );
    const err = await api("POST", "/groups", { name: "ops", members: [] }).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({
      status: 422,
      code: "members_refused",
      refused: [
        { id: "i7", reason: "not_found" },
        { id: "i3", reason: "person" },
      ],
    });
  });

  it("has no refused members on other errors", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse(404, { error: { code: "not_found", message: "x" } }),
    );
    const err = await api("GET", "/rooms/x").catch((e: unknown) => e);
    expect(err).toMatchObject({ refused: [] });
  });

  it("maps a network failure to an unavailable ApiError", async () => {
    fetchMock.mockRejectedValueOnce(new TypeError("Failed to fetch"));
    const err = await api("GET", "/me").catch((e: unknown) => e);
    expect(err).toMatchObject({ status: 0, code: "unavailable" });
  });
});
