import { zodResolver } from "@hookform/resolvers/zod";
import { PlusIcon, SlidersHorizontalIcon, XIcon } from "lucide-react";
import { type FormEvent, type KeyboardEvent, useId, useState } from "react";
import { useForm } from "react-hook-form";
import { toast } from "sonner";
import { FieldError, FormAlert } from "@/components/form-fields";
import { Button } from "@/components/ui/button";
import {
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  Form as FormProvider,
  useFormField,
} from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { isCoarsePointer } from "@/hooks/use-auto-focus";
import { isApiError } from "@/lib/api/client";
import { useUpdateMember } from "@/lib/api/queries";
import {
  type DeliveryMode,
  DeliveryModeAll,
  DeliveryModeMentions,
  DeliveryModeTrusted,
  MaxMemberKeywords,
  type RoomMember,
  type UpdateMemberRequest,
} from "@/lib/api/types.gen";
import { applyFieldErrors, errorText } from "@/lib/forms";
import { type MessageKey, useI18n } from "@/lib/i18n";
import {
  keywordError,
  type MemberSettingsForm,
  memberSettingsSchema,
  validationVars,
} from "@/lib/schemas";
import { cn } from "@/lib/utils";

/*
 * Per-room settings of an agent member (MEM-01..05): the name it uses in this room and which room
 * messages it receives. Signatures and outbound delivery belonged to chats, which are no longer
 * room members (CID-22), so they are gone. Managers only; the member list decides who sees it.
 */

const DELIVERY: { mode: DeliveryMode; label: MessageKey; hint: MessageKey }[] = [
  {
    mode: DeliveryModeAll,
    label: "memberSettings.delivery.all",
    hint: "memberSettings.delivery.allHint",
  },
  {
    mode: DeliveryModeMentions,
    label: "memberSettings.delivery.mentions",
    hint: "memberSettings.delivery.mentionsHint",
  },
  {
    mode: DeliveryModeTrusted,
    label: "memberSettings.delivery.trusted",
    hint: "memberSettings.delivery.trustedHint",
  },
];

/** The short, non-default settings of an agent member, for the member row. */
export function memberSettingsSummary(m: RoomMember): MessageKey[] {
  const out: MessageKey[] = [];
  if (m.delivery === DeliveryModeMentions) out.push("memberSettings.mentionsOnly");
  if (m.delivery === DeliveryModeTrusted) out.push("memberSettings.trustedOnly");
  return out;
}

export function MemberSettings({ roomId, member }: { roomId: string; member: RoomMember }) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          variant="ghost"
          size="icon-xs"
          className="shrink-0 text-muted-foreground opacity-60 group-hover/member:opacity-100 focus-visible:opacity-100 data-[state=open]:opacity-100"
          aria-label={t("memberSettings.open", { name: member.name })}
        >
          <SlidersHorizontalIcon aria-hidden="true" />
        </Button>
      </PopoverTrigger>
      <PopoverContent
        align="end"
        collisionPadding={16}
        className="flex max-h-(--radix-popover-content-available-height) w-[min(21rem,calc(100vw-2rem))] flex-col overscroll-contain p-0"
        aria-labelledby={`${member.id}-settings-title`}
        onOpenAutoFocus={(e) => {
          // A coarse pointer would open the on-screen keyboard over the form (UI-24).
          if (!isCoarsePointer()) return;
          e.preventDefault();
          if (e.target instanceof HTMLElement) e.target.focus();
        }}
      >
        {open ? (
          <SettingsForm roomId={roomId} member={member} onDone={() => setOpen(false)} />
        ) : null}
      </PopoverContent>
    </Popover>
  );
}

function changes(m: RoomMember, v: MemberSettingsForm): UpdateMemberRequest {
  const req: UpdateMemberRequest = {};
  const name = v.display_name.trim();
  if (name !== m.display_name) req.display_name = name;
  if (v.delivery !== m.delivery) req.delivery = v.delivery;
  if (v.keywords.join("\n") !== m.keywords.join("\n")) req.keywords = v.keywords;
  return req;
}

