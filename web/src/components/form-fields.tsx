import { EyeIcon, EyeOffIcon } from "lucide-react";
import { type ComponentProps, type ReactNode, useState } from "react";
import type { Control, FieldPath, FieldValues } from "react-hook-form";
import { Button } from "@/components/ui/button";
import {
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  useFormField,
} from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { useAutoFocus } from "@/hooks/use-auto-focus";
import { errorText } from "@/lib/forms";
import { useT } from "@/lib/i18n";
import { cn } from "@/lib/utils";

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
                className={inputClassName}
              />
            ) : (
              <Input
                {...field}
                value={field.value ?? ""}
                type={type}
                autoComplete={autoComplete}
                autoFocus={focusOnMount}
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
