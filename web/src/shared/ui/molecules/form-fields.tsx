import { EyeIcon, EyeOffIcon } from "lucide-react";
import { type ComponentProps, type ReactNode, useId, useState } from "react";
import type { Control, FieldPath, FieldValues } from "react-hook-form";
import { useT } from "@/shared/i18n";
import { useAutoFocus } from "@/shared/lib/device";
import { errorText } from "@/shared/lib/forms";
import { cn } from "@/shared/lib/utils";
import { NativeSelect } from "@/shared/ui/atoms/native-select";
import { Button } from "@/shared/ui/primitives/button";
import {
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  useFormField,
} from "@/shared/ui/primitives/form";
import { Input } from "@/shared/ui/primitives/input";
import { Label } from "@/shared/ui/primitives/label";
import { Textarea } from "@/shared/ui/primitives/textarea";

/** FormMessage that translates schema error keys. */
export function FieldError() {
  const t = useT();
  const { error, formMessageId } = useFormField();
  const text = errorText(t, error?.message);
  if (!text) return null;
  return (
    <p id={formMessageId} data-slot="form-message" className="text-sm text-destructive">
      {text}
    </p>
  );
}

function PasswordInput(props: ComponentProps<typeof Input>) {
  const t = useT();
  const [shown, setShown] = useState(false);
  return (
    <div className="relative">
      <Input
        {...props}
        type={shown ? "text" : "password"}
        className={cn("pr-10", props.className)}
      />
      <Button
        type="button"
        variant="ghost"
        size="icon"
        className="absolute top-1/2 right-0.5 size-8 -translate-y-1/2 text-muted-foreground hover:text-foreground"
        aria-label={shown ? t("auth.hidePassword") : t("auth.showPassword")}
        aria-pressed={shown}
        onClick={() => setShown((v) => !v)}
      >
        {shown ? <EyeOffIcon aria-hidden="true" /> : <EyeIcon aria-hidden="true" />}
      </Button>
    </div>
  );
}

export function TextField<T extends FieldValues>({
  control,
  name,
  label,
  description,
  optional,
  type = "text",
  autoComplete,
  autoFocus,
  disabled = false,
  className,
  inputClassName,
}: {
  control: Control<T>;
  name: FieldPath<T>;
  label: ReactNode;
  description?: ReactNode;
  optional?: boolean;
  type?: "text" | "password" | "url";
  autoComplete?: string;
  autoFocus?: boolean;
  /** The value is fixed (a name a replace keeps): shown, not editable. */
  disabled?: boolean;
  className?: string;
  inputClassName?: string;
}) {
  const t = useT();
  // `autoFocus` asks for focus on desktop only: on phones it would open the keyboard (UI-24).
  const focusOnMount = useAutoFocus() && autoFocus;
  return (
    <FormField
      control={control}
      name={name}
      render={({ field }) => (
        <FormItem className={className}>
          <FormLabel>
            {label}
            {optional ? (
              <span className="font-normal text-muted-foreground">({t("auth.optional")})</span>
            ) : null}
          </FormLabel>
          <FormControl>
            {type === "password" ? (
              <PasswordInput
                {...field}
                value={field.value ?? ""}
                autoComplete={autoComplete}
                autoFocus={focusOnMount}
                disabled={disabled}
                className={inputClassName}
              />
            ) : (
              <Input
                {...field}
                value={field.value ?? ""}
                type={type}
                autoComplete={autoComplete}
                autoFocus={focusOnMount}
                disabled={disabled}
                autoCapitalize="none"
                autoCorrect="off"
                spellCheck={false}
                className={inputClassName}
              />
            )}
          </FormControl>
          {description ? (
            <FormDescription className="text-xs">{description}</FormDescription>
          ) : null}
          <FieldError />
        </FormItem>
      )}
    />
  );
}

/**
 * A multi-line text field. `max` shows a running count in code points (what the server counts)
 * beside the hint; going over it is the schema's error, not this component's.
 */
