import { createFileRoute } from "@tanstack/react-router";
import { MessagesSquareIcon } from "lucide-react";
import { MessengerPanel } from "@/components/app-sidebar";
import { useMe } from "@/entities/account";
import { useT } from "@/shared/i18n";
import { useIsMobile } from "@/shared/lib/device";
import { EmptyState } from "@/shared/ui/molecules/empty-state";

export const Route = createFileRoute("/_app/")({
  component: HomePage,
});

/*
 * The home page is the messenger with nothing open: beside the sidebar it asks to pick a
 * conversation; on a phone, where the sidebar has no room beside a page, it is the list itself
 * (LAY-05).
 */
function HomePage() {
  const t = useT();
  const isMobile = useIsMobile();
  const me = useMe().data;
  if (isMobile) return me ? <MessengerPanel me={me} screen /> : null;
  return (
    <div className="flex flex-1 items-center justify-center">
      <EmptyState icon={MessagesSquareIcon} title={t("home.title")} headingLevel="h1">
        <p>{t("home.body")}</p>
      </EmptyState>
    </div>
  );
}
