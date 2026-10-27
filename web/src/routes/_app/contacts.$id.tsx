import { useQuery } from "@tanstack/react-query";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import {
  ArrowLeftIcon,
  CompassIcon,
  GitMergeIcon,
  GlobeIcon,
  PlugZapIcon,
  PowerOffIcon,
  TerminalIcon,
  Trash2Icon,
  UserMinusIcon,
} from "lucide-react";
import { type ReactNode, useId, useState } from "react";
import { DeleteAIDialog } from "@/components/delete-ai-dialog";
import { EmptyState } from "@/components/empty-state";
import { EndSessionDialog } from "@/components/end-session-dialog";
import { IntelligenceAvatar } from "@/components/intelligence/intelligence-avatar";
import { IntelligenceName } from "@/components/intelligence/intelligence-name";
import { YouBadge } from "@/components/intelligence/you-badge";
import { MembershipSummary } from "@/components/membership-summary";
import { MergeAIDialog } from "@/components/merge-ai-dialog";
import { MessageButton } from "@/components/message-button";
import { platformLabel } from "@/components/message-item";
import { ProfileConversations } from "@/components/profile-conversations";
import { ProfileEditor } from "@/components/profile-editor";
import { RemoveFromOrgDialog } from "@/components/remove-from-org-dialog";
import { StatusDot } from "@/components/status-dot";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { harnessLabel } from "@/lib/agents";
import { isApiError } from "@/lib/api/client";
import { contactsQuery, useIntelligence, useMe } from "@/lib/api/queries";
import {
  type AIDetail,
  type IntelligenceProfile,
  IntelligenceRolePerson,
  LevelManage,
  type PersonDetail,
  type ProfileMembership,
} from "@/lib/api/types.gen";
import { mergeTargets } from "@/lib/contacts";
import { useI18n, useT } from "@/lib/i18n";
import { useLiveStore } from "@/lib/live/store";
import { formatAbsolute, formatRelative } from "@/lib/time";

export const Route = createFileRoute("/_app/contacts/$id")({
  component: ProfilePage,
});

/*
 * The profile of an intelligence (PROF-03, PROF-05, PROF-11, PEOP-03, ACC-07): who it is, where
 * it lives (an AI: its folder and app; a person: the account it writes from), the organizations
 * the viewer shares with it, the apps it speaks through (the connectors it operates among them)
 * and, for a person, the conversations the viewer can see with it. What the viewer may do about it
 * comes from the profile's flags (POS-04, POS-05, POS-06): whoever may edit it gets the editor,
 * whoever manages an AI may end its session or merge it, and each organization offers "Delete"
 * (an AI in its home organization) or "Remove from" (anyone else) where the server allows it.
 */

function ProfilePage() {
  const t = useT();
  const { id } = Route.useParams();
  const profile = useIntelligence(id);

  if (profile.isError && isApiError(profile.error) && profile.error.status === 404) {
    return <ProfileNotFound />;
  }
  if (profile.isError && !profile.data) {
    return (
      <div className="px-4 py-10 text-sm md:px-8" role="alert">
        <p className="text-destructive">{t("profile.loadError")}</p>
        <Button variant="outline" size="sm" className="mt-3" onClick={() => profile.refetch()}>
          {t("common.retry")}
        </Button>
      </div>
    );
  }
  if (!profile.data) return <ProfileSkeleton />;
  return <ProfileView profile={profile.data} />;
}

function BackToContacts() {
  const t = useT();
  return (
    <Link
      to="/contacts"
      className="-ml-1 inline-flex items-center gap-1.5 rounded-md px-1 py-1 text-sm text-muted-foreground outline-none hover:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/70"
    >
      <ArrowLeftIcon className="size-4" aria-hidden="true" />
      {t("profile.back")}
    </Link>
  );
}

function ProfileNotFound() {
  const t = useT();
  return (
    <EmptyState
      icon={CompassIcon}
      title={t("profile.notFoundTitle")}
      action={
        <Button asChild variant="outline">
          <Link to="/contacts">{t("profile.backToContacts")}</Link>
        </Button>
      }
    >
      <p>{t("profile.notFoundBody")}</p>
    </EmptyState>
  );
}

