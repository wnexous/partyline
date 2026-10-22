import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { IntelligenceAvatar, initials } from "@/components/intelligence/intelligence-avatar";
import { IntelligenceName } from "@/components/intelligence/intelligence-name";
import { KindBadge } from "@/components/intelligence/kind-badge";
import type { IntelligenceRef } from "@/lib/api/types.gen";
import { useLiveStore } from "@/lib/live/store";
import { renderWithProviders } from "@/test/render";

/** Synthetic intelligences; never real names or handles. */
function makeIntelligence(over: Partial<IntelligenceRef> = {}): IntelligenceRef {
  return {
    id: "u1",
    kind: "human",
    role: "user",
    display_name: "Ana Souza",
    handle: "ana",
    ...over,
  };
}

const ana = makeIntelligence();
const atlas = makeIntelligence({
  id: "a1",
  kind: "ai",
  role: "ai",
  display_name: "Ana Souza",
  handle: "atlas",
});
const person = makeIntelligence({
  id: "p1",
  kind: "human",
  role: "person",
  display_name: "Ana Souza",
  handle: "contact-ana",
  platform: "whatsapp",
});

/** happy-dom never loads images; report the avatar as loaded so Radix renders the <img>. */
class LoadedImage {
  complete = true;
  naturalWidth = 1;
  crossOrigin: string | null = null;
  referrerPolicy = "";
  src = "";
  addEventListener() {}
  removeEventListener() {}
}

beforeEach(() => useLiveStore.setState({ locale: "en" }));
afterEach(() => {
  useLiveStore.setState({ locale: "en" });
  vi.unstubAllGlobals();
});

describe("initials (PROF-08)", () => {
  it.each([
    ["Ana Souza", "AS"],
    ["Ana Maria Souza", "AS"],
    ["  ana   souza  ", "AS"],
    ["Atlas", "A"],
    ["atlas", "A"],
    ["x", "X"],
    ["", "?"],
    ["   ", "?"],
    ["😀 Smile", "😀S"],
    ["Åsa Öberg", "ÅÖ"],
  ])("shows %j as %j", (name, expected) => {
    expect(initials(name)).toBe(expected);
  });
});

describe("IntelligenceAvatar (PROF-08)", () => {
  it("shows initials on a colour stable per id and different from another id's", () => {
    const { container } = renderWithProviders(
      <>
        <IntelligenceAvatar intelligence={ana} />
        <IntelligenceAvatar intelligence={ana} />
        <IntelligenceAvatar intelligence={makeIntelligence({ id: "u2" })} />
      </>,
    );
    const fallbacks = [...container.querySelectorAll('[data-slot="avatar-fallback"]')];
    expect(fallbacks).toHaveLength(3);
    const [first, second, third] = fallbacks;
    if (!first || !second || !third) throw new Error("expected three avatar fallbacks");
    expect(first).toHaveTextContent(/^AS$/);
    expect(first.className).toBe(second.className);
    expect(first.className).not.toBe(third.className);
  });

  it("has a 96 px size for the profile page, with its initials larger", () => {
    const { container } = renderWithProviders(
      <>
        <IntelligenceAvatar intelligence={ana} size="xl" />
        <IntelligenceAvatar intelligence={ana} />
      </>,
    );
    const [xl, normal] = [...container.querySelectorAll('[data-slot="avatar"]')];
    expect(xl?.className).toContain("size-24");
    expect(normal?.className).not.toContain("size-24");
    expect(xl?.querySelector('[data-slot="avatar-fallback"]')?.className).toContain("text-2xl");
  });

  it("asks for an image of the size it shows", () => {
    vi.stubGlobal("Image", LoadedImage);
    const { container } = renderWithProviders(
      <IntelligenceAvatar
        intelligence={makeIntelligence({ avatar_url: "/api/v1/intelligences/u1/avatar?v=abc" })}
        size="xl"
      />,
    );
    expect(container.querySelector("img")).toHaveAttribute("width", "96");
    expect(container.querySelector("img")).toHaveAttribute("height", "96");
  });

  it("shows the avatar image with the display name as alt text", () => {
    vi.stubGlobal("Image", LoadedImage);
    renderWithProviders(
      <IntelligenceAvatar
        intelligence={makeIntelligence({ avatar_url: "/api/v1/intelligences/u1/avatar?v=abc" })}
      />,
    );
    expect(screen.getByAltText("Ana Souza")).toHaveAttribute(
      "src",
      "/api/v1/intelligences/u1/avatar?v=abc",
    );
    expect(screen.queryByText("AS")).toBeNull();
  });
});

