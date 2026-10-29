import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Secret } from "@/shared/api/types.gen";
import { apiError, json, mockApi } from "@/test/fetch";
import { makeSecret } from "@/test/fixtures";
import { useLiveStore } from "@/test/live-store";
import { renderWithProviders } from "@/test/render";
import { SecretsSection } from "./secrets-section";

/*
 * "Secrets" in the account settings (CA-03, CA-08): the owner's sealed credentials by name, adding
 * one or replacing a value in place, and deleting one after a confirmation. A value goes in only:
 * nothing the API answers with holds it, and it is never rendered back.
 */

const SECRET_VALUE = "sk-e2e-000-not-a-real-key";

beforeEach(() => useLiveStore.setState({ locale: "en" }));
afterEach(() => vi.unstubAllGlobals());

/** A list that follows what a test saves, so a replacement shows the new date. */
function render(routes: Parameters<typeof mockApi>[0] = {}, rows: Secret[] = [makeSecret()]) {
  const secrets = [...rows];
  const api = mockApi({ "GET /secrets": () => json(200, { items: secrets }), ...routes });
  renderWithProviders(<SecretsSection />);
  return { ...api, secrets, user: userEvent.setup() };
}

const form = () => screen.getByRole("dialog", { name: /^(New secret|Replace )/ });

describe("the secrets list", () => {
  it("shows the names and when each was last replaced", async () => {
    render();
    const row = await screen.findByTestId("secret-OPENCODE_API_KEY");
    expect(within(row).getByText("OPENCODE_API_KEY")).toBeInTheDocument();
    expect(within(row).getByText(/updated/)).toBeInTheDocument();
    // There is nothing of the value to show, and nothing rendered that could hold it.
    expect(row.textContent).not.toContain(SECRET_VALUE);
  });

  it("teaches what a secret is for while there are none", async () => {
    render({}, []);
    expect(await screen.findByText("No secrets yet")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Add secret" })).toBeInTheDocument();
  });
});

describe("saving a secret (CA-03)", () => {
  it("writes the name and the value and shows the name, never the value", async () => {
    const { calls, secrets, user } = render({
      "PUT /secrets/GITHUB_TOKEN": () => {
        secrets.push(makeSecret({ name: "GITHUB_TOKEN" }));
        return json(204, undefined);
      },
    });
    await screen.findByTestId("secret-OPENCODE_API_KEY");
    await user.click(screen.getByRole("button", { name: "Add secret" }));
    const dialog = await screen.findByRole("dialog", { name: "New secret" });
    await user.type(within(dialog).getByRole("textbox", { name: "Name" }), "GITHUB_TOKEN");
    await user.type(within(dialog).getByLabelText("Value"), SECRET_VALUE);
    await user.click(within(dialog).getByRole("button", { name: "Save secret" }));

    await waitFor(() => expect(calls.some((c) => c.method === "PUT")).toBe(true));
    expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ value: SECRET_VALUE });
    expect(await screen.findByTestId("secret-GITHUB_TOKEN")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    // The value went in and stays in the server: the page never shows it again.
    expect(screen.queryByDisplayValue(SECRET_VALUE)).toBeNull();
    expect(document.body.textContent).not.toContain(SECRET_VALUE);
  });

  it("replaces a value in place: the name is fixed and the value starts empty", async () => {
    const { calls, user } = render({
      "PUT /secrets/OPENCODE_API_KEY": json(204, undefined),
    });
    await screen.findByTestId("secret-OPENCODE_API_KEY");
    await user.click(screen.getByRole("button", { name: "Replace OPENCODE_API_KEY" }));
    const dialog = await form();
    expect(within(dialog).getByRole("textbox", { name: "Name" })).toBeDisabled();
    expect(within(dialog).getByRole("textbox", { name: "Name" })).toHaveValue("OPENCODE_API_KEY");
    expect(within(dialog).getByLabelText("Value")).toHaveValue("");
    await user.type(within(dialog).getByLabelText("Value"), "sk-new");
    await user.click(within(dialog).getByRole("button", { name: "Replace" }));
    await waitFor(() => expect(calls.some((c) => c.method === "PUT")).toBe(true));
    expect(calls.find((c) => c.method === "PUT")?.path).toBe("/secrets/OPENCODE_API_KEY");
    expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ value: "sk-new" });
  });

  it("asks for a name the server accepts before sending anything", async () => {
    const { calls, user } = render();
    await screen.findByTestId("secret-OPENCODE_API_KEY");
    await user.click(screen.getByRole("button", { name: "Add secret" }));
    const dialog = await screen.findByRole("dialog", { name: "New secret" });
    await user.type(within(dialog).getByRole("textbox", { name: "Name" }), "1BAD");
    await user.type(within(dialog).getByLabelText("Value"), "x");
    await user.click(within(dialog).getByRole("button", { name: "Save secret" }));
    expect(
      await within(dialog).findByText(
        "1 to 64 characters: start with a letter, then letters, digits, _ or -",
      ),
    ).toBeInTheDocument();
    expect(calls.some((c) => c.method === "PUT")).toBe(false);
  });

  it("shows the server's reason on the field it refused", async () => {
    const { user } = render({
      "PUT /secrets/GITHUB_TOKEN": apiError(422, "invalid", "invalid", {
        name: "must match ^[A-Za-z][A-Za-z0-9_-]{0,63}$",
      }),
    });
    await screen.findByTestId("secret-OPENCODE_API_KEY");
    await user.click(screen.getByRole("button", { name: "Add secret" }));
    const dialog = await screen.findByRole("dialog", { name: "New secret" });
    await user.type(within(dialog).getByRole("textbox", { name: "Name" }), "GITHUB_TOKEN");
    await user.type(within(dialog).getByLabelText("Value"), SECRET_VALUE);
    await user.click(within(dialog).getByRole("button", { name: "Save secret" }));
    expect(await within(dialog).findByText(/must match \^\[A-Za-z\]/)).toBeInTheDocument();
    // The dialog stays open to try again, and the value is still only in the field.
    expect(screen.getByRole("dialog", { name: "New secret" })).toBeInTheDocument();
  });
});

describe("deleting a secret (CA-03)", () => {
  it("asks first, then deletes it by name", async () => {
    const { calls, secrets, user } = render({
      "DELETE /secrets/OPENCODE_API_KEY": () => {
        secrets.splice(0, 1);
        return json(204, undefined);
      },
    });
    await screen.findByTestId("secret-OPENCODE_API_KEY");
    await user.click(screen.getByRole("button", { name: "Delete OPENCODE_API_KEY" }));
    const dialog = await screen.findByRole("alertdialog", {
      name: "Delete OPENCODE_API_KEY?",
    });
    await user.click(within(dialog).getByRole("button", { name: "Delete secret" }));
    await waitFor(() => expect(calls.some((c) => c.method === "DELETE")).toBe(true));
    expect(calls.find((c) => c.method === "DELETE")?.path).toBe("/secrets/OPENCODE_API_KEY");
    await waitFor(() => expect(screen.queryByTestId("secret-OPENCODE_API_KEY")).toBeNull());
  });
});
