import { CrownIcon, GlobeIcon, PlusIcon, SearchIcon, XIcon } from "lucide-react";
import { type ReactNode, useEffect, useId, useMemo, useState } from "react";
import { toast } from "sonner";
import { VISIBILITY } from "@/components/room-visibility";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { Skeleton } from "@/components/ui/skeleton";
import { isApiError } from "@/lib/api/client";
import { type SharingResource, useSharing, useUpdateSharing, useUsers } from "@/lib/api/queries";
import {
  type Level,
  LevelManage,
  LevelPost,
  LevelUse,
  LevelView,
  type Sharing,
  type User,
  type Visibility,
  VisibilityPrivate,
  VisibilityPublic,
  VisibilityShared,
} from "@/lib/api/types.gen";
import { type MessageKey, type Translate, useI18n } from "@/lib/i18n";
import { cn } from "@/lib/utils";

/*
 * Who can reach a room, agent or connector (UI-11, ACL-07). One dialog for every resource kind:
 * rooms grant view/post/manage (public: view or post); agents and connectors grant `use` only.
 * The payload is exactly dto.Sharing; the summary always says who will have access.
 */

const LEVELS: Record<SharingResource, { shared: Level[]; public: Level[] }> = {
  rooms: { shared: [LevelView, LevelPost, LevelManage], public: [LevelView, LevelPost] },
  agents: { shared: [LevelUse], public: [LevelUse] },
  connectors: { shared: [LevelUse], public: [LevelUse] },
};

const LEVEL_LABEL: Record<Level, MessageKey> = {
  view: "sharing.level.view",
  post: "sharing.level.post",
  manage: "sharing.level.manage",
  use: "sharing.level.use",
};

const MODE_HINT: Record<SharingResource, Record<Visibility, MessageKey>> = {
  rooms: {
    private: "sharing.privateHint",
    shared: "sharing.sharedHint",
    public: "sharing.publicHint",
  },
  agents: {
    private: "sharing.privateHint",
    shared: "sharing.sharedHintUse",
    public: "sharing.publicHintUse",
  },
  connectors: {
    private: "sharing.privateHint",
    shared: "sharing.sharedHintUse",
    public: "sharing.publicHintUse",
  },
};

const TITLE: Record<SharingResource, MessageKey> = {
  rooms: "sharing.titleRoom",
  agents: "sharing.titleAgent",
  connectors: "sharing.titleConnector",
};

interface Grant {
  user_id: string;
  level: Level;
  name: string;
}

const displayName = (u: Pick<User, "display_name" | "username">) => u.display_name || u.username;

/** Builds the exact dto.Sharing request for the chosen mode. */
export function sharingPayload(
  mode: Visibility,
  grants: Pick<Grant, "user_id" | "level">[],
  publicLevel: Level,
): Sharing {
  if (mode === VisibilityShared) {
    return { mode, users: grants.map(({ user_id, level }) => ({ user_id, level })) };
  }
  if (mode === VisibilityPublic) return { mode, users: [], public_level: publicLevel };
  return { mode: VisibilityPrivate, users: [] };
}

export interface SharingDialogProps {
  kind: SharingResource;
  id: string;
  /** Resource name, for the title. */
  name: string;
  owner: User;
  trigger: ReactNode;
}

export function SharingDialog({ kind, id, name, owner, trigger }: SharingDialogProps) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>{trigger}</DialogTrigger>
      <DialogContent className="flex max-h-[min(44rem,calc(100dvh-2rem))] flex-col gap-0 p-0 sm:max-w-lg">
        <DialogHeader className="border-b px-5 pt-5 pb-4 text-left">
          <DialogTitle>{t(TITLE[kind], { name })}</DialogTitle>
          <DialogDescription>{t("sharing.body")}</DialogDescription>
        </DialogHeader>
        {open ? (
          <SharingForm kind={kind} id={id} owner={owner} onDone={() => setOpen(false)} />
        ) : null}
      </DialogContent>
    </Dialog>
  );
}

