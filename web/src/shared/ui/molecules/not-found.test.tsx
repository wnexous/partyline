import { render, screen } from "@testing-library/react";
import { MessageSquareOffIcon } from "lucide-react";
import { describe, expect, it } from "vitest";
import { NotFound } from "./not-found";

describe("NotFound", () => {
  it("is a named section with the title as its heading, the body and the action", () => {
    render(
      <NotFound
        title="Room not found"
        body="It may have been deleted."
        action={<a href="/">Back</a>}
      />,
    );
    expect(screen.getByRole("heading", { name: "Room not found" })).toBeInTheDocument();
    expect(screen.getByText("It may have been deleted.")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Back" })).toHaveAttribute("href", "/");
  });

  it("hides its icon from assistive tech and takes another icon when given one", () => {
    const { container } = render(
      <NotFound title="Gone" body="Nothing here." icon={MessageSquareOffIcon} />,
    );
    const decor = container.querySelector('[aria-hidden="true"]');
    expect(decor).not.toBeNull();
    expect(decor?.querySelector("svg")).not.toBeNull();
  });
});
