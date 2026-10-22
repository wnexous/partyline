import { zodResolver } from "@hookform/resolvers/zod";
import { createFileRoute, Link } from "@tanstack/react-router";
import { KeyRoundIcon, PlusIcon } from "lucide-react";
import { useId, useMemo, useState } from "react";
import { Controller, useForm } from "react-hook-form";
import { toast } from "sonner";
import { EmptyState } from "@/components/empty-state";
import { FormAlert, TextField } from "@/components/form-fields";
import { PageHeader } from "@/components/page-header";
import { SecretOnceDialog } from "@/components/secret-once-dialog";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Form } from "@/components/ui/form";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { isApiError } from "@/lib/api/client";
import { useCreateToken, usePurgeToken, useRevokeToken, useTokens } from "@/lib/api/queries";
import { type CreatedToken, type Token, TokenKindAgent, TokenKindUser } from "@/lib/api/types.gen";
import { applyFieldErrors } from "@/lib/forms";
import { type MessageKey, useI18n, useT } from "@/lib/i18n";
import { type CreateTokenForm, createTokenSchema, TOKEN_EXPIRY_DAYS } from "@/lib/schemas";
import { readStorage, writeStorage } from "@/lib/theme";
import { formatAbsolute, formatRelative } from "@/lib/time";
import { cn } from "@/lib/utils";

export const Route = createFileRoute("/_app/tokens")({
  component: TokensPage,
});

const DAY = 86_400_000;

/** Per-browser choice to list revoked tokens too (TOKH-04); hidden by default. */
const SHOW_REVOKED_KEY = "partyline.tokens.showRevoked";

const KIND_LABEL: Record<string, MessageKey> = {
  agent: "tokens.kind.agent",
  user: "tokens.kind.user",
};

const EXPIRY_LABEL: Record<(typeof TOKEN_EXPIRY_DAYS)[number], MessageKey> = {
  never: "tokens.expiry.never",
  "7": "tokens.expiry.7",
  "30": "tokens.expiry.30",
  "90": "tokens.expiry.90",
  "365": "tokens.expiry.365",
};

type TokenState = "active" | "expired" | "revoked";

function tokenState(token: Token, now = Date.now()): TokenState {
  if (token.revoked_at) return "revoked";
  if (token.expires_at && Date.parse(token.expires_at) <= now) return "expired";
  return "active";
}

function byStateThenNewest(a: Token, b: Token) {
  const rank = (t: Token) => (tokenState(t) === "active" ? 0 : 1);
  return rank(a) - rank(b) || Date.parse(b.created_at) - Date.parse(a.created_at);
}