describe("IntelligenceAvatar next to a name", () => {
  it("is hidden from assistive technology when decorative, and heard otherwise", () => {
    renderWithProviders(
      <>
        <IntelligenceAvatar intelligence={ana} decorative />
        <IntelligenceAvatar intelligence={makeIntelligence({ id: "u2" })} />
      </>,
    );
    const [decorative, heard] = screen.getAllByTestId("intelligence-avatar");
    expect(decorative).toHaveAttribute("aria-hidden", "true");
    expect(heard).not.toHaveAttribute("aria-hidden");
  });

  it("gives its image no alt text when decorative, so the name beside it is not read twice", () => {
    vi.stubGlobal("Image", LoadedImage);
    const { container } = renderWithProviders(
      <IntelligenceAvatar
        intelligence={makeIntelligence({ avatar_url: "/api/v1/intelligences/u1/avatar?v=abc" })}
        decorative
      />,
    );
    expect(container.querySelector("img")).toHaveAttribute("alt", "");
  });
});

describe("KindBadge (PROF-09)", () => {
  it("marks an AI as AI even when its display name looks like a human's", () => {
    renderWithProviders(<KindBadge intelligence={atlas} />);
    expect(screen.getByTestId("kind-badge")).toHaveTextContent(/^AI$/);
  });

  it("shows a person's platform", () => {
    renderWithProviders(<KindBadge intelligence={person} />);
    expect(screen.getByTestId("kind-badge")).toHaveTextContent(/^WhatsApp$/);
  });

  it("shows You for the viewer", () => {
    renderWithProviders(<KindBadge intelligence={person} viewerId={person.id} />);
    expect(screen.getByTestId("kind-badge")).toHaveTextContent(/^You$/);
  });

  it("translates the badges in Portuguese", () => {
    useLiveStore.setState({ locale: "pt-BR" });
    renderWithProviders(
      <>
        <KindBadge intelligence={atlas} />
        <KindBadge intelligence={person} viewerId={person.id} />
      </>,
    );
    const badges = screen.getAllByTestId("kind-badge");
    expect(badges[0]).toHaveTextContent(/^IA$/);
    expect(badges[1]).toHaveTextContent(/^Você$/);
  });
});

describe("IntelligenceName (PROF-09)", () => {
  it("keeps the AI badge and @handle for an AI named like a human, on keyboard focus", async () => {
    const user = userEvent.setup();
    renderWithProviders(<IntelligenceName intelligence={atlas} viewerId={ana.id} />);
    expect(screen.getByText("Ana Souza")).toBeInTheDocument();
    expect(screen.getByTestId("kind-badge")).toHaveTextContent(/^AI$/);
    expect(screen.queryByText("@atlas")).toBeNull();

    await user.tab();
    expect(screen.getByRole("button", { name: "Ana Souza" })).toHaveFocus();
    expect(await screen.findByRole("tooltip")).toHaveTextContent(/^@atlas$/);
  });

  it("shows the handle inline in detail views", () => {
    renderWithProviders(<IntelligenceName intelligence={person} showHandle />);
    expect(screen.getByText("Ana Souza")).toBeInTheDocument();
    expect(screen.getByText("@contact-ana")).toBeInTheDocument();
    expect(screen.queryByRole("tooltip")).toBeNull();
  });
});
