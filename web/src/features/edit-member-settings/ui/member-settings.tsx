import { zodResolver } from "@hookform/resolvers/zod";
import { SlidersHorizontalIcon } from "lucide-react";
import { type FormEvent, useId, useState } from "react";
import { useForm } from "react-hook-form";
import { toast } from "sonner";
import { memberChanges, useUpdateMember } from "@/entities/room-member";
import { keywordError, type MemberSettingsForm, memberSettingsSchema } from "@/lib/schemas";
import { isApiError } from "@/shared/api/client";
import {
  type DeliveryMode,
  DeliveryModeAll,
  DeliveryModeMentions,
  DeliveryModeTrusted,
  type RoomMember,
} from "@/shared/api/types.gen";
import { type MessageKey, useI18n } from "@/shared/i18n";
import { isCoarsePointer } from "@/shared/lib/device";
import { applyFieldErrors } from "@/shared/lib/forms";
import { cn } from "@/shared/lib/utils";
import { FieldError, FormAlert } from "@/shared/ui/molecules/form-fields";
import { Button } from "@/shared/ui/primitives/button";
import {
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  Form as FormProvider,
} from "@/shared/ui/primitives/form";
import { Input } from "@/shared/ui/primitives/input";
import { Popover, PopoverContent, PopoverTrigger } from "@/shared/ui/primitives/popover";
import { RadioGroup, RadioGroupItem } from "@/shared/ui/primitives/radio-group";
import { KeywordsField } from "./keywords-field";

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
    const req = memberChanges(member, values);
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
        selected && "bg-tape/40 hover:bg-tape/50",
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
