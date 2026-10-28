import { useMutation } from "@tanstack/react-query";
import { api } from "@/shared/api/client";
import type { DeviceRequest } from "@/shared/api/types.gen";

export type DeviceAction = "lookup" | "approve" | "deny";

/** Looks up, approves or denies the request with a typed code; answers the request. */
export function useDeviceAction() {
  return useMutation({
    mutationFn: ({ action, code }: { action: DeviceAction; code: string }) =>
      api<DeviceRequest>("POST", `/devices/${action}`, { code }),
  });
}
