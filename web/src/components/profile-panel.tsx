import { useQuery } from "@tanstack/react-query";
import { Link, useRouter } from "@tanstack/react-router";
import { CompassIcon, Maximize2Icon, MessageSquareIcon, PencilIcon, XIcon } from "lucide-react";
import { type MouseEvent, useEffect, useState } from "react";
import { toast } from "sonner";
import { IntelligenceAvatar } from "@/components/intelligence/intelligence-avatar";
import { KindBadge } from "@/components/intelligence/kind-badge";
import { opensElsewhere } from "@/components/profile-link";
import { StatusDot } from "@/components/status-dot";
import { Button } from "@/components/ui/button";
import { Sheet, SheetClose, SheetContent, SheetTitle } from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import { useIsMobile } from "@/hooks/use-mobile";
import { useProfilePanelStore } from "@/hooks/use-profile-panel";
import { useStartConversation } from "@/hooks/use-start-conversation";
import { harnessLabel } from "@/lib/agents";
import { isApiError } from "@/lib/api/client";
import { intelligenceQuery, useMe } from "@/lib/api/queries";
import { type IntelligenceProfile, IntelligenceRolePerson } from "@/lib/api/types.gen";
import { useT } from "@/lib/i18n";
import { useLiveStore } from "@/lib/live/store";
import { cn } from "@/lib/utils";

/*
 * The profile panel (PRF-01..PRF-06): who an intelligence is, beside the page you were on. One
 * component for every place an identity can be clicked, mounted once in the app shell and opened
 * through `useProfilePanel().open(id)`. It is a sidebar on the right from 768 px of width and a
 * drawer from the bottom below it, built on the shared Sheet: focus is trapped inside, Escape and
 * the close button close it, and focus goes back to what opened it.
 *
 * It shows the avatar, name, @handle, kind and bio; for an AI also whether it is online and the
 * app it runs in. The actions are the ones the viewer may use: Message (DM-01, never for the
 * viewer or for a person known only through a chat platform: PDM-04, DM-05), Edit profile (the
 * viewer's own profile, when it says they may edit it) and Open full profile (the page, which also
 * has the editor of an AI the viewer manages).
 */

const BUTTON = "w-full max-md:h-11";

export function ProfilePanel() {
  const t = useT();
  const id = useProfilePanelStore((state) => state.id);
  const close = useProfilePanelStore((state) => state.close);
  const isMobile = useIsMobile();
  const router = useRouter();

  // The panel slides away showing what it showed: keep the profile until the next one opens.
  const [shown, setShown] = useState(id);
  if (id !== null && id !== shown) setShown(id);
  // Only an open panel watches the profile, so opening it again asks for it again (when stale),
  // and an old failure is not replayed.
  const profile = useQuery({ ...intelligenceQuery(shown ?? ""), enabled: id !== null });

  // The store outlives the panel: leaving the app shell (signing out) must not leave a profile
  // waiting to open for whoever comes next.
  useEffect(() => () => useProfilePanelStore.getState().dismiss(), []);

  // Another page closes it (a link in the panel, the browser's back button) and keeps the focus
  // the new page gives (PRF-05). A change of search or hash does not.
  useEffect(
    () =>
      router.subscribe("onBeforeLoad", (event) => {
        if (event.pathChanged) useProfilePanelStore.getState().dismiss();
      }),
    [router],
  );

  if (shown === null) return null;
  const notFound = profile.isError && isApiError(profile.error) && profile.error.status === 404;
  const heard = profile.data
    ? profile.data.display_name.trim() || profile.data.handle
    : profile.isPending
      ? t("common.loading")
      : "";

  return (
    <Sheet
      open={id !== null}
      onOpenChange={(open) => {
        if (!open) close();
      }}
    >
      <SheetContent
        side={isMobile ? "bottom" : "right"}
        data-layout={isMobile ? "drawer" : "sidebar"}
        aria-busy={profile.isPending}
        // The body says what it is; a description would repeat it.
        aria-describedby={undefined}
        showCloseButton={false}
        // Focus starts on the panel itself, so no action is one stray Enter away and the arrow
        // keys and Tab begin from the top.
        onOpenAutoFocus={(event) => {
          event.preventDefault();
          if (event.target instanceof HTMLElement) event.target.focus();
        }}
        // There is no trigger to hand focus back to: it goes to what opened the panel, if that is
        // still on the page (PRF-05).
        onCloseAutoFocus={(event) => {
          event.preventDefault();
          const { opener } = useProfilePanelStore.getState();
          if (opener?.isConnected) opener.focus();
        }}
        className={cn(
          "gap-0 p-0 data-[state=closed]:duration-200 data-[state=open]:duration-250",
          isMobile && "max-h-[85dvh] rounded-t-lg",
        )}
      >
        {/* Focus stays on the panel, named "Profile" when it opened: what arrives is said here. */}
        <p role="status" className="sr-only">
          {heard}
        </p>
        <div className="min-h-0 overflow-y-auto overscroll-contain">
          {profile.data ? (
            <ProfileCard profile={profile.data} />
          ) : notFound ? (
            <NotFound />
          ) : profile.isError ? (
            <Failed onRetry={() => void profile.refetch()} />
          ) : (
            <Loading />
          )}
        </div>
        {/* Outside the scrolling, so a short screen (a phone on its side) never hides them. */}
        {profile.data ? <ProfileActions profile={profile.data} /> : null}
        <SheetClose asChild>
          <Button
            variant="ghost"
            size="icon-sm"
            className="absolute top-2 right-2 max-md:size-11"
            aria-label={t("common.close")}
          >
            <XIcon aria-hidden="true" />
          </Button>
        </SheetClose>
      </SheetContent>
    </Sheet>
  );
}

