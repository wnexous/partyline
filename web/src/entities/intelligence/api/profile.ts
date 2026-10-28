import {
  type QueryClient,
  queryOptions,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { putAvatarImage } from "@/shared/api/avatar-upload";
import { api } from "@/shared/api/client";
import { queryKeys } from "@/shared/api/query-keys";
import type { IntelligenceProfile, UpdateProfileRequest } from "@/shared/api/types.gen";
import { refOfProfile } from "../lib/profile";
import { patchIntelligence } from "../model/cache";

export const intelligenceQuery = (id: string) =>
  queryOptions({
    queryKey: queryKeys.intelligence(id),
    queryFn: () => api<IntelligenceProfile>("GET", `/intelligences/${id}`),
  });

export const useIntelligence = (id: string) => useQuery(intelligenceQuery(id));

/** A profile the server answered with: cached, and shown everywhere the intelligence appears. */
function storeProfile(qc: QueryClient, profile: IntelligenceProfile) {
  qc.setQueryData(queryKeys.intelligence(profile.id), profile);
  patchIntelligence(qc, refOfProfile(profile));
}

/** Edits display name, handle and bio (PROF-03, PROF-05); 409 `handle_taken` on a taken handle. */
export function useUpdateIntelligence(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: UpdateProfileRequest) =>
      api<IntelligenceProfile>("PATCH", `/intelligences/${id}`, req),
    onSuccess: (profile) => storeProfile(qc, profile),
  });
}

/** Uploads an avatar: the image file goes as the raw body (PROF-07); 422 `avatar_invalid`. */
export function useUploadAvatar(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (image: Blob) =>
      putAvatarImage<IntelligenceProfile>(`/intelligences/${id}/avatar`, image),
    onSuccess: (profile) => storeProfile(qc, profile),
  });
}

/** Removes the avatar: the initials show again everywhere. */
export function useRemoveAvatar(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api<void>("DELETE", `/intelligences/${id}/avatar`),
    onSuccess: () => {
      const cached = qc.getQueryData<IntelligenceProfile>(queryKeys.intelligence(id));
      if (cached) {
        const { avatar_url: _removed, ...rest } = cached;
        storeProfile(qc, rest);
      }
      qc.invalidateQueries({ queryKey: queryKeys.intelligence(id), exact: true });
    },
  });
}
