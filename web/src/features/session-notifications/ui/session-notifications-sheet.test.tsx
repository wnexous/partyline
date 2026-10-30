import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";
import type { List, RoomNotification } from "@/shared/api/types.gen";
import { json, mockApi } from "@/test/fetch";
import { useLiveStore } from "@/test/live-store";
import { renderWithProviders } from "@/test/render";
import {
  MemberSessionNotifyButton,
  SessionNotificationsButton,
} from "./session-notifications-sheet";

/*
 * The owner's notifications of a session (SN-08): the member card opens one conversation and the profile
 * opens all of them, each with a switch that changes the setting through the API. The data is the
 * session's, per conversation: deploys is on mentions only with two unread, the DM is on.
 */

const rooms: List<RoomNotification> = {
  items: [
    { room_id: "r1", name: "deploys", kind: "group", notify: "mentions", unread: 2 },
    { room_id: "d1", name: "", kind: "dm", notify: "on", unread: 0 },
  ],
};

beforeEach(() => useLiveStore.setState({ locale: "en" }));

describe("Session notifications (SN-08)", () => {
  it("opens this conversation from the member card and changes it through the API", async () => {
    const { calls } = mockApi({
      "GET /agents/a1/sessions/claude-2/notifications": json(200, rooms),
      "PUT /agents/a1/sessions/claude-2/notifications/r1": json(200, {
        ...rooms.items[0],
        notify: "on",
      }),
    });
    renderWithProviders(
      <MemberSessionNotifyButton agentId="a1" label="claude-2" sessionName="Atlas" roomId="r1" />,
    );

    await userEvent
      .setup()
      .click(screen.getByRole("button", { name: "Notification settings of Atlas" }));
    expect(await screen.findByText("Notifications in this conversation")).toBeInTheDocument();
    expect(screen.getByText("deploys")).toBeInTheDocument();
    expect(screen.queryByText("Direct message")).toBeNull();
    expect(screen.getByText(/Mentions only/)).toBeInTheDocument();
    expect(screen.getByText(/2 unread/)).toBeInTheDocument();

    const toggle = screen.getByRole("switch", { name: "Notifications in deploys" });
    expect(toggle).not.toBeChecked();
    await userEvent.setup().click(toggle);
    await waitFor(() =>
      expect(
        calls.some(
          (call) =>
            call.method === "PUT" && call.path === "/agents/a1/sessions/claude-2/notifications/r1",
        ),
      ).toBe(true),
    );
    expect(calls.find((call) => call.method === "PUT")?.body).toEqual({ notify: "on" });
    expect(await screen.findByText(/All messages/)).toBeInTheDocument();
  });

  it("lists every conversation of the session from the profile, the principal's included", async () => {
    const { calls } = mockApi({
      "GET /agents/a1/sessions/_principal/notifications": json(200, rooms),
      "PUT /agents/a1/sessions/_principal/notifications/r1": json(200, {
        ...rooms.items[0],
        notify: "on",
      }),
    });
    renderWithProviders(
      <SessionNotificationsButton agentId="a1" label="" sessionName="Atlas principal" />,
    );

    await userEvent.setup().click(screen.getByRole("button", { name: "Notifications" }));
    expect(await screen.findByText("Notifications of Atlas principal")).toBeInTheDocument();
    expect(screen.getByText("deploys")).toBeInTheDocument();
    expect(screen.getByText("Direct message")).toBeInTheDocument();
    expect(screen.getByRole("switch", { name: "Notifications in Direct message" })).toBeChecked();

    await userEvent.setup().click(screen.getByRole("switch", { name: "Notifications in deploys" }));
    await waitFor(() =>
      expect(
        calls.some(
          (call) =>
            call.method === "PUT" &&
            call.path === "/agents/a1/sessions/_principal/notifications/r1",
        ),
      ).toBe(true),
    );
  });

  it("says why a change failed instead of hiding the switch", async () => {
    mockApi({
      "GET /agents/a1/sessions/claude-2/notifications": json(200, rooms),
      "PUT /agents/a1/sessions/claude-2/notifications/r1": json(403, {
        error: { code: "forbidden", message: "forbidden" },
      }),
    });
    renderWithProviders(
      <MemberSessionNotifyButton agentId="a1" label="claude-2" sessionName="Atlas" roomId="r1" />,
    );
    await userEvent
      .setup()
      .click(screen.getByRole("button", { name: "Notification settings of Atlas" }));
    const toggle = await screen.findByRole("switch", { name: "Notifications in deploys" });
    await userEvent.setup().click(toggle);
    expect(
      await screen.findByText("Only whoever manages this AI can change its notifications."),
    ).toBeInTheDocument();
    expect(toggle).not.toBeChecked();
  });
});
