import type { Translate } from "@/shared/i18n";

export const PLATFORMS: Record<string, string> = {
  telegram: "Telegram",
  whatsapp: "WhatsApp",
  discord: "Discord",
  webhook: "Webhook",
};

export const platformLabel = (kind?: string) => (kind ? (PLATFORMS[kind] ?? kind) : "");

/** A platform's display name; kinds without a known label are capitalized, like the server's. */
export function platformName(kind?: string): string {
  const label = platformLabel(kind);
  return label.charAt(0).toUpperCase() + label.slice(1);
}

/**
 * A chat's name (CTX-07): its title, else "<Platform> chat", matching the names the server gives
 * rooms routed from untitled chats (connectors.PlatformLabel).
 */
export function chatTitle(t: Translate, title: string | undefined, platform?: string): string {
  const trimmed = title?.trim();
  if (trimmed) return trimmed;
  const name = platformName(platform);
  return name ? t("chat.untitled", { platform: name }) : t("message.kind.chat");
}
