import { screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useLiveStore } from "@/lib/live/store";
import { json, mockApi } from "@/test/fetch";
import { makeAgent, makeChat } from "@/test/fixtures";
import { renderWithProviders } from "@/test/render";
import { RouteChatDialog } from "./route-chat-dialog";

vi.mock("@tanstack/react-router", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@tanstack/react-router")>()),
  useNavigate: () => vi.fn(),
}));

beforeEach(() => useLiveStore.setState({ locale: "en" }));
afterEach(() => vi.unstubAllGlobals());

describe("route a chat to an AI", () => {
  // PROF-09: an AI with a human-looking name still carries the AI badge in the picker.
  it("marks every AI in the list with the AI badge", async () => {
    mockApi({ "GET /agents": json(200, { items: [makeAgent({ id: "a9", name: "Ana Lima" })] }) });
    renderWithProviders(<RouteChatDialog chat={makeChat()} onClose={() => {}} />);
    const list = await screen.findByRole("list", { name: "Agents" });
    const item = within(list).getByRole("button", { name: /Ana Lima/ });
    expect(within(item).getByText("AI")).toBeInTheDocument();
  });
});