function TokensPage() {
  const t = useT();
  const tokens = useTokens();
  const [creating, setCreating] = useState(false);
  const [revealed, setRevealed] = useState<CreatedToken | null>(null);
  const [revoking, setRevoking] = useState<Token | null>(null);
  const [deleting, setDeleting] = useState<Token | null>(null);
  const [showRevoked, setShowRevoked] = useState(() => readStorage(SHOW_REVOKED_KEY) === "1");
  const switchId = useId();
  const sorted = useMemo(() => [...(tokens.data ?? [])].sort(byStateThenNewest), [tokens.data]);
  const revokedCount = sorted.filter((t) => t.revoked_at).length;
  const visible = showRevoked ? sorted : sorted.filter((t) => !t.revoked_at);
  const toggleRevoked = (on: boolean) => {
    setShowRevoked(on);
    writeStorage(SHOW_REVOKED_KEY, on ? "1" : null);
  };

  const create = (
    <Button onClick={() => setCreating(true)}>
      <PlusIcon aria-hidden="true" />
      {t("tokens.new")}
    </Button>
  );

  return (
    <>
      <PageHeader
        title={t("page.tokensTitle")}
        description={t("page.tokensBody")}
        actions={
          revokedCount > 0 || visible.length > 0 ? (
            <>
              {revokedCount > 0 ? (
                <div className="flex items-center gap-2 pr-2 text-sm">
                  <Switch id={switchId} checked={showRevoked} onCheckedChange={toggleRevoked} />
                  <label htmlFor={switchId} className="flex items-center gap-1.5">
                    {t("tokens.showRevoked")}
                    <span className="font-mono text-xs text-muted-foreground tabular">
                      {revokedCount}
                    </span>
                  </label>
                </div>
              ) : null}
              {visible.length > 0 ? create : null}
            </>
          ) : null
        }
      />
      {tokens.isPending ? (
        <div className="space-y-3 px-4 py-6 md:px-8" aria-hidden="true">
          <Skeleton className="h-4 w-48" />
          <Skeleton className="h-4 w-64 max-w-full" />
        </div>
      ) : tokens.isError ? (
        <div className="px-4 py-10 text-sm md:px-8" role="alert">
          <p className="text-destructive">{t("tokens.loadError")}</p>
          <Button variant="outline" size="sm" className="mt-3" onClick={() => tokens.refetch()}>
            {t("common.retry")}
          </Button>
        </div>
      ) : visible.length === 0 ? (
        <EmptyState icon={KeyRoundIcon} title={t("tokens.emptyTitle")} action={create}>
          <p>{t("tokens.emptyBody")}</p>
        </EmptyState>
      ) : (
        <ul aria-label={t("tokens.listLabel")} className="divide-y border-b">
          {visible.map((token) => (
            <TokenRow
              key={token.id}
              token={token}
              onRevoke={() => setRevoking(token)}
              onDelete={() => setDeleting(token)}
            />
          ))}
        </ul>
      )}
      <CreateTokenDialog
        open={creating}
        onOpenChange={setCreating}
        onCreated={(created) => {
          setCreating(false);
          setRevealed(created);
        }}
      />
      <SecretOnceDialog
        secret={revealed?.secret ?? null}
        title={t("tokens.secretTitle")}
        description={t("tokens.secretBody")}
        label={revealed?.token.name ?? ""}
        warning={t("tokens.secretWarning")}
        confirmLabel={t("tokens.secretDone")}
        onClose={() => setRevealed(null)}
      >
        {revealed?.token.kind === TokenKindAgent ? (
          <p className="text-sm text-muted-foreground">
            {t("tokens.agentHint")}{" "}
            <Link to="/contacts" className="font-medium text-foreground underline">
              {t("connect.open")}
            </Link>
          </p>
        ) : null}
      </SecretOnceDialog>
      <RevokeTokenDialog token={revoking} onClose={() => setRevoking(null)} />
      <DeleteTokenDialog token={deleting} onClose={() => setDeleting(null)} />
    </>
  );
}

