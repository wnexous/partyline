import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Schedule, ScheduleList } from "@/shared/api/types.gen";
import { browserTimeZone } from "@/shared/lib/time";
import { apiError, json, mockApi } from "@/test/fetch";
import { makeIntelligence, makeRoutine, makeRoutineRun } from "@/test/fixtures";
import { handleLiveEvent, useLiveStore } from "@/test/live-store";
import { renderWithProviders } from "@/test/render";
import { RoutinesSection } from "./routines-section";

/*
 * The routines of an AI on its profile (HB-13, HB-12). Whoever manages Atlas sees each routine in words
 * and creates, pauses, runs, edits and deletes one; whoever only sees Atlas learns how many it has and
 * not what they say. The routine is in the viewer's own zone, so its rule carries no zone.
 */

const zone = browserTimeZone();
const routine = makeRoutine({ tz: zone });
const managed = (items: Schedule[] = [routine]): ScheduleList => ({
  items,
  count: items.length,
  can_manage: true,
});
const LIST = "GET /agents/a1/schedules";
const RUNS = "GET /agents/a1/schedules/s1/runs";
const minutesAgo = (n: number) => new Date(Date.now() - n * 60_000).toISOString();

beforeEach(() => useLiveStore.setState({ locale: "pt-BR" }));
afterEach(() => vi.unstubAllGlobals());

function renderSection() {
  return renderWithProviders(<RoutinesSection agentId="a1" aiName="Atlas" />);
}

