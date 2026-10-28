import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { PageSection } from "./page-section";

describe("PageSection", () => {
  it("is labelled by its heading, which it names with the given id", () => {
    render(
      <PageSection id="sec-a" title="Notifications" description="When to ping you">
        <p>body</p>
      </PageSection>,
    );
    const section = screen.getByRole("region", { name: "Notifications" });
    expect(section).toHaveAttribute("aria-labelledby", "sec-a");
    expect(screen.getByText("When to ping you")).toBeInTheDocument();
  });

  it("makes its own id when none is given", () => {
    render(
      <>
        <PageSection title="One">
          <p>a</p>
        </PageSection>
        <PageSection title="Two">
          <p>b</p>
        </PageSection>
      </>,
    );
    const one = screen.getByRole("region", { name: "One" });
    const two = screen.getByRole("region", { name: "Two" });
    expect(one.getAttribute("aria-labelledby")).toBeTruthy();
    expect(one.getAttribute("aria-labelledby")).not.toBe(two.getAttribute("aria-labelledby"));
  });

  it("puts the actions at the end of the heading's row in the stacked layout", () => {
    render(
      <PageSection
        title="Chats"
        layout="stacked"
        actions={<button type="button">Refresh</button>}
        headingClassName="label-caps"
      >
        <p>rows</p>
      </PageSection>,
    );
    expect(screen.getByRole("heading", { name: "Chats" })).toHaveClass("label-caps");
    expect(screen.getByRole("button", { name: "Refresh" })).toBeInTheDocument();
  });

  it("keeps the list layout's description under the heading", () => {
    render(
      <PageSection title="Hosted" description="Run here" layout="list">
        <p>rows</p>
      </PageSection>,
    );
    expect(screen.getByText("Run here")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Hosted" })).toBeInTheDocument();
  });
});