function TokenRow({
  token,
  onRevoke,
  onDelete,
}: {
  token: Token;
  onRevoke: () => void;
  onDelete: () => void;
}) {
  const { t, locale } = useI18n();
  const state = tokenState(token);
  return (
    <li
      aria-label={token.name}
      className={cn(
        "flex items-center gap-4 px-4 py-3.5 md:px-8",
        state !== "active" && "text-muted-foreground",
      )}
    >
      <div className="min-w-0 flex-1">
        <p className="flex min-w-0 flex-wrap items-center gap-2">
          <span
            className={cn(
              "truncate font-semibold tracking-[-0.01em]",
              state !== "active" && "line-through decoration-1",
            )}
          >
            {token.name}
          </span>
          <span className="shrink-0 rounded-[3px] border px-1.5 py-px text-[0.7rem] text-muted-foreground">
            {t(KIND_LABEL[token.kind] ?? "tokens.kind.user")}
          </span>
          {state === "active" ? null : (
            <span className="shrink-0 rounded-[3px] bg-muted px-1.5 py-px text-[0.7rem] font-medium">
              {state === "revoked" ? t("tokens.revoked") : t("tokens.expired")}
            </span>
          )}
        </p>
        <p className="mt-0.5 flex min-w-0 flex-wrap gap-x-3 gap-y-0.5 text-xs text-muted-foreground">
          <span className="font-mono">{token.prefix}…</span>
          <span title={formatAbsolute(token.created_at, locale)}>
            {t("tokens.created", { time: formatRelative(token.created_at, locale) })}
          </span>
          <span>
            {token.last_used_at
              ? t("tokens.lastUsed", { time: formatRelative(token.last_used_at, locale) })
              : t("tokens.neverUsed")}
          </span>
          {token.expires_at && state === "active" ? (
            <span title={formatAbsolute(token.expires_at, locale)}>
              {t("tokens.expires", { time: formatRelative(token.expires_at, locale) })}
            </span>
          ) : null}
        </p>
      </div>
      {state === "active" ? (
        <Button
          variant="outline"
          size="sm"
          aria-label={t("tokens.revoke", { name: token.name })}
          className="text-destructive hover:text-destructive"
          onClick={onRevoke}
        >
          {t("tokens.revokeShort")}
        </Button>
      ) : state === "revoked" ? (
        <Button
          variant="ghost"
          size="sm"
          aria-label={t("tokens.delete", { name: token.name })}
          className="text-muted-foreground hover:text-destructive"
          onClick={onDelete}
        >
          {t("tokens.deleteShort")}
        </Button>
      ) : null}
    </li>
  );
}

function CreateTokenDialog({
  open,
  onOpenChange,
  onCreated,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onCreated: (created: CreatedToken) => void;
}) {
  const t = useT();
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t("tokens.new")}</DialogTitle>
          <DialogDescription>{t("tokens.newBody")}</DialogDescription>
        </DialogHeader>
        {open ? <NewTokenForm onCreated={onCreated} onCancel={() => onOpenChange(false)} /> : null}
      </DialogContent>
    </Dialog>
  );
}

