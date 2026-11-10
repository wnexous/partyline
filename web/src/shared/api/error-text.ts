import type { MessageKey, Translate } from "@/shared/i18n";
import { formatBytes } from "@/shared/lib/bytes";
import { type ApiError, isApiError } from "./client";
import {
  ErrorCodeAvatarInvalid,
  ErrorCodeContactNotFound,
  ErrorCodeCounterpartDisabled,
  ErrorCodeFileTooLarge,
  ErrorCodeInsufficientStorage,
  ErrorCodeNoRouteToPerson,
  ErrorCodeTooFrequent,
  ErrorCodeTooMany,
  type IntelligenceRef,
  MaxScheduleRunsPerDay,
  MaxSchedulesPerAgent,
  MinScheduleEveryMinutes,
} from "./types.gen";
import { isUploadError, knownMaxFileSize } from "./uploads";

/*
 * What a failed request says to the person, per operation (H error text, audit 1.H). A rule is
 * tried in order and the first one that matches decides; a request that matches none says the
 * generic error. `"server"` shows the server's own message (validation), when it has one.
 */

/**
 * What a rule matches: an HTTP status (0 is no network), the server's error code, or a status of
 * an upload session (LF-01: the file's own upload failed, not the post).
 */
type ErrorMatch = { status: number } | { code: string } | { uploadStatus: number | "5xx" };

/** Copy that needs the facts of the error (a file's name, a size in the viewer's locale). */
type ErrorCopy = (t: Translate, err: ApiError, locale: string) => string;

type ErrorRules = ReadonlyArray<readonly [ErrorMatch, MessageKey | "server" | ErrorCopy]>;

/** The file an upload was for, when the error says it. */
const fileNameOf = (err: ApiError) => (isUploadError(err) ? err.fileName : "");

/** A file over the server's largest one, with the limit the server gave (5 GB by default). */
const fileTooLarge: ErrorCopy = (t, err, locale) => {
  const max = Number(err.details.max_size) || knownMaxFileSize();
  const name = fileNameOf(err);
  if (!max) return t("composer.tooLarge");
  const size = formatBytes(max, locale);
  return name
    ? t("composer.fileTooLarge", { name, max: size })
    : t("composer.filesTooLarge", { max: size });
};

const withName =
  (named: MessageKey, unnamed: MessageKey): ErrorCopy =>
  (t, err) => {
    const name = fileNameOf(err);
    return name ? t(named, { name }) : t(unnamed);
  };

/** Per operation, the rules that decide the copy of a failed request. */
export const errorTextRules = {
  /** Sending a message (composer). */
  post: [
    // A file's upload (LF-01): it gave up on the network or the server, or the server refused it.
    [{ code: ErrorCodeFileTooLarge }, fileTooLarge],
    [
      { code: ErrorCodeInsufficientStorage },
      withName("composer.noStorage", "composer.noStorageAny"),
    ],
    [{ uploadStatus: 0 }, withName("composer.uploadStopped", "error.network")],
    [{ uploadStatus: "5xx" }, withName("composer.uploadStopped", "error.generic")],
    [{ uploadStatus: 429 }, "composer.uploadsWaiting"],
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
  /**
   * A write of an AI's routine (HB-13): a rule over the limits (HB-03) and a 21st routine say the
   * limit in words; a routine written meanwhile (409) or gone (404) is read again by the caller.
   */
  routine: [
    [{ status: 0 }, "error.network"],
    [
      { code: ErrorCodeTooFrequent },
      (t) =>
        t("routines.tooFrequent", { min: MinScheduleEveryMinutes, max: MaxScheduleRunsPerDay }),
    ],
    [{ code: ErrorCodeTooMany }, (t) => t("routines.tooMany", { max: MaxSchedulesPerAgent })],
    [{ status: 409 }, "routines.changed"],
    [{ status: 404 }, "routines.gone"],
    [{ status: 403 }, "routines.forbidden"],
    [{ status: 429 }, "routines.rateLimited"],
    [{ status: 422 }, "server"],
  ],
  /** A session's notifications (SN-08): a setting is 422 and names the field. */
  notifications: [
    [{ status: 0 }, "error.network"],
    [{ status: 403 }, "notifications.forbidden"],
    [{ status: 404 }, "notifications.notFound"],
    [{ status: 422 }, "server"],
  ],
} as const satisfies Record<string, ErrorRules>;

export type ErrorOperation = keyof typeof errorTextRules;

function matches(match: ErrorMatch, err: ApiError): boolean {
  if ("status" in match) return err.status === match.status;
  if ("code" in match) return err.code === match.code;
  if (!isUploadError(err)) return false;
  if (match.uploadStatus === "5xx") return err.status >= 500;
  return err.status === match.uploadStatus;
}

/** The words for a failed request of one operation; `locale` formats the numbers some of them carry. */
export function errorText(
  t: Translate,
  err: unknown,
  operation: ErrorOperation,
  locale = "en",
): string {
  if (!isApiError(err)) return t("error.generic");
  for (const [match, copy] of errorTextRules[operation] as ErrorRules) {
    if (!matches(match, err)) continue;
    if (copy === "server") return err.message || t("error.generic");
    if (typeof copy === "function") return copy(t, err, locale);
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

/** The text of a failed form request that has no specific reason: the network, or a generic line. */
export function formFailureText(t: Translate, err: unknown): string {
  return isApiError(err) && err.status === 0 ? t("error.network") : t("error.generic");
}

/** Why a photo was refused, in words the user can act on; a lost connection ends like any other failed upload. */
export function photoErrorText(t: Translate, err: unknown): string {
  if (isApiError(err)) {
    if (err.code === ErrorCodeAvatarInvalid) return t("profile.photoInvalid");
    if (err.status === 413) return t("profile.photoTooLarge");
  }
  return t("profile.photoFailed");
}