/** The panel's name while there is no name to give it yet, or none to give. */
function HiddenTitle() {
  const t = useT();
  return <SheetTitle className="sr-only">{t("profilePanel.title")}</SheetTitle>;
}

function Loading() {
  return (
    <>
      <HiddenTitle />
      <div aria-hidden="true" className="space-y-5 px-5 py-5">
        <div className="flex items-start gap-4">
          <Skeleton className="size-24 shrink-0 rounded-full" />
          <div className="flex-1 space-y-2 pt-2">
            <Skeleton className="h-6 w-32 max-w-full" />
            <Skeleton className="h-4 w-20" />
          </div>
        </div>
        <div className="space-y-2">
          <Skeleton className="h-4 w-full" />
          <Skeleton className="h-4 w-3/4" />
        </div>
      </div>
    </>
  );
}

function Failed({ onRetry }: { onRetry: () => void }) {
  const t = useT();
  return (
    <>
      <HiddenTitle />
      <div role="alert" className="px-5 py-6 pr-14 text-sm">
        <p className="text-destructive">{t("profile.loadError")}</p>
        <Button variant="outline" size="sm" className="mt-3" onClick={onRetry}>
          {t("common.retry")}
        </Button>
      </div>
    </>
  );
}

function NotFound() {
  const t = useT();
  return (
    <>
      <HiddenTitle />
      <div className="flex items-start gap-3 px-5 py-6 pr-14">
        <CompassIcon className="mt-0.5 size-5 shrink-0 text-muted-foreground" aria-hidden="true" />
        <div className="space-y-1 text-sm">
          <p className="font-semibold">{t("profile.notFoundTitle")}</p>
          <p className="text-muted-foreground">{t("profile.notFoundBody")}</p>
        </div>
      </div>
    </>
  );
}

