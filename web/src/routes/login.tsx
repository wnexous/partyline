import { createFileRoute } from "@tanstack/react-router";

export interface LoginSearch {
  /** In-app location to return to after login (UI-02). */
  redirect?: string;
}

export const Route = createFileRoute("/login")({
  validateSearch: (search: Record<string, unknown>): LoginSearch => ({
    redirect: typeof search.redirect === "string" ? search.redirect : undefined,
  }),
  component: LoginPage,
});

function LoginPage() {
  return (
    <main className="grid min-h-dvh place-items-center">
      <h1 className="text-2xl font-semibold">partyline</h1>
    </main>
  );
}
