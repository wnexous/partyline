import { api } from "@/shared/api/client";

/** How long an avatar upload may take before it is abandoned, so a stalled connection cannot
 * leave the photo "sending" forever. */
export const AvatarUploadTimeoutMs = 30_000;

/** Sends an avatar image as the raw body of a PUT, and gives up on a connection that stalls. */
export async function putAvatarImage<T>(path: string, image: Blob): Promise<T> {
  const abort = new AbortController();
  const timer = setTimeout(() => abort.abort(), AvatarUploadTimeoutMs);
  try {
    return await api<T>("PUT", path, image, { signal: abort.signal });
  } finally {
    clearTimeout(timer);
  }
}
