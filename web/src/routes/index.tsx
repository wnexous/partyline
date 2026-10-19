import { createFileRoute } from "@tanstack/react-router";

export const Route = createFileRoute("/")({
  component: Home,
});

function Home() {
  return (
    <main className="grid min-h-dvh place-items-center bg-background p-6">
      <h1 className="text-2xl font-semibold tracking-tight">partyline</h1>
    </main>
  );
}