export function TextAreaField<T extends FieldValues>({
  control,
  name,
  label,
  description,
  max,
  rows = 3,
  className,
}: {
  control: Control<T>;
  name: FieldPath<T>;
  label: ReactNode;
  description?: ReactNode;
  max?: number;
  rows?: number;
  className?: string;
}) {
  return (
    <FormField
      control={control}
      name={name}
      render={({ field }) => (
        <FormItem className={className}>
          <FormLabel>{label}</FormLabel>
          <FormControl>
            <Textarea {...field} value={field.value ?? ""} rows={rows} autoComplete="off" />
          </FormControl>
          {description || max ? (
            <div className="flex items-start justify-between gap-3">
              {description ? (
                <FormDescription className="text-xs">{description}</FormDescription>
              ) : null}
              {max ? (
                <span className="ml-auto shrink-0 font-mono text-xs text-muted-foreground tabular">
                  {[...String(field.value ?? "")].length}/{max}
                </span>
              ) : null}
            </div>
          ) : null}
          <FieldError />
        </FormItem>
      )}
    />
  );
}

/** Form-level error (wrong credentials, rate limit, network). */
export function FormAlert({ children }: { children: ReactNode }) {
  if (!children) return null;
  return (
    <div
      role="alert"
      className="rounded-md border border-destructive/30 bg-destructive/8 px-3 py-2.5 text-sm text-destructive"
    >
      {children}
    </div>
  );
}

/** A native select in a form: the platform's own picker opens on a phone (UI-24). */
export function SelectField<T extends FieldValues>({
  control,
  name,
  label,
  description,
  children,
  className,
}: {
  control: Control<T>;
  name: FieldPath<T>;
  label: ReactNode;
  description?: ReactNode;
  /** The `<option>`s. */
  children: ReactNode;
  className?: string;
}) {
  return (
    <FormField
      control={control}
      name={name}
      render={({ field }) => (
        <FormItem className={className}>
          <FormLabel>{label}</FormLabel>
          <FormControl>
            <NativeSelect {...field} value={field.value ?? ""}>
              {children}
            </NativeSelect>
          </FormControl>
          {description ? (
            <FormDescription className="text-xs">{description}</FormDescription>
          ) : null}
          <FieldError />
        </FormItem>
      )}
    />
  );
}

/**
 * A labelled select outside react-hook-form: the same markup as `SelectField` for a value the
 * caller holds itself (a setting saved as it changes, a picker inside a dialog). `hideLabel` keeps
 * the label for screen readers when the row says what the select is.
 */
export function SelectControl({
  label,
  hideLabel = false,
  description,
  error,
  className,
  selectClassName,
  children,
  id,
  ...select
}: {
  label: ReactNode;
  hideLabel?: boolean;
  description?: ReactNode;
  error?: string | null;
  className?: string;
  selectClassName?: string;
  /** The `<option>`s. */
  children: ReactNode;
  id?: string;
} & Omit<ComponentProps<"select">, "id" | "className" | "children">) {
  const autoId = useId();
  const selectId = id ?? autoId;
  const descriptionId = `${selectId}-description`;
  const errorId = `${selectId}-error`;
  const describedBy =
    [description ? descriptionId : null, error ? errorId : null].filter(Boolean).join(" ") ||
    undefined;
  return (
    <div data-slot="form-item" className={cn("grid gap-2", className)}>
      <Label
        data-slot="form-label"
        data-error={!!error}
        className={cn(hideLabel && "sr-only", "data-[error=true]:text-destructive")}
        htmlFor={selectId}
      >
        {label}
      </Label>
      <NativeSelect
        id={selectId}
        aria-invalid={error ? true : undefined}
        aria-describedby={describedBy}
        className={selectClassName}
        {...select}
      >
        {children}
      </NativeSelect>
      {description ? (
        <p
          data-slot="form-description"
          id={descriptionId}
          className="text-xs text-muted-foreground"
        >
          {description}
        </p>
      ) : null}
      {error ? (
        <p data-slot="form-message" id={errorId} className="text-sm text-destructive">
          {error}
        </p>
      ) : null}
    </div>
  );
}
