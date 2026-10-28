import type { Me } from "@/shared/api/types.gen";
import { useT } from "@/shared/i18n";
import { type ConversationSlots, MessengerPanel } from "./messenger-panel";

/** The wide-screen column: the messenger panel beside the page, as tall as the window. */
export function AppSidebar({ me, ...slots }: ConversationSlots & { me: Me }) {
  const t = useT();
  return (
    <aside
      aria-label={t("conversations.label")}
      className="sticky top-0 h-svh w-80 shrink-0 border-r border-sidebar-border"
    >
      <MessengerPanel me={me} {...slots} />
    </aside>
  );
}