function ProfileSkeleton() {
  return (
    <div aria-hidden="true" className="space-y-5 px-4 py-6 md:px-8">
      <Skeleton className="h-4 w-20" />
      <div className="flex items-center gap-5">
        <Skeleton className="size-24 rounded-full" />
        <div className="space-y-2">
          <Skeleton className="h-7 w-48" />
          <Skeleton className="h-4 w-32" />
        </div>
      </div>
    </div>
  );
}

function ProfileView({ profile }: { profile: IntelligenceProfile }) {
  const { t, locale } = useI18n();
  const me = useMe().data;
  const navigate = useNavigate();
  const [deleting, setDeleting] = useState(false);
  const [merging, setMerging] = useState(false);
  const [ending, setEnding] = useState(false);
  const [removing, setRemoving] = useState<ProfileMembership | null>(null);
  const presence = useLiveStore((s) => s.presence[profile.id]);
  const own = me?.intelligence.id === profile.id;
  const isPerson = profile.role === IntelligenceRolePerson;
  const manages = profile.ai?.my_level === LevelManage;
  const name = profile.display_name;
  // The targets of a merge are the caller's own AIs, which only the owner needs (MRG-01).
  const contacts = useQuery({ ...contactsQuery(), enabled: manages });
  const mergeCandidates = contacts.data ? mergeTargets(contacts.data, profile.id) : [];
  const canMerge = manages && mergeCandidates.length > 0;
  // There is a session to end only while the AI is connected.
  const canEnd = profile.can_end_session && (presence?.online ?? profile.ai?.online ?? false);

  return (
    <div className="mx-auto w-full max-w-3xl space-y-8 px-4 py-5 md:px-8 md:py-8">
      <BackToContacts />

      <header className="flex flex-wrap items-start gap-x-5 gap-y-4">
        <IntelligenceAvatar intelligence={profile} size="xl" decorative />
        <div className="min-w-0 flex-1 basis-48 space-y-2">
          <h1 className="text-2xl font-semibold tracking-[-0.02em] text-balance [overflow-wrap:anywhere]">
            {name}
          </h1>
          <p className="flex flex-wrap items-center gap-x-2 gap-y-1">
            <YouBadge intelligence={profile} viewerId={me?.intelligence.id} />
            <span className="font-mono text-sm text-muted-foreground">@{profile.handle}</span>
          </p>
          {profile.bio ? (
            <p className="max-w-[65ch] text-sm text-pretty whitespace-pre-wrap [overflow-wrap:anywhere]">
              {profile.bio}
            </p>
          ) : null}
        </div>
        <div className="flex w-full flex-wrap items-center gap-2 sm:w-auto">
          {own || isPerson ? null : <MessageButton target={profile} compact={false} />}
          {canMerge ? (
            <Button
              variant="outline"
              size="sm"
              aria-label={t("ai.mergeAction", { name })}
              onClick={() => setMerging(true)}
            >
              <GitMergeIcon aria-hidden="true" />
              <span className="max-sm:hidden">{t("ai.mergeShort")}</span>
            </Button>
          ) : null}
          {canEnd ? (
            <Button variant="outline" size="sm" onClick={() => setEnding(true)}>
              <PowerOffIcon aria-hidden="true" />
              {t("profile.endSession")}
            </Button>
          ) : null}
        </div>
      </header>

      {/* A person's profile is history: no editor and no message (CID-21, CID-22). */}
      {profile.can_edit && !isPerson ? <ProfileEditor profile={profile} /> : null}

      {profile.ai || profile.person ? (
        <Section title={t("profile.details")}>
          <dl className="grid gap-x-6 gap-y-3 text-sm sm:grid-cols-[9rem_minmax(0,1fr)]">
            {profile.ai ? <AIDetails ai={profile.ai} id={profile.id} /> : null}
            {profile.person ? <PersonDetails person={profile.person} /> : null}
          </dl>
          {profile.person?.name_locked ? (
            <p className="text-xs text-muted-foreground">{t("profile.nameLocked")}</p>
          ) : null}
        </Section>
      ) : null}

      {/* The AI's live sessions, the principal first (SESS-32): a fact of the profile, and the
          addresses the mention list offers. Absent while the AI has no live session. */}
      {profile.ai && profile.ai.sessions.length > 0 ? (
        <Section title={t("profile.sessions")}>
          <ul aria-label={t("profile.sessions")} className="divide-y rounded-lg border">
            {profile.ai.sessions.map((session) => (
              <li
                key={session.label || "principal"}
                className="flex flex-wrap items-baseline gap-x-2 gap-y-1 px-4 py-3 text-sm"
              >
                <span
                  data-testid="session-label"
                  className="min-w-0 font-mono text-xs [overflow-wrap:anywhere]"
                >
                  {session.label ? `:${session.label}` : t("profile.sessionPrincipal")}
                </span>
                <span className="min-w-0 text-muted-foreground [overflow-wrap:anywhere]">
                  {harnessLabel(session.harness) ?? session.harness}
                </span>
                <time
                  dateTime={session.connected_at}
                  title={formatAbsolute(session.connected_at, locale)}
                  className="ml-auto shrink-0 font-mono text-xs text-muted-foreground tabular"
                >
                  {t("profile.sessionConnected", {
                    time: formatRelative(session.connected_at, locale),
                  })}
                </time>
              </li>
            ))}
          </ul>
        </Section>
      ) : null}

      {profile.memberships.length > 0 ? (
        <Section title={t("profile.orgs")}>
          <ul className="divide-y rounded-lg border">
            {profile.memberships.map((m) => (
              <MembershipRow
                key={m.org_id}
                membership={m}
                name={name}
                onDelete={() => setDeleting(true)}
                onRemove={() => setRemoving(m)}
              />
            ))}
          </ul>
        </Section>
      ) : null}

      {profile.apps.length > 0 ? (
        <Section title={t("profile.apps")}>
          <ul className="divide-y rounded-lg border">
            {profile.apps.map((app) => (
              <li key={`${app.kind}:${app.connector_id ?? ""}:${app.label}`}>
                <AppRow app={app} />
              </li>
            ))}
          </ul>
        </Section>
      ) : null}

      {isPerson || profile.conversations.length > 0 ? (
        <Section title={t("profile.conversations")}>
          <ProfileConversations conversations={profile.conversations} />
        </Section>
      ) : null}

      <DeleteAIDialog
        ai={deleting ? profile : null}
        onClose={() => setDeleting(false)}
        onDeleted={() => void navigate({ to: "/contacts" })}
      />
      <RemoveFromOrgDialog
        identity={profile}
        membership={removing}
        onClose={() => setRemoving(null)}
      />
      <EndSessionDialog ai={ending ? profile : null} onClose={() => setEnding(false)} />
      <MergeAIDialog
        ai={merging ? profile : null}
        candidates={mergeCandidates}
        onClose={() => setMerging(false)}
        onMerged={(survivorId) =>
          void navigate({ to: "/contacts/$id", params: { id: survivorId } })
        }
      />
    </div>
  );
}