function SharingForm({
  kind,
  id,
  owner,
  onDone,
}: {
  kind: SharingResource;
  id: string;
  owner: User;
  onDone: () => void;
}) {
  const { t } = useI18n();
  const current = useSharing(kind, id);
  const update = useUpdateSharing(kind, id);
  const levels = LEVELS[kind];
  const defaultShared = levels.shared.includes(LevelPost) ? LevelPost : (levels.shared[0] as Level);

  const [mode, setMode] = useState<Visibility>(VisibilityPrivate);
  const [grants, setGrants] = useState<Grant[]>([]);
  const [publicLevel, setPublicLevel] = useState<Level>(levels.public[0] as Level);
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (loaded || !current.data) return;
    const s = current.data;
    setMode(s.mode);
    setGrants(
      s.users
        .filter((u) => u.user_id !== owner.id)
        .map((u) => ({
          user_id: u.user_id,
          level: levels.shared.includes(u.level) ? u.level : defaultShared,
          name: u.display_name || u.username || u.user_id,
        })),
    );
    if (s.public_level && levels.public.includes(s.public_level)) setPublicLevel(s.public_level);
    setLoaded(true);
  }, [current.data, loaded, owner.id, levels, defaultShared]);

  const save = () => {
    setError(null);
    update.mutate(sharingPayload(mode, grants, publicLevel), {
      onSuccess: () => {
        toast.success(t("sharing.saved"));
        onDone();
      },
      onError: (err) =>
        setError(
          isApiError(err) && err.status === 403 ? t("sharing.forbidden") : t("error.generic"),
        ),
    });
  };

  if (current.isError) {
    return (
      <p role="alert" className="px-5 py-6 text-sm text-destructive">
        {t("sharing.loadError")}
      </p>
    );
  }
  if (!loaded) {
    return (
      <div className="space-y-3 px-5 py-6" aria-hidden="true">
        <Skeleton className="h-14 w-full" />
        <Skeleton className="h-14 w-full" />
        <Skeleton className="h-14 w-full" />
      </div>
    );
  }

  const invalid = mode === VisibilityShared && grants.length === 0;

  return (
    <>
      <div className="min-h-0 flex-1 space-y-5 overflow-y-auto px-5 py-4">
        <RadioGroup
          value={mode}
          onValueChange={(v) => setMode(v as Visibility)}
          aria-label={t("sharing.visibility")}
          className="gap-2"
        >
          {([VisibilityPrivate, VisibilityShared, VisibilityPublic] as const).map((m) => (
            <ModeOption key={m} mode={m} hint={t(MODE_HINT[kind][m])} selected={mode === m} />
          ))}
        </RadioGroup>

        {mode === VisibilityShared ? (
          <SharedEditor
            owner={owner}
            grants={grants}
            levels={levels.shared}
            defaultLevel={defaultShared}
            onChange={setGrants}
          />
        ) : null}

        {mode === VisibilityPublic && levels.public.length > 1 ? (
          <div className="flex items-center justify-between gap-4 text-sm">
            <span>{t("sharing.everyoneCan")}</span>
            <LevelSelect
              value={publicLevel}
              levels={levels.public}
              onChange={setPublicLevel}
              label={t("sharing.levelForEveryone")}
            />
          </div>
        ) : null}

        <AccessSummary owner={owner} mode={mode} grants={grants} publicLevel={publicLevel} t={t} />
        {error ? (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        ) : null}
      </div>
      <DialogFooter className="border-t px-5 py-3">
        {invalid ? (
          <p className="mr-auto self-center text-xs text-muted-foreground">
            {t("sharing.pickSomeone")}
          </p>
        ) : null}
        <Button type="button" variant="outline" onClick={onDone}>
          {t("common.cancel")}
        </Button>
        <Button type="button" onClick={save} disabled={invalid || update.isPending}>
          {t("common.save")}
        </Button>
      </DialogFooter>
    </>
  );
}

function ModeOption({
  mode,
  hint,
  selected,
}: {
  mode: Visibility;
  hint: string;
  selected: boolean;
}) {
  const { t } = useI18n();
  const id = useId();
  const { icon: Icon, label } = VISIBILITY[mode];
  return (
    <label
      htmlFor={id}
      className={cn(
        "flex cursor-pointer items-start gap-3 rounded-md border px-3 py-2.5 transition-colors hover:bg-accent/50",
        selected && "border-tape-strong bg-tape/40 hover:bg-tape/50",
      )}
    >
      <RadioGroupItem id={id} value={mode} className="mt-0.5" />
      <Icon className="mt-0.5 size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
      <span className="min-w-0">
        <span className="block text-sm font-medium">{t(label)}</span>
        <span className="block text-xs text-muted-foreground">{hint}</span>
      </span>
    </label>
  );
}

function LevelSelect({
  value,
  levels,
  onChange,
  label,
}: {
  value: Level;
  levels: Level[];
  onChange: (level: Level) => void;
  label: string;
}) {
  const { t } = useI18n();
  return (
    <select
      value={value}
      onChange={(e) => onChange(e.target.value as Level)}
      aria-label={label}
      className="h-8 shrink-0 rounded-md border border-input bg-background px-2 text-sm shadow-xs outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 dark:bg-input/30"
    >
      {levels.map((l) => (
        <option key={l} value={l}>
          {t(LEVEL_LABEL[l])}
        </option>
      ))}
    </select>
  );
}

