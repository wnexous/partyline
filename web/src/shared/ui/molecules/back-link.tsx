import { Link } from "@tanstack/react-router";
import { ArrowLeftIcon } from "lucide-react";
import { useT } from "@/shared/i18n";
import { cn } from "@/shared/lib/utils";
import { Button } from "@/shared/ui/primitives/button";

/** The way back to the conversation list on a phone (LAY-05). */
export function BackToConversations({ className }: { className?: string }) {
  const t = useT();
  return (
    <Button asChild variant="ghost" size="icon" className={cn("size-11 shrink-0", className)}>
      <Link to="/" aria-label={t("nav.back")}>
        <ArrowLeftIcon aria-hidden="true" />
      </Link>
    </Button>
  );
}
