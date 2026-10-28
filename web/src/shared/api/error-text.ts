import type { MessageKey, Translate } from "@/shared/i18n";
import { isApiError } from "./client";
import {
  ErrorCodeContactNotFound,
  ErrorCodeCounterpartDisabled,
  ErrorCodeNoRouteToPerson,
  type IntelligenceRef,
} from "./types.gen";

/*
 * What a failed request says to the person, per operation (H error text, audit 1.H). A rule is
 * tried in order and the first one that matches decides; a request that matches none says the
 * generic error. `"server"` shows the server's own message (validation), when it has one.
 */

/** What a rule matches: an HTTP status (0 is no network) or the server's error code. */
type ErrorMatch = { status: number } | { code: string };

type ErrorRules = ReadonlyArray<readonly [ErrorMatch, MessageKey | "server"]>;

/** Per operation, the rules that decide the copy of a failed request. */
export const errorTextRules = {
  /** Sending a message (composer). */
  post: [
    [{ status: 0 }, "error.network"],
    [{ status: 403 }, "composer.forbidden"],
    [{ status: 413 }, "composer.tooLarge"],
    [{ status: 429 }, "composer.rateLimited"],
    [{ status: 422 }, "server"],
  ],
  /** Saving an edit (EDT-02..EDT-05): the message is gone, not the viewer's, the DM's other side is disabled, or the text is refused. */
  edit: [
    [{ status: 0 }, "error.network"],
    [{ status: 409 }, "composer.editGone"],
    [{ code: ErrorCodeCounterpartDisabled }, "composer.editCounterpartDisabled"],
    [{ status: 403 }, "composer.editForbidden"],
    [{ status: 429 }, "composer.rateLimited"],
    [{ status: 422 }, "server"],
  ],
} as const satisfies Record<string, ErrorRules>;

export type ErrorOperation = keyof typeof errorTextRules;

/** The words for a failed request of one operation. */
export function errorText(t: Translate, err: unknown, operation: ErrorOperation): string {
  if (!isApiError(err)) return t("error.generic");
  for (const [match, copy] of errorTextRules[operation]) {
    const hit = "status" in match ? err.status === match.status : err.code === match.code;
    if (!hit) continue;
    if (copy === "server") return err.message || t("error.generic");
    return t(copy);
  }
  return t("error.generic");
}

/** Why a DM with `target` could not be opened, in words the user can act on. */
export function startConversationError(
  t: Translate,
  err: unknown,
  target: Pick<IntelligenceRef, "display_name">,
): string {
  const name = target.display_name;
  if (isApiError(err)) {
    if (err.code === ErrorCodeNoRouteToPerson) return t("dm.noRoute", { name });
    if (err.code === ErrorCodeContactNotFound) return t("dm.notFound", { name });
    if (err.status === 0) return t("error.network");
  }
  return t("dm.failed");
}
