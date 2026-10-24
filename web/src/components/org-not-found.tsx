import { Link } from "@tanstack/react-router";
import { CompassIcon } from "lucide-react";
import { EmptyState } from "@/components/empty-state";
import { Button } from "@/components/ui/button";
import { useT } from "@/lib/i18n";

/**
 * What a non-member gets for any page of an organization (CHART-02): it does not exist for them. The
 * same words whether it was deleted, never was or they were taken out of it.
 */
export function OrgNotFound() {
  const t = useT();
  return (
    <EmptyState
      icon={CompassIcon}
      title={t("orgs.notFoundTitle")}
      action={
        <Button asChild variant="outline">
          <Link to="/orgs">{t("orgs.notFoundBack")}</Link>
        </Button>
      }
    >
      <p>{t("orgs.notFoundBody")}</p>
    </EmptyState>
  );
}
