import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { RelativeTime } from "./relative-time";

const NOW = Date.now();
const minutesAgo = (n: number) => new Date(NOW - n * 60_000).toISOString();

describe("RelativeTime", () => {
  it("renders a time element with the relative text and the absolute time as its title", () => {
    const iso = minutesAgo(5);
    render(<RelativeTime value={iso} locale="en" />);
    const el = screen.getByText("5 min. ago");
    expect(el.tagName).toBe("TIME");
    expect(el).toHaveAttribute("datetime", iso);
    expect(el.getAttribute("title")).toMatch(/\d/);
  });

  it("passes the formatted time to a sentence built by children", () => {
    render(
      <RelativeTime value={minutesAgo(5)} locale="en">
        {(time) => `Seen ${time}`}
      </RelativeTime>,
    );
    expect(screen.getByText("Seen 5 min. ago")).toBeInTheDocument();
  });

  it("has no title for the date format, which is already absolute", () => {
    render(<RelativeTime value={minutesAgo(5)} locale="en" format="date" />);
    const el = document.querySelector("time");
    expect(el).not.toHaveAttribute("title");
    expect(el?.textContent).toMatch(/\d{4}/);
  });

  it("shows the clock for the clock format", () => {
    render(<RelativeTime value={minutesAgo(0)} locale="en" format="clock" />);
    expect(document.querySelector("time")?.textContent).toMatch(/\d{2}:\d{2}/);
  });
});
