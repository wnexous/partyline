import { screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { mockApi } from "@/test/fetch";
import { renderRoute } from "@/test/render";

describe("root route", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("renders the app and its not-found page for unknown paths", async () => {
    mockApi({});
    renderRoute("/definitely/not/here");
    expect(
      await screen.findByRole("heading", { name: "Nothing on this line" }),
    ).toBeInTheDocument();
  });
});
