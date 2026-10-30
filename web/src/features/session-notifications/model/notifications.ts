/*
 * A session's notification setting in one conversation (SN-02): every message the room's delivery mode
 * wants, or only the messages that address the session. The server stores the same two values.
 */
export type NotificationSetting = "on" | "mentions";

/** The setting a server value names; anything but "mentions" is the default of the principal, "on". */
export function notificationSettingOf(value: string | undefined): NotificationSetting {
  return value === "mentions" ? "mentions" : "on";
}
