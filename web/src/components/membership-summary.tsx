import type { ProfileMembership } from "@/lib/api/types.gen";
import { type MessageKey, useT } from "@/shared/i18n";

const PERMISSION_LABELS: Record<string, MessageKey> = {
  admin: "profile.permissionAdmin",
  member: "profile.permissionMember",
  guest: "profile.permissionGuest",
};

/**
 * What an identity is in one organization of its profile: the organization, tagged when it is the
 * AI's home, and its permission and position there (OMEM-03). The profile page and the profile
 * panel both show it; the ways to delete or remove the identity belong to the page alone.
 */
export function MembershipSummary({ membership: m }: { membership: ProfileMembership }) {
  const t = useT();
  const permission = PERMISSION_LABELS[m.permission];
  return (
    <div className="min-w-0 flex-1 basis-48 space-y-0.5">
      <p className="flex flex-wrap items-baseline gap-x-2 gap-y-1 text-sm">
        <span className="font-medium [overflow-wrap:anywhere]">{m.org_name}</span>
        {m.delete_effect === "delete" ? (
          <span className="shrink-0 rounded-[3px] border px-1.5 py-px text-[0.7rem] text-muted-foreground">
            {t("profile.homeOrg")}
          </span>
        ) : null}
      </p>
      <p className="text-xs text-muted-foreground [overflow-wrap:anywhere]">
        {permission ? t(permission) : m.permission}
        {m.position_name ? ` · ${m.position_name}` : ""}
      </p>
    </div>
  );
}
