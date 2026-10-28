import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MemberSettings } from "@/components/member-settings";
import type { RoomMember } from "@/shared/api/types.gen";
import { useLiveStore } from "@/lib/live/store";
import { apiError, json, mockApi } from "@/test/fetch";
import { makeMember } from "@/test/fixtures";
import { renderWithProviders } from "@/test/render";

/*
 * Agent membership settings (MEM-01..05, routing-context T13): each field maps to its PATCH key,
 * unchanged fields are not sent, limits are checked before sending and 422 reasons show under
 * the field they belong to.
 */

const PATH = "PATCH /rooms/r1/members/mb-bot";

function setup(member: Partial<RoomMember> = {}, response?: Response) {
  const base = makeMember({ id: "mb-bot", member_id: "a1", name: "release-bot", ...member });
  const api = mockApi({
    [PATH]: response ?? ((_url, init) => json(200, { ...base, ...JSON.parse(String(init?.body)) })),
  });
  const user = userEvent.setup();
  renderWithProviders(<MemberSettings roomId="r1" member={base} />);
  const patches = () => api.calls.filter((c) => c.method === "PATCH");
  return { user, patches };
}

async function open(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole("button", { name: "Settings for release-bot" }));
  return screen.findByRole("dialog", { name: "Agent settings" });
}

beforeEach(() => useLiveStore.setState({ locale: "en" }));
afterEach(() => vi.unstubAllGlobals());

