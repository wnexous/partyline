import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { PanelSkeleton } from "./panel-skeleton";

describe("PanelSkeleton", () => {
  it("is hidden from assistive tech and lays its children out with the given spacing", () => {
    const { container } = render(
      <PanelSkeleton className="p-4">
        <span>bar</span>
      </PanelSkeleton>,
    );
    const root = container.firstElementChild;
    expect(root).toHaveAttribute("aria-hidden", "true");
    expect(root).toHaveClass("space-y-3", "p-4");
    expect(screen.getByText("bar")).toBeInTheDocument();
  });
});
