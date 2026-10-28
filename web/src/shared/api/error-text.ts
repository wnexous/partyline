import type { Translate } from "@/shared/i18n";
import { isApiError } from "./client";

/** The text of a failed form request that has no specific reason: the network, or a generic line. */
export function formFailureText(t: Translate, err: unknown): string {
  return isApiError(err) && err.status === 0 ? t("error.network") : t("error.generic");
}
