import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { apiError, mockApi } from "@/test/fetch";
import { useLiveStore } from "@/test/live-store";
import { renderWithProviders } from "@/test/render";
import { MakePrincipalButton } from "./make-principal-button";

const toast = vi.hoisted(() => Object.assign(vi.fn(), { error: vi.fn() }));
vi.mock("sonner", () => ({ toast }));

beforeEach(() => {
  useLiveStore.setState({ locale: "en" });
  toast.mockClear();
  toast.error.mockClear();
});

const noContent = () => new Response(null, { status: 204 });

describe("MakePrincipalButton (SESS-59)", () => {
  it("posts the label of the session and says the session is taking over", async () => {
    const { calls } = mockApi({ "POST /agents/a1/principal": noContent });
    renderWithProviders(<MakePrincipalButton agentId="a1" name="Atlas" label="claude-2" />);
    await userEvent.click(
      screen.getByRole("button", { name: "Make :claude-2 the principal session of Atlas" }),
    );
    await waitFor(() => expect(calls.filter((c) => c.method === "POST")).toHaveLength(1));
    expect(calls[0]?.body).toEqual({ label: "claude-2" });
    await waitFor(() =>
      expect(toast).toHaveBeenCalledWith(":claude-2 is taking over as Atlas’s principal session"),
    );
  });

  it("says so when the server refuses", async () => {
    mockApi({
      "POST /agents/a1/principal": () => apiError(409, "conflict", "that session is not live"),
    });
    renderWithProviders(<MakePrincipalButton agentId="a1" name="Atlas" label="claude-2" />);
    await userEvent.click(screen.getByRole("button", { name: /Make :claude-2/ }));
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith("Couldn’t change Atlas’s principal session."),
    );
  });
});
