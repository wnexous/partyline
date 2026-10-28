import { SearchIcon, XIcon } from "lucide-react";
import { useRef } from "react";
import { Input } from "@/shared/ui/primitives/input";
import { useT } from "@/lib/i18n";

/**
 * The sidebar's search box (LAY-03): filters the conversation list as the user types, by the
 * conversation's name and by who is in it. Escape or the clear button empties it.
 */
export function ConversationSearch({
  value,
  onChange,
}: {
  value: string;
  onChange: (value: string) => void;
}) {
  const t = useT();
  const input = useRef<HTMLInputElement>(null);
  return (
    <div className="relative">
      <SearchIcon
        className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground"
        aria-hidden="true"
      />
      <Input
        ref={input}
        type="search"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Escape" && value) {
            e.preventDefault();
            onChange("");
          }
        }}
        placeholder={t("conversations.search")}
        aria-label={t("conversations.search")}
        autoComplete="off"
        // Our own clear button replaces the browser's, which would be a second one.
        className="h-11 border-sidebar-border bg-background/70 pr-10 pl-8 md:h-9 [&::-webkit-search-cancel-button]:hidden"
      />
      {value ? (
        <button
          type="button"
          aria-label={t("conversations.searchClear")}
          onClick={() => {
            onChange("");
            input.current?.focus();
          }}
          className="absolute top-1/2 right-0.5 grid size-10 -translate-y-1/2 place-items-center rounded-md text-muted-foreground outline-none hover:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/70 md:size-8"
        >
          <XIcon className="size-4" aria-hidden="true" />
        </button>
      ) : null}
    </div>
  );
}
