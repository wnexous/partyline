import { createFileRoute, Link } from "@tanstack/react-router";
import { BuildingIcon, PlusIcon } from "lucide-react";
import { useId, useState } from "react";
import { toast } from "sonner";
import { NewOrgDialog } from "@/components/new-org-dialog";
import { OrgAvatar } from "@/components/org-avatar";
import { useCurrentOrg } from "@/hooks/use-current-org";
import {
  useAcceptInvitation,
  useDeclineInvitation,
  useMe,
  useMyInvitations,
  useOrgs,
} from "@/lib/api/queries";
import { type MyOrg, PermissionGuest, permissionLabel } from "@/lib/orgs";
import { isApiError } from "@/shared/api/client";
import { ErrorCodeAlreadyMember, type Invitation } from "@/shared/api/types.gen";
import { type MessageKey, type Translate, useI18n, useT } from "@/shared/i18n";
import { RelativeTime } from "@/shared/ui/atoms/relative-time";
import { EmptyState } from "@/shared/ui/molecules/empty-state";
import { PageHeader } from "@/shared/ui/molecules/page-header";
import { Badge } from "@/shared/ui/primitives/badge";
import { Button } from "@/shared/ui/primitives/button";
import { Skeleton } from "@/shared/ui/primitives/skeleton";

export const Route = createFileRoute("/_app/orgs/")({
  component: OrgsPage,
});

/*
 * The viewer's organizations (ORG-01, OMEM-03) and the invitations waiting for them or for an AI
 * they manage (OMEM-01, OMEM-02, OMEM-07), with what accepting grants said in words: a position
 * puts the invitee under the identities above it, and a guest is the responsibility of its
 * inviter, who may remove it (POS-05).
 */
function OrgsPage() {
  const t = useT();
  const orgs = useOrgs();
  const { org: current, select } = useCurrentOrg();
  const [creating, setCreating] = useState(false);
  const mine = orgs.data ?? [];

  const newOrg = (
    <Button onClick={() => setCreating(true)}>
      <PlusIcon aria-hidden="true" />
      {t("orgs.new")}
    </Button>
  );

  return (
    <>
      <PageHeader
        title={t("page.orgsTitle")}
        description={t("page.orgsBody")}
        actions={mine.length > 0 ? newOrg : null}
      />
      <Invitations />
      {orgs.isPending ? (
        <ul aria-hidden="true" className="divide-y border-b">
          {[0, 1].map((i) => (
            <li key={i} className="flex items-center gap-3 px-4 py-4 md:px-8">
              <Skeleton className="size-10" />
              <div className="space-y-2">
                <Skeleton className="h-4 w-40" />
                <Skeleton className="h-3 w-24" />
              </div>
            </li>
          ))}
        </ul>
      ) : orgs.isError ? (
        <div className="px-4 py-10 text-sm md:px-8" role="alert">
          <p className="text-destructive">{t("orgs.loadError")}</p>
          <Button variant="outline" size="sm" className="mt-3" onClick={() => orgs.refetch()}>
            {t("common.retry")}
          </Button>
        </div>
      ) : mine.length === 0 ? (
        <EmptyState icon={BuildingIcon} title={t("orgs.emptyTitle")} action={newOrg}>
          <p>{t("orgs.emptyBody")}</p>
        </EmptyState>
      ) : (
        <section aria-labelledby="orgs-yours">
          <h2 id="orgs-yours" className="sr-only">
            {t("orgs.yours")}
          </h2>
          <ul aria-label={t("orgs.yours")} className="divide-y border-b">
            {mine.map((o) => (
              <OrgRow
                key={o.id}
                org={{
                  id: o.id,
                  name: o.name,
                  slug: o.slug,
                  permission: o.permission,
                  ...(o.avatar_url ? { avatarUrl: o.avatar_url } : {}),
                }}
                current={o.id === current?.id}
                onSwitch={() => select(o.id)}
              />
            ))}
          </ul>
        </section>
      )}
      <NewOrgDialog open={creating} onOpenChange={setCreating} />
    </>
  );
}

function OrgRow({
  org,
  current,
  onSwitch,
}: {
  org: MyOrg;
  current: boolean;
  onSwitch: () => void;
}) {
  const t = useT();
  return (
    <li
      aria-label={org.name}
      className="flex flex-wrap items-center gap-x-3 gap-y-2 px-4 py-3.5 md:px-8"
    >
      <div className="flex min-w-0 flex-1 basis-44 items-center gap-3">
        <OrgAvatar org={org} size="lg" />
        <div className="min-w-0 flex-1">
          <p className="truncate font-semibold tracking-[-0.01em]">{org.name}</p>
          <p className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
            <span className="truncate font-mono">{org.slug}</span>
            <Badge variant="secondary" className="px-1.5 py-0 text-[0.68rem] leading-5">
              {permissionLabel(t, org.permission)}
            </Badge>
          </p>
        </div>
      </div>
      <div className="flex shrink-0 flex-wrap items-center gap-1">
        <Button asChild variant="ghost" size="sm">
          <Link
            to="/orgs/$orgId/chart"
            params={{ orgId: org.id }}
            aria-label={t("orgs.chartLink", { org: org.name })}
          >
            {t("orgs.tabChart")}
          </Link>
        </Button>
        <Button asChild variant="ghost" size="sm">
          <Link
            to="/orgs/$orgId/settings"
            params={{ orgId: org.id }}
            aria-label={t("orgs.settingsLink", { org: org.name })}
          >
            {t("orgs.tabSettings")}
          </Link>
        </Button>
        {current ? (
          <Badge variant="outline" className="shrink-0">
            {t("orgs.current")}
          </Badge>
        ) : (
          <Button
            variant="outline"
            size="sm"
            className="shrink-0"
            aria-label={t("orgs.switchTo", { org: org.name })}
            onClick={onSwitch}
          >
            {t("orgs.switch")}
          </Button>
        )}
      </div>
    </li>
  );
}

