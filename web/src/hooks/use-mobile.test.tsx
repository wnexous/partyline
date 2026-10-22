import { renderToString } from "react-dom/server";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubViewport } from "@/test/media";
import { useIsMobile } from "./use-mobile";

function Probe() {
  return <span>{useIsMobile() ? "mobile" : "wide"}</span>;
}

afterEach(() => vi.unstubAllGlobals());

describe("useIsMobile", () => {
  // The messenger shows the list or the page, not both: a first render that guessed "wide" on a
  // phone would flash the wrong screen before the effect corrected it.
  it("knows the screen on the very first render, below 768 px", () => {
    stubViewport(360);
    expect(renderToString(<Probe />)).toContain("mobile");
  });

  it("is wide from 768 px up on the first render", () => {
    stubViewport(768);
    expect(renderToString(<Probe />)).toContain("wide");
    stubViewport(767);
    expect(renderToString(<Probe />)).toContain("mobile");
  });
});