describe("the routines of an AI (HB-13)", () => {
  it("shows each routine in words to whoever manages the AI", async () => {
    mockApi({ [LIST]: json(200, managed()) });
    renderSection();

    const row = await screen.findByTestId("routine-s1");
    expect(within(row).getByRole("heading", { name: "Daily summary" })).toBeInTheDocument();
    expect(within(row).getByTestId("routine-rule")).toHaveTextContent("Todo dia às 09:00");
    expect(within(row).getByText("Summarize yesterday's messages")).toBeInTheDocument();
    expect(within(row).getByTestId("routine-next")).toHaveTextContent(/^Próxima: /);
    expect(within(row).getByTestId("routine-last")).toHaveTextContent(/^Última: .* · entregue$/);
    expect(within(row).getByText("Definida por Ana Souza")).toBeInTheDocument();
    expect(within(row).getByRole("switch", { name: "Rotina Daily summary ativa" })).toBeChecked();
  });

  it("creates a routine from a preset, said in words as it is picked", async () => {
    const created = makeRoutine({ id: "s2", name: "Deploys", every_minutes: 120, cron: "" });
    const { calls } = mockApi({
      [LIST]: json(200, managed([])),
      "POST /agents/a1/schedules": json(201, created),
    });
    renderSection();
    const user = userEvent.setup();

    expect(await screen.findByText(/Nenhuma rotina ainda/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Nova rotina" }));
    const dialog = await screen.findByRole("dialog", { name: "Nova rotina para Atlas" });
    await user.type(within(dialog).getByLabelText("Nome"), "Deploys");
    await user.type(within(dialog).getByLabelText("O que pedir"), "Check the deploys");
    expect(within(dialog).getByTestId("routine-preview")).toHaveTextContent(
      "Roda: Todo dia às 09:00",
    );
    await user.selectOptions(within(dialog).getByLabelText("Repetir"), "hours");
    const every = within(dialog).getByLabelText("Horas entre execuções");
    await user.clear(every);
    await user.type(every, "2");
    expect(within(dialog).getByTestId("routine-preview")).toHaveTextContent("Roda: A cada 2 horas");
    await user.click(within(dialog).getByRole("button", { name: "Criar rotina" }));

    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({
      name: "Deploys",
      prompt: "Check the deploys",
      every_minutes: 120,
      tz: zone,
      catch_up: "once",
    });
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("refuses an interval under the minimum before sending it", async () => {
    const { calls } = mockApi({ [LIST]: json(200, managed([])) });
    renderSection();
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Nova rotina" }));
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("Nome"), "Ping");
    await user.type(within(dialog).getByLabelText("O que pedir"), "ping");
    await user.selectOptions(within(dialog).getByLabelText("Repetir"), "minutes");
    const every = within(dialog).getByLabelText("Minutos entre execuções");
    await user.clear(every);
    await user.type(every, "5");
    await user.click(within(dialog).getByRole("button", { name: "Criar rotina" }));

    expect(
      await within(dialog).findByText("Um número inteiro de minutos, no mínimo 15"),
    ).toBeInTheDocument();
    expect(calls.some((c) => c.method === "POST")).toBe(false);
  });

  it("says the server's too_frequent above the buttons and keeps the dialog open", async () => {
    mockApi({
      [LIST]: json(200, managed([])),
      "POST /agents/a1/schedules": apiError(
        422,
        "too_frequent",
        "the enabled schedules of an AI run at most 48 times in 24 hours",
      ),
    });
    renderSection();
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Nova rotina" }));
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("Nome"), "Often");
    await user.type(within(dialog).getByLabelText("O que pedir"), "check");
    await user.click(within(dialog).getByRole("button", { name: "Criar rotina" }));

    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "Frequente demais: uma rotina roda no máximo a cada 15 minutos, e as rotinas ativas de uma IA, no máximo 48 vezes por dia.",
    );
  });

  it("pauses a routine, runs it now and deletes it after asking", async () => {
    // The server's list follows the writes, as it is read again after each.
    let items = [routine];
    const { calls } = mockApi({
      [LIST]: () => json(200, managed(items)),
      "PATCH /agents/a1/schedules/s1": () => {
        items = [makeRoutine({ tz: zone, enabled: false, paused_by_manager: true })];
        return json(200, items[0]);
      },
      "POST /agents/a1/schedules/s1/run": json(202, {
        id: "r1",
        schedule_id: "s1",
        due_at: "2026-11-10T15:00:00Z",
        status: "pending",
        manual: true,
      }),
      "DELETE /agents/a1/schedules/s1": () => {
        items = [];
        return new Response(null, { status: 204 });
      },
    });
    renderSection();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("switch", { name: "Rotina Daily summary ativa" }));
    await waitFor(() =>
      expect(calls.find((c) => c.method === "PATCH")?.body).toEqual({ enabled: false }),
    );
    expect(await screen.findByTestId("routine-paused")).toHaveTextContent("Pausada");
    expect(screen.getByTestId("routine-next")).toHaveTextContent(
      "Pausada por quem gerencia Atlas: Atlas não pode reativar",
    );

    await user.click(screen.getByRole("button", { name: "Executar Daily summary agora" }));
    await waitFor(() =>
      expect(calls.some((c) => c.method === "POST" && c.path.endsWith("/run"))).toBe(true),
    );

    await user.click(screen.getByRole("button", { name: "Apagar Daily summary" }));
    const confirm = await screen.findByRole("alertdialog", {
      name: "Apagar a rotina “Daily summary”?",
    });
    await user.click(within(confirm).getByRole("button", { name: "Apagar" }));
    await waitFor(() => expect(calls.some((c) => c.method === "DELETE")).toBe(true));
    expect(await screen.findByText(/Nenhuma rotina ainda/)).toBeInTheDocument();
  });

  it("reads the list again and says so when the routine changed meanwhile (409)", async () => {
    let reads = 0;
    mockApi({
      [LIST]: () => {
        reads += 1;
        return json(200, managed());
      },
      "PATCH /agents/a1/schedules/s1": apiError(409, "conflict"),
    });
    renderSection();
    const user = userEvent.setup();
    await user.click(await screen.findByRole("switch", { name: "Rotina Daily summary ativa" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Esta rotina mudou enquanto isso. A lista foi recarregada: confira e tente de novo.",
    );
    await waitFor(() => expect(reads).toBe(2));
  });

  it("edits only what changed", async () => {
    const { calls } = mockApi({
      [LIST]: json(200, managed()),
      "PATCH /agents/a1/schedules/s1": json(200, makeRoutine({ tz: zone, cron: "0 9 * * 1-5" })),
    });
    renderSection();
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Editar Daily summary" }));
    const dialog = await screen.findByRole("dialog", { name: "Editar rotina" });
    expect(within(dialog).getByLabelText("Nome")).toHaveValue("Daily summary");
    await user.selectOptions(within(dialog).getByLabelText("Repetir"), "weekdays");
    await user.click(within(dialog).getByRole("button", { name: "Salvar" }));

    await waitFor(() =>
      expect(calls.find((c) => c.method === "PATCH")?.body).toEqual({ cron: "0 9 * * 1-5" }),
    );
  });

  it("tells whoever only sees the AI how many routines it has, not what they say (HB-12)", async () => {
    mockApi({ [LIST]: json(200, { items: [], count: 2, can_manage: false }) });
    renderSection();

    expect(await screen.findByTestId("routines-hidden")).toHaveTextContent(
      "Atlas tem 2 rotinas. Só quem gerencia Atlas vê o que elas dizem.",
    );
    expect(screen.queryByRole("button", { name: "Nova rotina" })).toBeNull();
    expect(screen.queryByTestId("routine-s1")).toBeNull();
    expect(screen.queryByTestId("routine-history-toggle")).toBeNull();
  });

  it("shows nothing to a viewer with no routine to learn about or who may not read them", async () => {
    mockApi({ [LIST]: json(200, { items: [], count: 0, can_manage: false }) });
    const { container, unmount } = renderSection();
    await waitFor(() => expect(container.querySelector("section")).toBeNull());
    await waitFor(() => expect(container.querySelector("[aria-hidden]")).toBeNull());
    unmount();

    mockApi({ [LIST]: apiError(403, "forbidden") });
    const forbidden = renderSection();
    await waitFor(() => expect(forbidden.container.querySelector("[aria-hidden]")).toBeNull());
    expect(forbidden.container.querySelector("section")).toBeNull();
  });

  it("says a routine the AI set itself is the AI's", async () => {
    mockApi({
      [LIST]: json(
        200,
        managed([
          makeRoutine({
            tz: zone,
            origin: "self",
            set_by: makeIntelligence({ display_name: "Atlas", kind: "ai", role: "ai" }),
          }),
        ]),
      ),
    });
    renderSection();
    expect(await screen.findByText("Definida pela própria IA (Atlas)")).toBeInTheDocument();
  });

  it("reads the routines again when the live socket says they changed", async () => {
    let reads = 0;
    mockApi({
      [LIST]: () => {
        reads += 1;
        return json(200, managed());
      },
    });
    const { queryClient } = renderSection();
    await screen.findByTestId("routine-s1");
    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "agent.schedules",
        data: { agent_id: "a1" },
      }),
    );
    await waitFor(() => expect(reads).toBe(2));
  });
  describe("the history of a routine (HB-11)", () => {
    it("reads the runs only when a manager opens them, and says each in words", async () => {
      const { calls } = mockApi({
        [LIST]: json(200, managed()),
        [RUNS]: json(200, {
          items: [
            makeRoutineRun({
              id: "r3",
              due_at: minutesAgo(2),
              fired_at: undefined,
              acked_at: undefined,
              status: "pending",
              manual: true,
            }),
            makeRoutineRun({
              id: "r2",
              due_at: minutesAgo(30),
              fired_at: minutesAgo(30),
              acked_at: minutesAgo(29),
            }),
            makeRoutineRun({
              id: "r1",
              due_at: minutesAgo(50),
              fired_at: undefined,
              acked_at: undefined,
              status: "missed",
            }),
          ],
        }),
      });
      renderSection();
      const user = userEvent.setup();
      const toggle = await screen.findByRole("button", { name: "Histórico de Daily summary" });
      expect(toggle).toHaveAttribute("aria-expanded", "false");
      expect(calls.some((c) => c.path.endsWith("/runs"))).toBe(false);

      await user.click(toggle);
      expect(toggle).toHaveAttribute("aria-expanded", "true");
      const list = await screen.findByRole("list", { name: "Histórico de Daily summary" });
      const items = within(list).getAllByRole("listitem");
      expect(items).toHaveLength(3);

      const manual = within(list).getByTestId("routine-run-r3");
      expect(within(manual).getByTestId("routine-run-status")).toHaveTextContent(
        "aguardando a sessão principal",
      );
      expect(within(manual).getByTestId("routine-run-manual")).toHaveTextContent(
        "Manual (Executar agora)",
      );
      expect(within(manual).getByTestId("routine-run-due")).toHaveTextContent(
        /^Prevista há 2 min\. · \d+ de \S+ de \d{4}/,
      );
      expect(within(manual).getByTestId("routine-run-ack")).toHaveTextContent(
        "Atlas ainda não recebeu",
      );

      const delivered = within(list).getByTestId("routine-run-r2");
      expect(within(delivered).getByTestId("routine-run-status")).toHaveTextContent("entregue");
      expect(within(delivered).queryByTestId("routine-run-manual")).toBeNull();
      expect(within(delivered).getByTestId("routine-run-ack")).toHaveTextContent(
        "Atlas confirmou há 29 min.",
      );

      const missed = within(list).getByTestId("routine-run-r1");
      expect(within(missed).getByTestId("routine-run-status")).toHaveTextContent(
        "perdida: a IA estava offline",
      );
      expect(within(missed).getByTestId("routine-run-ack")).toHaveTextContent("Não chegou a Atlas");

      await user.click(toggle);
      expect(screen.queryByTestId("routine-history")).toBeNull();
    });

    it("says a run sent and not acknowledged yet", async () => {
      mockApi({
        [LIST]: json(200, managed()),
        [RUNS]: json(200, {
          items: [
            makeRoutineRun({
              due_at: minutesAgo(5),
              fired_at: minutesAgo(5),
              acked_at: undefined,
              status: "pending",
            }),
            makeRoutineRun({
              id: "r0",
              due_at: minutesAgo(65),
              fired_at: minutesAgo(65),
              acked_at: undefined,
              status: "superseded",
            }),
          ],
        }),
      });
      renderSection();
      const user = userEvent.setup();
      await user.click(await screen.findByRole("button", { name: "Histórico de Daily summary" }));
      expect(
        within(await screen.findByTestId("routine-run-r1")).getByTestId("routine-run-ack"),
      ).toHaveTextContent("Enviada a Atlas há 5 min., aguardando confirmação");
      expect(
        within(screen.getByTestId("routine-run-r0")).getByTestId("routine-run-ack"),
      ).toHaveTextContent("Enviada a Atlas há 1 h, sem confirmação");
    });

    it("says when the routine has not run in the kept 30 days", async () => {
      mockApi({ [LIST]: json(200, managed()), [RUNS]: json(200, { items: [] }) });
      renderSection();
      const user = userEvent.setup();
      await user.click(await screen.findByRole("button", { name: "Histórico de Daily summary" }));
      expect(await screen.findByRole("status")).toHaveTextContent(
        "Nenhuma execução nos últimos 30 dias.",
      );
    });

    it("shows a placeholder while the runs load", async () => {
      let answer: (r: Response) => void = () => {};
      mockApi({
        [LIST]: json(200, managed()),
        [RUNS]: () =>
          new Promise<Response>((resolve) => {
            answer = resolve;
          }),
      });
      renderSection();
      const user = userEvent.setup();
      await user.click(await screen.findByRole("button", { name: "Histórico de Daily summary" }));
      const panel = screen.getByTestId("routine-history");
      expect(panel.querySelector("[aria-hidden]")).not.toBeNull();
      expect(within(panel).queryByRole("list")).toBeNull();
      answer(json(200, { items: [makeRoutineRun()] }));
      expect(await within(panel).findByRole("list")).toBeInTheDocument();
    });

    it("says a failed read and tries again", async () => {
      let reads = 0;
      mockApi({
        [LIST]: json(200, managed()),
        [RUNS]: () => {
          reads += 1;
          return reads === 1 ? apiError(500, "internal") : json(200, { items: [makeRoutineRun()] });
        },
      });
      renderSection();
      const user = userEvent.setup();
      await user.click(await screen.findByRole("button", { name: "Histórico de Daily summary" }));
      const alert = await screen.findByRole("alert");
      expect(alert).toHaveTextContent("Não foi possível carregar o histórico.");
      await user.click(within(alert).getByRole("button", { name: "Tentar de novo" }));
      expect(await screen.findByTestId("routine-run-r1")).toBeInTheDocument();
    });

    it("says a refusal without offering to try again", async () => {
      mockApi({ [LIST]: json(200, managed()), [RUNS]: apiError(403, "forbidden") });
      renderSection();
      const user = userEvent.setup();
      await user.click(await screen.findByRole("button", { name: "Histórico de Daily summary" }));
      const alert = await screen.findByRole("alert");
      expect(alert).toHaveTextContent("Só quem gerencia Atlas vê o histórico das rotinas.");
      expect(within(alert).queryByRole("button")).toBeNull();
    });

    it("reads an open history again after Run now", async () => {
      let runs = [makeRoutineRun()];
      mockApi({
        [LIST]: json(200, managed()),
        [RUNS]: () => json(200, { items: runs }),
        "POST /agents/a1/schedules/s1/run": () => {
          const run = makeRoutineRun({
            id: "r2",
            due_at: new Date().toISOString(),
            fired_at: undefined,
            acked_at: undefined,
            status: "pending",
            manual: true,
          });
          runs = [run, ...runs];
          return json(202, run);
        },
      });
      renderSection();
      const user = userEvent.setup();
      await user.click(await screen.findByRole("button", { name: "Histórico de Daily summary" }));
      await screen.findByTestId("routine-run-r1");
      await user.click(screen.getByRole("button", { name: "Executar Daily summary agora" }));
      const added = await screen.findByTestId("routine-run-r2");
      expect(within(added).getByTestId("routine-run-manual")).toBeInTheDocument();
    });
  });
});
