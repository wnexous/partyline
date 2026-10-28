import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ListSkeleton } from "./list-skeleton";

describe("ListSkeleton", () => {
  it("is hidden from assistive tech and shows three rows by default", () => {
    const { container } = render(<ListSkeleton />);
    const list = container.querySelector("ul");
    expect(list).toHaveAttribute("aria-hidden", "true");
    expect(container.querySelectorAll("li")).toHaveLength(3);
  });

  it("shows the given number of rows", () => {
    const { container } = render(<ListSkeleton rows={5} />);
    expect(container.querySelectorAll("li")).toHaveLength(5);
  });

  it("puts a round face at the start of each row with avatar", () => {
    const { container } = render(<ListSkeleton rows={2} avatar />);
    expect(container.querySelectorAll("li")).toHaveLength(2);
    expect(container.querySelectorAll("li .rounded-full")).toHaveLength(2);
  });
});