function SettingsForm({
  roomId,
  member,
  onDone,
}: {
  roomId: string;
  member: RoomMember;
  onDone: () => void;
}) {
  const { t } = useI18n();
  const update = useUpdateMember(roomId);
  const [formError, setFormError] = useState<string | null>(null);
  const form = useForm<MemberSettingsForm>({
    resolver: zodResolver(memberSettingsSchema),
    defaultValues: {
      display_name: member.display_name,
      delivery: member.delivery,
      keywords: member.keywords,
    },
  });
  const delivery = form.watch("delivery");
  // The keyword being typed; Save adds it like Enter does (A11Y-02).
  const [draft, setDraft] = useState("");
  const [draftError, setDraftError] = useState<MessageKey | null>(null);
  const commitDraft = (): boolean => {
    const word = draft.trim();
    const err = keywordError(word);
    if (err) {
      setDraftError(err);
      return false;
    }
    setDraftError(null);
    setDraft("");
    const list = form.getValues("keywords");
    if (!list.some((k) => k.toLowerCase() === word.toLowerCase())) {
      form.setValue("keywords", [...list, word], { shouldDirty: true });
    }
    return true;
  };

  const onSubmit = form.handleSubmit((values) => {
    setFormError(null);
    const req = changes(member, values);
    if (Object.keys(req).length === 0) {
      onDone();
      return;
    }
    update.mutate(
      { memberId: member.id, ...req },
      {
        onSuccess: () => {
          toast.success(t("memberSettings.saved", { name: member.name }));
          onDone();
        },
        onError: (err) => {
          const fields = ["display_name", "keywords"] as const;
          const applied = applyFieldErrors(err, form.setError, fields);
          const rest =
            isApiError(err) && err.status === 422
              ? Object.entries(err.fields)
                  .filter(([name]) => !(fields as readonly string[]).includes(name))
                  .map(([, reason]) => reason)
              : [];
          if (applied && rest.length === 0) return;
          setFormError(
            rest.length > 0
              ? rest.join(" ")
              : isApiError(err) && err.status === 403
                ? t("settings.forbidden")
                : t("error.generic"),
          );
        },
      },
    );
  });

  const save = (e: FormEvent<HTMLFormElement>) => {
    if (delivery === DeliveryModeMentions && draft.trim() !== "" && !commitDraft()) {
      e.preventDefault();
      return;
    }
    return onSubmit(e);
  };

  const titleId = `${member.id}-settings-title`;
  return (
    <FormProvider {...form}>
      <form onSubmit={save} noValidate className="flex min-h-0 flex-1 flex-col">
        <div className="border-b px-4 pt-3.5 pb-3">
          <h2 id={titleId} className="text-sm font-semibold">
            {t("memberSettings.title")}
          </h2>
          <p className="text-xs text-muted-foreground">
            {t("memberSettings.body", { name: member.name })}
          </p>
        </div>
        <div className="min-h-0 flex-1 space-y-4 overflow-y-auto overscroll-contain px-4 py-4">
          <FormAlert>{formError}</FormAlert>
          <FormField
            control={form.control}
            name="display_name"
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t("memberSettings.displayName")}</FormLabel>
                <FormControl>
                  <Input
                    {...field}
                    placeholder={member.name}
                    autoComplete="off"
                    spellCheck={false}
                  />
                </FormControl>
                <FormDescription className="text-xs">
                  {t("memberSettings.displayNameHint", { name: member.name })}
                </FormDescription>
                <FieldError />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name="delivery"
            render={({ field }) => (
              <FormItem>
                <FormLabel asChild>
                  <span>{t("memberSettings.delivery")}</span>
                </FormLabel>
                <FormControl>
                  <RadioGroup
                    value={field.value}
                    onValueChange={field.onChange}
                    aria-label={t("memberSettings.delivery")}
                    className="gap-0 overflow-hidden rounded-md border"
                  >
                    {DELIVERY.map((d) => (
                      <DeliveryOption
                        key={d.mode}
                        mode={d.mode}
                        label={t(d.label)}
                        hint={t(d.hint)}
                        selected={field.value === d.mode}
                      />
                    ))}
                  </RadioGroup>
                </FormControl>
                {/* MEM-06: mentions mode is about noise, not safety. */}
                <FormDescription className="text-xs">
                  {t("memberSettings.deliveryHint")}
                </FormDescription>
              </FormItem>
            )}
          />
          {delivery === DeliveryModeMentions ? (
            <KeywordsField
              draft={draft}
              draftError={draftError}
              onDraft={(v) => {
                setDraft(v);
                setDraftError(null);
              }}
              onCommit={commitDraft}
            />
          ) : null}
        </div>
        <div className="flex justify-end gap-2 border-t px-4 py-3">
          <Button type="button" variant="outline" size="sm" onClick={onDone}>
            {t("common.cancel")}
          </Button>
          <Button type="submit" size="sm" disabled={update.isPending}>
            {t("common.save")}
          </Button>
        </div>
      </form>
    </FormProvider>
  );
}