describe("agent membership settings (MEM-01..05)", () => {
  it("starts from the member's current settings", async () => {
    const { user } = setup({
      display_name: "Bia",
      delivery: "mentions",
      keywords: ["deploy"],
    });
    const dialog = await open(user);
    expect(within(dialog).getByRole("textbox", { name: /Display name/ })).toHaveValue("Bia");
    expect(within(dialog).getByRole("radio", { name: /Only mentions/ })).toBeChecked();
    expect(within(dialog).getByRole("listitem")).toHaveTextContent("deploy");
  });

  it("sends only the trimmed display name when that is all that changed (MEM-02)", async () => {
    const { user, patches } = setup();
    const dialog = await open(user);
    await user.type(within(dialog).getByRole("textbox", { name: /Display name/ }), "  Bia  ");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(patches()).toHaveLength(1));
    expect(patches()[0]?.body).toEqual({ display_name: "Bia" });
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("clears the display name with an empty string", async () => {
    const { user, patches } = setup({ display_name: "Bia" });
    const dialog = await open(user);
    await user.clear(within(dialog).getByRole("textbox", { name: /Display name/ }));
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(patches()).toHaveLength(1));
    expect(patches()[0]?.body).toEqual({ display_name: "" });
  });

  it("offers no signature control and never sends sign_outbound (CID-22)", async () => {
    const { user, patches } = setup();
    const dialog = await open(user);
    expect(within(dialog).queryByRole("switch")).toBeNull();
    expect(within(dialog).queryByText(/Sign messages/)).toBeNull();
    await user.type(within(dialog).getByRole("textbox", { name: /Display name/ }), "Bia");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(patches()).toHaveLength(1));
    expect(patches()[0]?.body).toEqual({ display_name: "Bia" });
  });

  it("says that only trusted delivers what identities that manage the agent wrote (MEM-04, POS-10)", async () => {
    const { user } = setup();
    const dialog = await open(user);
    const hint = "Only identities that manage it, or chat accounts they verified.";
    expect(within(dialog).getByRole("radio", { name: /Only trusted/ })).toHaveAccessibleDescription(
      hint,
    );
    // The agent's owner is not who it trusts any more (AD-018).
    expect(dialog).not.toHaveTextContent("owner");
  });

  it("sends delivery trusted (MEM-04)", async () => {
    const { user, patches } = setup();
    const dialog = await open(user);
    await user.click(within(dialog).getByRole("radio", { name: /Only trusted/ }));
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(patches()).toHaveLength(1));
    expect(patches()[0]?.body).toEqual({ delivery: "trusted" });
  });

  it("sends delivery mentions with the keywords added (MEM-03)", async () => {
    const { user, patches } = setup();
    const dialog = await open(user);
    await user.click(within(dialog).getByRole("radio", { name: /Only mentions/ }));
    const keyword = within(dialog).getByRole("textbox", { name: /Keywords/ });
    await user.type(keyword, " deploy {Enter}");
    await user.type(keyword, "release");
    await user.click(within(dialog).getByRole("button", { name: "Add keyword" }));
    // A repeat in another case is the same keyword.
    await user.type(keyword, "Deploy{Enter}");
    expect(
      within(dialog)
        .getAllByRole("listitem")
        .map((li) => li.textContent),
    ).toEqual(["deploy", "release"]);
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(patches()).toHaveLength(1));
    expect(patches()[0]?.body).toEqual({ delivery: "mentions", keywords: ["deploy", "release"] });
  });

  it("removes a keyword and sends the remaining list", async () => {
    const { user, patches } = setup({ delivery: "mentions", keywords: ["deploy", "release"] });
    const dialog = await open(user);
    await user.click(within(dialog).getByRole("button", { name: "Remove keyword deploy" }));
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(patches()).toHaveLength(1));
    expect(patches()[0]?.body).toEqual({ keywords: ["release"] });
  });

  it("sends nothing when nothing changed", async () => {
    const { user, patches } = setup();
    const dialog = await open(user);
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(patches()).toHaveLength(0);
  });

  it("counts the display name limit in characters, not UTF-16 units (MEM-05)", async () => {
    const { user, patches } = setup();
    const dialog = await open(user);
    const name = within(dialog).getByRole("textbox", { name: /Display name/ });
    // 64 emoji are 128 UTF-16 units but 64 characters: allowed.
    await user.type(name, "🙂".repeat(64));
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(patches()).toHaveLength(1));
    expect(patches()[0]?.body).toEqual({ display_name: "🙂".repeat(64) });
  });

  it("refuses 65 characters with the limit under the field (MEM-05)", async () => {
    const { user, patches } = setup();
    const dialog = await open(user);
    await user.type(within(dialog).getByRole("textbox", { name: /Display name/ }), "a".repeat(65));
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(await within(dialog).findByText("At most 64 characters")).toBeInTheDocument();
    expect(patches()).toHaveLength(0);
  });

  it("refuses a keyword over 64 characters and a 21st keyword (MEM-05)", async () => {
    const twenty = Array.from({ length: 20 }, (_, i) => `k${i}`);
    const { user, patches } = setup({ delivery: "mentions", keywords: twenty.slice(0, 19) });
    const dialog = await open(user);
    const keyword = within(dialog).getByRole("textbox", { name: /Keywords/ });

    await user.type(keyword, `${"a".repeat(65)}{Enter}`);
    expect(await within(dialog).findByText("Each keyword: 1 to 64 characters")).toBeInTheDocument();
    expect(within(dialog).getAllByRole("listitem")).toHaveLength(19);

    await user.clear(keyword);
    await user.type(keyword, "k19{Enter}");
    expect(within(dialog).getAllByRole("listitem")).toHaveLength(20);
    expect(keyword).toBeDisabled();
    expect(within(dialog).getByText("At most 20 keywords")).toBeInTheDocument();
    expect(patches()).toHaveLength(0);
  });

  it("shows 422 reasons under their fields and keeps the popover open", async () => {
    const { user } = setup(
      {},
      apiError(422, "validation", "invalid", {
        display_name: "must have at most 64 characters",
        keywords: "at most 20 keywords",
      }),
    );
    const dialog = await open(user);
    await user.type(within(dialog).getByRole("textbox", { name: /Display name/ }), "Bia");
    await user.click(within(dialog).getByRole("radio", { name: /Only mentions/ }));
    await user.type(within(dialog).getByRole("textbox", { name: /Keywords/ }), "deploy{Enter}");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(await within(dialog).findByText("must have at most 64 characters")).toBeInTheDocument();
    expect(within(dialog).getByText("at most 20 keywords")).toBeInTheDocument();
    expect(within(dialog).getByRole("textbox", { name: /Display name/ })).toHaveAttribute(
      "aria-invalid",
      "true",
    );
  });

  it("shows a 422 on a field without an input as a form error", async () => {
    const { user } = setup(
      {},
      apiError(422, "validation", "invalid", { delivery: "only for agent members" }),
    );
    const dialog = await open(user);
    await user.click(within(dialog).getByRole("radio", { name: /Only trusted/ }));
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("only for agent members");
  });

  it("explains a 403", async () => {
    const { user } = setup({}, apiError(403, "forbidden"));
    const dialog = await open(user);
    await user.click(within(dialog).getByRole("radio", { name: /Only trusted/ }));
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "Only room managers can change these settings.",
    );
  });

  it("adds a keyword typed but not committed when saving (A11Y-02)", async () => {
    const { user, patches } = setup({ delivery: "mentions", keywords: ["deploy"] });
    const dialog = await open(user);
    await user.type(within(dialog).getByRole("textbox", { name: /Keywords/ }), " pedido ");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(patches()).toHaveLength(1));
    expect(patches()[0]?.body).toEqual({ keywords: ["deploy", "pedido"] });
  });

  it("keeps the popover open when the typed keyword is invalid on save (A11Y-02)", async () => {
    const { user, patches } = setup({ delivery: "mentions" });
    const dialog = await open(user);
    const input = within(dialog).getByRole("textbox", { name: /Keywords/ });
    await user.click(input);
    await user.paste("k".repeat(65));
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(await within(dialog).findByText("Each keyword: 1 to 64 characters")).toBeInTheDocument();
    expect(patches()).toHaveLength(0);
  });

  it("announces the keyword message and ties it to the input (A11Y-01)", async () => {
    const { user } = setup({ delivery: "mentions" });
    const dialog = await open(user);
    const input = within(dialog).getByRole("textbox", { name: /Keywords/ });
    await user.click(input);
    await user.paste("k".repeat(65));
    await user.keyboard("{Enter}");
    const message = await within(dialog).findByText("Each keyword: 1 to 64 characters");
    expect(message.closest("[aria-live]")).toHaveAttribute("aria-live", "polite");
    expect(input).toHaveAccessibleDescription(/Each keyword: 1 to 64 characters/);
  });

  it("speaks Portuguese", async () => {
    useLiveStore.setState({ locale: "pt-BR" });
    mockApi({});
    const user = userEvent.setup();
    renderWithProviders(
      <MemberSettings roomId="r1" member={makeMember({ name: "release-bot" })} />,
    );
    await user.click(screen.getByRole("button", { name: "Configurações de release-bot" }));
    const dialog = await screen.findByRole("dialog", { name: "Configurações do agente" });
    expect(within(dialog).queryByRole("switch")).toBeNull();
    expect(within(dialog).getByRole("radiogroup", { name: "Recebe" })).toHaveAccessibleDescription(
      "Só menções reduz o ruído; não é um filtro de segurança. Use Só confiáveis para limitar quem chega ao agente.",
    );
  });

  it("says that only mentions is not a security filter (MEM-06)", async () => {
    const { user } = setup();
    const dialog = await open(user);
    expect(
      within(dialog).getByRole("radiogroup", { name: "Receives" }),
    ).toHaveAccessibleDescription(
      "Only mentions reduces noise; it is not a security filter. Use Only trusted to limit who can reach the agent.",
    );
  });
});