/**
 * One organization the viewer shares with the identity: what it is there and, where the server
 * allows it (POS-05), the way to take it out. The organization an AI belongs to deletes it for
 * good; anywhere else the identity is only removed from that organization (POS-06).
 */
function MembershipRow({
  membership: m,
  name,
  onDelete,
  onRemove,
}: {
  membership: ProfileMembership;
  name: string;
  onDelete: () => void;
  onRemove: () => void;
}) {
  const t = useT();
  const home = m.delete_effect === "delete";
  return (
    <li aria-label={m.org_name}>
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3">
        <MembershipSummary membership={m} />
        {m.can_delete ? (
          home ? (
            <Button
              variant="outline"
              size="sm"
              className="text-destructive hover:text-destructive"
              aria-label={t("ai.delete", { name })}
              onClick={onDelete}
            >
              <Trash2Icon aria-hidden="true" />
              {t("ai.deleteShort")}
            </Button>
          ) : (
            <Button
              variant="outline"
              size="sm"
              className="text-destructive hover:text-destructive"
              onClick={onRemove}
            >
              <UserMinusIcon aria-hidden="true" />
              {t("profile.removeFrom", { org: m.org_name })}
            </Button>
          )
        ) : null}
      </div>
    </li>
  );
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  const id = useId();
  return (
    <section aria-labelledby={id} className="space-y-3">
      <h2 id={id} className="text-lg font-semibold tracking-[-0.015em]">
        {title}
      </h2>
      {children}
    </section>
  );
}

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="min-w-0 [overflow-wrap:anywhere]">{children}</dd>
    </>
  );
}

