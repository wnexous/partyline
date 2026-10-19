import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { renderRoute } from "@/test/render";

describe("root route", () => {
  it("renders the app at /", async () => {
    renderRoute("/");
    expect(await screen.findByRole("heading", { name: "partyline" })).toBeInTheDocument();
  });
});