/** The invitations waiting for the viewer, or none at all (no heading, no list). */
function Invitations() {
  const t = useT();
  const id = useId();
  const invitations = useMyInvitations();
  const waiting = invitations.data ?? [];
  if (waiting.length === 0) return null;
  return (
    <section aria-labelledby={`${id}-title`} className="border-b bg-tape/20">
      <div className="px-4 pt-5 md:px-8">
        <h2 id={`${id}-title`} className="text-lg font-semibold tracking-[-0.015em]">
          {t("orgs.invitations")}
        </h2>
        <p className="text-sm text-muted-foreground">{t("orgs.invitationsBody")}</p>
      </div>
      <ul aria-label={t("orgs.invitationsLabel")} className="divide-y">
        {waiting.map((inv) => (
          <InvitationItem key={inv.id} invitation={inv} />
        ))}
      </ul>
    </section>
  );
}

const INVITED_AS: Record<string, MessageKey> = {
  admin: "orgs.inviteAdmin",
  member: "orgs.inviteMember",
  guest: "orgs.inviteGuest",
};

/** What accepting puts the invitee under, or nothing when it grants no one power over them (L-011). */
export function grantText(t: Translate, inv: Invitation, who: string): string | null {
  const guest = inv.permission === PermissionGuest;
  const position = inv.position_name;
  const vars = {
    who,
    org: inv.org_name,
    inviter: inv.invited_by.display_name,
    position: position ?? "",
  };
  if (position) return t(guest ? "orgs.grantPositionGuest" : "orgs.grantPosition", vars);
  if (guest) return t("orgs.grantGuest", vars);
  return null;
}

function InvitationItem({ invitation: inv }: { invitation: Invitation }) {
  const { t, locale } = useI18n();
  const sentenceId = useId();
  const me = useMe().data;
  const accept = useAcceptInvitation();
  const decline = useDeclineInvitation();
  const forMe = inv.invitee.id === me?.intelligence.id;
  const who = forMe ? t("orgs.you") : inv.invitee.display_name;
  const pending = accept.isPending || decline.isPending;
  const org = inv.org_name;

  const failed = (err: unknown) => {
    if (isApiError(err) && (err.status === 410 || err.status === 404)) {
      toast.error(t("orgs.expired", { org }));
    } else if (isApiError(err) && err.code === ErrorCodeAlreadyMember) {
      toast(t("orgs.alreadyIn", { org }));
    } else {
      toast.error(t("orgs.answerFailed", { org }));
    }
  };

  const sentence = t(INVITED_AS[inv.permission] ?? "orgs.inviteOther", {
    inviter: inv.invited_by.display_name,
    who,
    org,
  });
  const grants = grantText(t, inv, who);

  return (
    <li aria-label={t("orgs.invitationTo", { org })} className="space-y-3 px-4 py-4 md:px-8">
      <div className="flex items-start gap-3">
        <OrgAvatar org={{ id: inv.org_id, name: org }} size="lg" />
        <div className="min-w-0 flex-1 space-y-1">
          <p id={sentenceId} className="text-sm font-medium text-pretty">
            {sentence}
          </p>
          {inv.position_name ? (
            <p className="text-sm text-muted-foreground">
              {t("orgs.invitePosition", { position: inv.position_name })}
            </p>
          ) : null}
          {grants ? <p className="text-sm text-pretty">{grants}</p> : null}
          <p className="text-xs text-muted-foreground">
            <RelativeTime value={inv.expires_at} locale={locale}>
              {(when) => t("orgs.expires", { when })}
            </RelativeTime>
          </p>
        </div>
      </div>
      <div className="flex flex-wrap gap-2">
        <Button
          size="sm"
          disabled={pending}
          aria-describedby={sentenceId}
          onClick={() =>
            accept.mutate(inv.id, {
              onSuccess: () =>
                toast.success(
                  forMe
                    ? t("orgs.acceptedSelf", { org })
                    : t("orgs.acceptedAI", { name: inv.invitee.display_name, org }),
                ),
              onError: failed,
            })
          }
        >
          {t("orgs.accept")}
        </Button>
        <Button
          size="sm"
          variant="outline"
          disabled={pending}
          aria-describedby={sentenceId}
          onClick={() =>
            decline.mutate(inv.id, {
              onSuccess: () => toast(t("orgs.declined", { org })),
              onError: failed,
            })
          }
        >
          {t("orgs.decline")}
        </Button>
      </div>
    </li>
  );
}