/** Where an AI lives and how it is doing: host, app and state, folder and the viewer's access. */
function AIDetails({ ai, id }: { ai: AIDetail; id: string }) {
  const { t, locale } = useI18n();
  const presence = useLiveStore((s) => s.presence[id]);
  const online = presence?.online ?? ai.online;
  const lastSeen = presence?.last_seen_at ?? ai.last_seen_at;
  const app = ai.app ? (harnessLabel(ai.app) ?? ai.app) : "";
  return (
    <>
      <Row label={t("profile.owner")}>
        <Link
          to="/contacts/$id"
          params={{ id: ai.owner.id }}
          className="rounded-sm outline-none hover:underline focus-visible:ring-2 focus-visible:ring-ring"
        >
          <IntelligenceName intelligence={ai.owner} showHandle />
        </Link>
      </Row>
      <Row label={t("profile.app")}>
        <span className="inline-flex flex-wrap items-center gap-x-3 gap-y-1">
          {app ? <span>{app}</span> : null}
          <StatusDot online={online} withLabel />
          {online ? null : lastSeen ? (
            <span className="font-mono text-xs tabular" title={formatAbsolute(lastSeen, locale)}>
              {t("contacts.lastSeen", { time: formatRelative(lastSeen, locale) })}
            </span>
          ) : (
            <span className="text-xs text-muted-foreground">{t("contacts.neverSeen")}</span>
          )}
        </span>
      </Row>
      <Row label={t("profile.folder")}>
        {ai.folder_label ? (
          <span className="font-mono text-xs">{ai.folder_label}</span>
        ) : (
          <span className="text-muted-foreground">{t("profile.noFolder")}</span>
        )}
      </Row>
      {ai.my_level ? (
        <Row label={t("profile.access")}>
          {ai.my_level === LevelManage ? t("profile.accessManage") : t("profile.accessUse")}
        </Row>
      ) : null}
    </>
  );
}

/** The account a person writes from, who it is verified as, and whether its name is kept (PEOP-03). */
function PersonDetails({ person }: { person: PersonDetail }) {
  const t = useT();
  return (
    <>
      <Row label={t("profile.connector")}>
        <Link
          to="/connectors/$connectorId"
          params={{ connectorId: person.connector.id }}
          className="inline-flex flex-wrap items-baseline gap-x-2 rounded-sm outline-none hover:underline focus-visible:ring-2 focus-visible:ring-ring"
        >
          <span>{person.connector.name}</span>
          <span className="text-xs text-muted-foreground">
            {platformLabel(person.connector.kind)}
          </span>
        </Link>
      </Row>
      <Row label={t("profile.account")}>
        <span className="font-mono text-xs">{person.external_id}</span>
      </Row>
      {person.linked_user ? (
        <Row label={t("profile.linkedTo")}>
          <Link
            to="/contacts/$id"
            params={{ id: person.linked_user.id }}
            className="rounded-sm outline-none hover:underline focus-visible:ring-2 focus-visible:ring-ring"
          >
            <IntelligenceName intelligence={person.linked_user} showHandle />
          </Link>
        </Row>
      ) : null}
    </>
  );
}

function appIcon(kind: string) {
  if (kind === "web") return GlobeIcon;
  if (harnessLabel(kind)) return TerminalIcon;
  return PlugZapIcon;
}

/** One app: where the intelligence speaks. A connector's app links to the connector (ACC-07). */
function AppRow({ app }: { app: IntelligenceProfile["apps"][number] }) {
  const Icon = appIcon(app.kind);
  const body = (
    <>
      <Icon className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
      <span className="min-w-0 [overflow-wrap:anywhere]">{app.label}</span>
    </>
  );
  const row = "flex items-center gap-3 px-4 py-3 text-sm";
  return app.connector_id ? (
    <Link
      to="/connectors/$connectorId"
      params={{ connectorId: app.connector_id }}
      className={`${row} outline-none hover:bg-accent/40 focus-visible:bg-accent/40 focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset`}
    >
      {body}
    </Link>
  ) : (
    <div className={row}>{body}</div>
  );
}