function SharedEditor({
  owner,
  grants,
  levels,
  defaultLevel,
  onChange,
}: {
  owner: User;
  grants: Grant[];
  levels: Level[];
  defaultLevel: Level;
  onChange: (grants: Grant[]) => void;
}) {
  const { t } = useI18n();
  const users = useUsers();
  const [query, setQuery] = useState("");
  const candidates = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return [];
    return (users.data ?? [])
      .filter((u) => u.id !== owner.id && !grants.some((g) => g.user_id === u.id))
      .filter(
        (u) => u.username.toLowerCase().includes(q) || u.display_name.toLowerCase().includes(q),
      )
      .slice(0, 6);
  }, [users.data, query, owner.id, grants]);

  const add = (u: User) => {
    onChange([...grants, { user_id: u.id, level: defaultLevel, name: displayName(u) }]);
    setQuery("");
  };

  return (
    <div className="space-y-2">
      <div className="relative">
        <SearchIcon
          className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground"
          aria-hidden="true"
        />
        <Input
          type="search"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder={t("sharing.searchPlaceholder")}
          aria-label={t("sharing.addPeople")}
          className="pl-9"
        />
      </div>
      {candidates.length > 0 ? (
        <ul className="rounded-md border bg-popover py-1 shadow-sm">
          {candidates.map((u) => (
            <li key={u.id}>
              <button
                type="button"
                onClick={() => add(u)}
                aria-label={t("sharing.add", { name: displayName(u) })}
                className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-sm outline-none hover:bg-accent focus-visible:bg-accent"
              >
                <PlusIcon className="size-3.5 text-muted-foreground" aria-hidden="true" />
                <span className="font-medium">{displayName(u)}</span>
                <span className="font-mono text-xs text-muted-foreground">@{u.username}</span>
              </button>
            </li>
          ))}
        </ul>
      ) : query.trim() && users.data ? (
        <p className="px-1 text-xs text-muted-foreground">{t("sharing.noUser")}</p>
      ) : null}
      {grants.length > 0 ? (
        <ul className="divide-y rounded-md border">
          {grants.map((g) => (
            <li key={g.user_id} className="flex items-center gap-2 px-3 py-2">
              <span className="min-w-0 flex-1 truncate text-sm">{g.name}</span>
              {levels.length > 1 ? (
                <LevelSelect
                  value={g.level}
                  levels={levels}
                  label={t("sharing.levelFor", { name: g.name })}
                  onChange={(level) =>
                    onChange(grants.map((x) => (x.user_id === g.user_id ? { ...x, level } : x)))
                  }
                />
              ) : (
                <span className="text-xs text-muted-foreground">{t(LEVEL_LABEL[g.level])}</span>
              )}
              <Button
                type="button"
                variant="ghost"
                size="icon-xs"
                aria-label={t("sharing.remove", { name: g.name })}
                onClick={() => onChange(grants.filter((x) => x.user_id !== g.user_id))}
              >
                <XIcon aria-hidden="true" />
              </Button>
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}

function AccessSummary({
  owner,
  mode,
  grants,
  publicLevel,
  t,
}: {
  owner: User;
  mode: Visibility;
  grants: Grant[];
  publicLevel: Level;
  t: Translate;
}) {
  const ownerName = displayName(owner);
  return (
    <section
      aria-label={t("sharing.whoCanAccess")}
      className="rounded-md bg-muted/60 px-3 py-2.5 text-sm"
    >
      <h3 className="pb-1.5 label-caps text-[0.68rem] text-muted-foreground">
        {t("sharing.whoCanAccess")}
      </h3>
      {mode === VisibilityPrivate ? (
        <p>{t("sharing.onlyOwner", { name: ownerName })}</p>
      ) : (
        <ul className="space-y-1">
          <li className="flex items-center gap-2">
            <CrownIcon className="size-3.5 text-muted-foreground" aria-hidden="true" />
            <span className="flex-1">{ownerName}</span>
            <span className="text-xs text-muted-foreground">{t("sharing.owner")}</span>
          </li>
          {mode === VisibilityShared
            ? grants.map((g) => (
                <li key={g.user_id} className="flex items-center gap-2">
                  <span className="size-3.5" aria-hidden="true" />
                  <span className="flex-1">{g.name}</span>
                  <span className="text-xs text-muted-foreground">
                    {t(`sharing.can.${g.level}` as MessageKey)}
                  </span>
                </li>
              ))
            : null}
          {mode === VisibilityPublic ? (
            <li className="flex items-center gap-2">
              <GlobeIcon className="size-3.5 text-muted-foreground" aria-hidden="true" />
              <span className="flex-1">{t("sharing.everyone")}</span>
              <span className="text-xs text-muted-foreground">
                {t(`sharing.can.${publicLevel}` as MessageKey)}
              </span>
            </li>
          ) : null}
        </ul>
      )}
    </section>
  );
}
