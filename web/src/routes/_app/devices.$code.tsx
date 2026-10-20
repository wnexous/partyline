import { createFileRoute, redirect } from "@tanstack/react-router";

/*
 * Old approval links (/devices/<code>) carried the code. The code must now be typed from the
 * terminal (WIZ-02), so these links open the empty code form and never pre-fill it.
 */
export const Route = createFileRoute("/_app/devices/$code")({
  beforeLoad: () => {
    throw redirect({ to: "/devices", replace: true });
  },
});