function DeliveryOption({
  mode,
  label,
  hint,
  selected,
}: {
  mode: DeliveryMode;
  label: string;
  hint: string;
  selected: boolean;
}) {
  const id = useId();
  return (
    <label
      htmlFor={id}
      className={cn(
        "flex cursor-pointer items-start gap-2.5 border-b px-3 py-2 transition-colors last:border-b-0 hover:bg-accent/50",
        selected && "bg-tape/25 hover:bg-tape/30 dark:bg-tape/10",
      )}
    >
      <RadioGroupItem id={id} value={mode} className="mt-0.5" aria-describedby={`${id}-hint`} />
      <span className="min-w-0">
        <span className="block text-sm font-medium">{label}</span>
        <span id={`${id}-hint`} className="block text-xs text-muted-foreground">
          {hint}
        </span>
      </span>
    </label>
  );
}

/**
 * Keywords as removable chips plus one input; Enter, the add button or Save commits the draft.
 * The draft message is a live region that also describes the input (A11Y-01).
 */
function KeywordsField(props: {
  draft: string;
  draftError: MessageKey | null;
  onDraft: (value: string) => void;
  onCommit: () => boolean;
}) {
  return (
    <FormField
      name="keywords"
      render={({ field }) => (
        <FormItem>
          <KeywordsBody {...props} list={field.value as string[]} onChange={field.onChange} />
        </FormItem>
      )}
    />
  );
}

function KeywordsBody({
  draft,
  draftError,
  onDraft,
  onCommit,
  list,
  onChange,
}: {
  draft: string;
  draftError: MessageKey | null;
  onDraft: (value: string) => void;
  onCommit: () => boolean;
  list: string[];
  onChange: (list: string[]) => void;
}) {
  const { t } = useI18n();
  const { error, formDescriptionId, formMessageId } = useFormField();
  const messageId = useId();
  const full = list.length >= MaxMemberKeywords;
  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "Enter" || e.key === ",") {
      e.preventDefault();
      onCommit();
    }
  };
  const message = draftError
    ? errorText(t, draftError)
    : full
      ? t("validation.keywords", validationVars["validation.keywords"])
      : null;
  const describedBy = [formDescriptionId, message ? messageId : "", error ? formMessageId : ""]
    .filter(Boolean)
    .join(" ");
  return (
    <>
      <FormLabel>{t("memberSettings.keywords")}</FormLabel>
      {list.length > 0 ? (
        <ul className="flex flex-wrap gap-1.5">
          {list.map((k) => (
            <li
              key={k}
              className="inline-flex max-w-full items-center gap-0.5 rounded-sm border border-tape-strong/70 bg-tape/30 py-0.5 pr-0.5 pl-2 font-mono text-xs dark:bg-tape/10"
            >
              <span className="truncate">{k}</span>
              <button
                type="button"
                className="inline-flex size-5 shrink-0 items-center justify-center rounded-sm text-muted-foreground transition-colors hover:bg-foreground/10 hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/50 focus-visible:outline-none"
                aria-label={t("memberSettings.keywordRemove", { keyword: k })}
                onClick={() => onChange(list.filter((x) => x !== k))}
              >
                <XIcon className="size-3" aria-hidden="true" />
              </button>
            </li>
          ))}
        </ul>
      ) : null}
      <div className="flex gap-2">
        <FormControl>
          <Input
            value={draft}
            onChange={(e) => onDraft(e.target.value)}
            onKeyDown={onKeyDown}
            disabled={full}
            placeholder={t("memberSettings.keywordPlaceholder")}
            aria-invalid={draftError || error ? true : undefined}
            aria-describedby={describedBy}
            autoComplete="off"
            autoCapitalize="none"
            spellCheck={false}
            className="font-mono text-sm"
          />
        </FormControl>
        <Button
          type="button"
          variant="outline"
          size="icon"
          className="shrink-0"
          disabled={full}
          aria-label={t("memberSettings.keywordAdd")}
          onClick={onCommit}
        >
          <PlusIcon aria-hidden="true" />
        </Button>
      </div>
      <FormDescription className="text-xs">{t("memberSettings.keywordsHint")}</FormDescription>
      <p id={messageId} aria-live="polite" className="text-sm text-destructive empty:hidden">
        {message}
      </p>
      <FieldError />
    </>
  );
}
