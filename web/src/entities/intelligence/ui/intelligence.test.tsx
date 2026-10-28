import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { IntelligenceRef } from "@/lib/api/types.gen";
import { useLiveStore } from "@/lib/live/store";
import { renderWithProviders } from "@/test/render";
import { avatarTone, IntelligenceAvatar, initials } from "./intelligence-avatar";
import { IntelligenceName } from "./intelligence-name";
import { YouBadge } from "./you-badge";

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

  it("tints faces from soft tokens: never the red of errors or the green of presence", () => {
    const ids = Array.from({ length: 200 }, (_, i) => `id-${i}`);
    const tones = new Set(ids.map((id) => avatarTone(id)));
    expect(tones.size).toBe(8);
    for (const tone of tones) {
      expect(tone).toMatch(/^bg-tint-[a-z]+ text-tint-[a-z]+-foreground$/);
      expect(tone).not.toMatch(/destructive|online|red|green|success|warning|info/);
    }
  });

  it("draws every tint from one palette, by id alone (AD-024)", () => {
    const ids = Array.from({ length: 200 }, (_, i) => `id-${i}`);
    const names = [
      ...new Set(ids.map((id) => /bg-tint-([a-z]+)/.exec(avatarTone(id))?.[1])),
    ].sort();
    // The warm tints that used to be people's and the cool ones that used to be AIs' are one set.
    expect(names).toEqual(["apricot", "indigo", "ochre", "rose", "sage", "sky", "teal", "violet"]);
    // The tint is a function of the id: the same id keeps it, and the function takes nothing else.
    expect(avatarTone("id-7")).toBe(avatarTone("id-7"));
    expect(avatarTone).toHaveLength(1);
  });

  it("gives the same id the same tint whatever the kind, so an AI and a person look alike", () => {
    const ids = Array.from({ length: 60 }, (_, i) => `id-${i}`);
    const tintOf = (intelligence: IntelligenceRef) => {
      const { container } = renderWithProviders(<IntelligenceAvatar intelligence={intelligence} />);
      return container.querySelector('[data-slot="avatar-fallback"]')?.className;
    };
    for (const id of ids) {
      const asAI = tintOf({ ...atlas, id });
      expect(tintOf({ ...ana, id })).toBe(asAI);
      expect(tintOf({ ...person, id })).toBe(asAI);
    }
  });

  it("paints an AI, a user and a person from the whole palette, whatever the name looks like", () => {
    const tints = new Set<string>();
    for (const [i, kindOf] of [atlas, ana, person].entries()) {
      for (let n = 0; n < 40; n += 1) {
        const { container, unmount } = renderWithProviders(
          <IntelligenceAvatar intelligence={{ ...kindOf, id: `face-${i}-${n}` }} />,
        );
        const tint = /bg-tint-([a-z]+)\b/.exec(
          container.querySelector('[data-slot="avatar-fallback"]')?.className ?? "",
        )?.[1];
        tints.add(`${kindOf.kind}:${kindOf.role}:${tint}`);
        unmount();
      }
    }
    // Each kind reaches warm and cool tints alike: no family is reserved for any of them.
    for (const kindOf of ["ai:ai", "human:user", "human:person"]) {
      const reached = [...tints]
        .filter((t) => t.startsWith(`${kindOf}:`))
        .map((t) => t.split(":")[2]);
      expect(reached.some((t) => ["apricot", "ochre", "sage", "rose"].includes(t ?? ""))).toBe(
        true,
      );
      expect(reached.some((t) => ["teal", "sky", "indigo", "violet"].includes(t ?? ""))).toBe(true);
    }
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

describe("YouBadge (PROF-09)", () => {
  it("shows You for the viewer", () => {
    renderWithProviders(<YouBadge intelligence={person} viewerId={person.id} />);
    expect(screen.getByTestId("you-badge")).toHaveTextContent(/^You$/);
  });

  it("shows nothing for anyone else, an AI or a person included", () => {
    renderWithProviders(
      <>
        <YouBadge intelligence={atlas} viewerId={ana.id} />
        <YouBadge intelligence={person} viewerId={ana.id} />
        <YouBadge intelligence={ana} />
      </>,
    );
    expect(screen.queryByTestId("you-badge")).toBeNull();
    expect(screen.queryByText(/^(AI|IA|WhatsApp)$/)).toBeNull();
  });

  it("translates the badge in Portuguese", () => {
    useLiveStore.setState({ locale: "pt-BR" });
    renderWithProviders(<YouBadge intelligence={ana} viewerId={ana.id} />);
    expect(screen.getByTestId("you-badge")).toHaveTextContent(/^Você$/);
  });
});

describe("IntelligenceName (PROF-09)", () => {
  it("marks an AI named like a human by its @handle on keyboard focus, not by a kind badge (AD-024)", async () => {
    const user = userEvent.setup();
    renderWithProviders(<IntelligenceName intelligence={atlas} viewerId={ana.id} />);
    expect(screen.getByText("Ana Souza")).toBeInTheDocument();
    expect(screen.queryByTestId("kind-badge")).toBeNull();
    expect(screen.queryByText(/^(AI|IA)$/)).toBeNull();
    expect(screen.queryByText("@atlas")).toBeNull();

    await user.tab();
    expect(screen.getByRole("button", { name: "Ana Souza" })).toHaveFocus();
    expect(await screen.findByRole("tooltip")).toHaveTextContent(/^@atlas$/);
  });

  it("looks the same for an AI, a user and a person: the name and nothing after it", () => {
    const { container } = renderWithProviders(
      <>
        <IntelligenceName intelligence={atlas} viewerId={ana.id} />
        <IntelligenceName
          intelligence={makeIntelligence({ id: "u9", handle: "" })}
          viewerId={ana.id}
        />
        <IntelligenceName intelligence={{ ...person, handle: "" }} viewerId={ana.id} />
      </>,
    );
    const names = screen.getAllByTestId("intelligence-name");
    expect(names).toHaveLength(3);
    for (const name of names) {
      expect(name).toHaveTextContent(/^Ana Souza$/);
      expect(name.querySelector('[data-slot="badge"]')).toBeNull();
    }
    expect(container.querySelector('[data-slot="badge"]')).toBeNull();
  });

  it("says You after the viewer's own name, and only there", () => {
    renderWithProviders(
      <>
        <IntelligenceName intelligence={ana} viewerId={ana.id} />
        <IntelligenceName intelligence={atlas} viewerId={ana.id} />
      </>,
    );
    const [mine, theirs] = screen.getAllByTestId("intelligence-name");
    expect(mine).toHaveTextContent(/^Ana SouzaYou$/);
    expect(theirs).toHaveTextContent(/^Ana Souza$/);
  });

  it("shows the handle inline in detail views", () => {
    renderWithProviders(<IntelligenceName intelligence={person} showHandle />);
    expect(screen.getByText("Ana Souza")).toBeInTheDocument();
    expect(screen.getByText("@contact-ana")).toBeInTheDocument();
    expect(screen.queryByRole("tooltip")).toBeNull();
  });
});