function ProfileCard({ profile }: { profile: IntelligenceProfile }) {
  const t = useT();
  const me = useMe().data;
  const presence = useLiveStore((state) => state.presence[profile.id]);
  const { ai } = profile;
  const online = presence?.online ?? ai?.online ?? false;
  const app = ai ? profile.apps[0]?.label || harnessLabel(ai.app) || ai.app : "";
  const name = profile.display_name.trim() || profile.handle;

  return (
    <>
      <div className="flex items-start gap-4 px-5 pt-5 pr-12">
        <IntelligenceAvatar intelligence={profile} size="xl" decorative />
        <div className="min-w-0 flex-1 space-y-1.5 pt-1">
          <SheetTitle className="text-xl leading-tight font-semibold tracking-[-0.015em] text-balance [overflow-wrap:anywhere]">
            {name}
          </SheetTitle>
          <p className="flex flex-wrap items-center gap-x-2 gap-y-1">
            <KindBadge intelligence={profile} viewerId={me?.intelligence.id} />
            {profile.handle ? (
              <span className="font-mono text-sm text-muted-foreground [overflow-wrap:anywhere]">
                @{profile.handle}
              </span>
            ) : null}
          </p>
        </div>
      </div>

      {profile.bio ? (
        <p className="mt-4 px-5 text-sm leading-relaxed text-pretty whitespace-pre-wrap [overflow-wrap:anywhere]">
          {profile.bio}
        </p>
      ) : null}

      <dl className="mt-5 grid grid-cols-[4.5rem_minmax(0,1fr)] items-center gap-x-4 gap-y-2.5 px-5 pb-5 text-sm">
        <dt className="text-muted-foreground">{t("profilePanel.kind")}</dt>
        <dd>{t(profile.kind === "ai" ? "intelligence.ai" : "profilePanel.kindPerson")}</dd>
        {ai ? (
          <>
            <dt className="text-muted-foreground">{t("profilePanel.status")}</dt>
            <dd>
              <StatusDot online={online} withLabel labelClassName="text-sm text-foreground" />
            </dd>
            {app ? (
              <>
                <dt className="text-muted-foreground">{t("profile.app")}</dt>
                <dd className="[overflow-wrap:anywhere]">{app}</dd>
              </>
            ) : null}
          </>
        ) : null}
      </dl>
    </>
  );
}

/**
 * What the viewer may do with this profile, the first that applies as the primary button: Message
 * (not for oneself, not for a person known only through a chat platform), Edit profile (one's own),
 * and always the full page.
 */
function ProfileActions({ profile }: { profile: IntelligenceProfile }) {
  const t = useT();
  const me = useMe().data;
  const own = me?.intelligence.id === profile.id;
  const canMessage = !own && profile.role !== IntelligenceRolePerson;
  const canEdit = own && profile.can_edit;
  // A click that opens the page in a new tab leaves this tab, and its panel, as it was.
  const dismiss = (event: MouseEvent) => {
    if (!opensElsewhere(event)) useProfilePanelStore.getState().dismiss();
  };

  return (
    <div className="flex shrink-0 flex-col gap-2 border-t px-5 py-4">
      {canMessage ? <MessageAction profile={profile} /> : null}
      {canEdit ? (
        <Button asChild variant={canMessage ? "outline" : "default"} className={BUTTON}>
          <Link to="/contacts/$id" params={{ id: profile.id }} onClick={dismiss}>
            <PencilIcon aria-hidden="true" />
            {t("profile.edit")}
          </Link>
        </Button>
      ) : null}
      <Button asChild variant={canMessage || canEdit ? "outline" : "default"} className={BUTTON}>
        <Link to="/contacts/$id" params={{ id: profile.id }} onClick={dismiss}>
          <Maximize2Icon aria-hidden="true" />
          {t("profilePanel.openFull")}
        </Link>
      </Button>
    </div>
  );
}

/** "Message": opens the one DM with this intelligence, creating it when there is none (DM-01). */
function MessageAction({ profile }: { profile: IntelligenceProfile }) {
  const t = useT();
  const { start, pending } = useStartConversation({ onError: (message) => toast.error(message) });
  return (
    <Button
      disabled={pending}
      className={BUTTON}
      onClick={() => {
        // When the DM is another page, the navigation dismisses the panel (and no focus goes back);
        // when it is the one already open nothing changes, and focus returns to what was clicked.
        void start(profile).then((started) => {
          if (started) useProfilePanelStore.getState().close();
        });
      }}
    >
      <MessageSquareIcon aria-hidden="true" />
      {t("contacts.message")}
    </Button>
  );
}