function NewTokenForm({
  onCreated,
  onCancel,
}: {
  onCreated: (created: CreatedToken) => void;
  onCancel: () => void;
}) {
  const t = useT();
  const create = useCreateToken();
  const ids = useId();
  const [formError, setFormError] = useState<string | null>(null);
  const form = useForm<CreateTokenForm>({
    resolver: zodResolver(createTokenSchema),
    defaultValues: { kind: TokenKindAgent, name: "", expiry: "never" },
  });

  const onSubmit = form.handleSubmit((values) => {
    setFormError(null);
    const { kind, name, expiry } = createTokenSchema.parse(values);
    create.mutate(
      {
        kind,
        name,
        ...(expiry === "never"
          ? {}
          : { expires_at: new Date(Date.now() + Number(expiry) * DAY).toISOString() }),
      },
      {
        onSuccess: onCreated,
        onError: (err) => {
          if (applyFieldErrors(err, form.setError, ["name", "kind"])) return;
          setFormError(
            isApiError(err) && err.status === 0 ? t("error.network") : t("error.generic"),
          );
        },
      },
    );
  });

  return (
    <Form {...form}>
      <form onSubmit={onSubmit} noValidate className="space-y-5">
        <FormAlert>{formError}</FormAlert>
        <fieldset className="space-y-2">
          <legend className="mb-2 text-sm font-medium">{t("tokens.kind")}</legend>
          <Controller
            control={form.control}
            name="kind"
            render={({ field }) => (
              <RadioGroup value={field.value} onValueChange={field.onChange} className="gap-2">
                {[TokenKindAgent, TokenKindUser].map((kind) => (
                  <label
                    key={kind}
                    htmlFor={`${ids}-${kind}`}
                    className={cn(
                      "flex cursor-pointer items-start gap-3 rounded-md border px-3 py-2.5 transition-colors hover:bg-accent/60",
                      field.value === kind && "border-tape-strong bg-tape/35 dark:bg-tape/12",
                    )}
                  >
                    <RadioGroupItem id={`${ids}-${kind}`} value={kind} className="mt-0.5" />
                    <span className="space-y-0.5">
                      <span className="block text-sm font-medium">
                        {t(KIND_LABEL[kind] as MessageKey)}
                      </span>
                      <span className="block text-xs text-muted-foreground">
                        {t(
                          kind === TokenKindAgent ? "tokens.kindAgentHint" : "tokens.kindUserHint",
                        )}
                      </span>
                    </span>
                  </label>
                ))}
              </RadioGroup>
            )}
          />
        </fieldset>
        <TextField
          control={form.control}
          name="name"
          label={t("tokens.name")}
          description={t("tokens.nameHint")}
          autoComplete="off"
        />
        <fieldset>
          <legend className="mb-2 text-sm font-medium">{t("tokens.expiry")}</legend>
          <Controller
            control={form.control}
            name="expiry"
            render={({ field }) => (
              <RadioGroup
                value={field.value}
                onValueChange={field.onChange}
                className="flex flex-wrap gap-1.5"
              >
                {TOKEN_EXPIRY_DAYS.map((value) => (
                  <label
                    key={value}
                    htmlFor={`${ids}-exp-${value}`}
                    className={cn(
                      "flex cursor-pointer items-center gap-2 rounded-md border px-2.5 py-1.5 text-sm transition-colors hover:bg-accent/60",
                      field.value === value && "border-tape-strong bg-tape/35 dark:bg-tape/12",
                    )}
                  >
                    <RadioGroupItem id={`${ids}-exp-${value}`} value={value} />
                    {t(EXPIRY_LABEL[value])}
                  </label>
                ))}
              </RadioGroup>
            )}
          />
        </fieldset>
        <DialogFooter>
          <Button type="button" variant="outline" onClick={onCancel}>
            {t("common.cancel")}
          </Button>
          <Button type="submit" disabled={create.isPending}>
            {create.isPending ? t("connect.creating") : t("connect.create")}
          </Button>
        </DialogFooter>
      </form>
    </Form>
  );
}

function RevokeTokenDialog({ token, onClose }: { token: Token | null; onClose: () => void }) {
  const t = useT();
  const revoke = useRevokeToken();
  return (
    <AlertDialog open={token !== null} onOpenChange={(open) => (open ? null : onClose())}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>
            {t("tokens.revokeTitle", { name: token?.name ?? "" })}
          </AlertDialogTitle>
          <AlertDialogDescription>{t("tokens.revokeBody")}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
          <AlertDialogAction
            className="bg-destructive text-white hover:bg-destructive/90"
            disabled={revoke.isPending}
            onClick={(e) => {
              e.preventDefault();
              if (!token) return;
              revoke.mutate(token.id, {
                onSuccess: () => {
                  toast(t("tokens.revokedToast", { name: token.name }));
                  onClose();
                },
                onError: () => toast.error(t("tokens.revokeFailed")),
              });
            }}
          >
            {t("tokens.revokeConfirm")}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

function DeleteTokenDialog({ token, onClose }: { token: Token | null; onClose: () => void }) {
  const t = useT();
  const purge = usePurgeToken();
  return (
    <AlertDialog open={token !== null} onOpenChange={(open) => (open ? null : onClose())}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>
            {t("tokens.deleteTitle", { name: token?.name ?? "" })}
          </AlertDialogTitle>
          <AlertDialogDescription>{t("tokens.deleteBody")}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
          <AlertDialogAction
            className="bg-destructive text-white hover:bg-destructive/90"
            disabled={purge.isPending}
            onClick={(e) => {
              e.preventDefault();
              if (!token) return;
              purge.mutate(token.id, {
                onSuccess: () => {
                  toast(t("tokens.deletedToast", { name: token.name }));
                  onClose();
                },
                onError: () => toast.error(t("tokens.deleteFailed")),
              });
            }}
          >
            {t("tokens.deleteConfirm")}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
