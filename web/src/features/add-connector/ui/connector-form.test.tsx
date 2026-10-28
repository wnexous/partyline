import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ConnectorFormValues } from "@/entities/connector";
import { SecretMask } from "@/shared/api/types.gen";
import { makeConnectorKind } from "@/test/fixtures";
import { useLiveStore } from "@/test/live-store";
import { renderWithProviders } from "@/test/render";
import { ConnectorForm } from "./connector-form";

beforeEach(() => useLiveStore.setState({ locale: "en" }));

function renderForm(props: Partial<Parameters<typeof ConnectorForm>[0]> = {}) {
  const onSubmit = vi.fn<(values: ConnectorFormValues) => void>();
  renderWithProviders(
    <ConnectorForm
      kind={makeConnectorKind()}
      submitLabel="Add connector"
      pending={false}
      onSubmit={onSubmit}
      {...props}
    />,
  );
  return { onSubmit, user: userEvent.setup() };
}

describe("schema form (UI-16)", () => {
  it("maps every field type to its input", () => {
    renderForm();
    expect(screen.getByLabelText("Bot token")).toHaveAttribute("type", "password");
    expect(screen.getByLabelText(/^API URL/)).toHaveAttribute("type", "url");
    expect(screen.getByLabelText(/Greeting/)).toHaveAttribute("type", "text");
    expect(screen.getByRole("switch", { name: "Groups only" })).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: /Allowed chats/ })).toBeInTheDocument();
    expect(screen.getByText("Sent when the bot joins a group.")).toBeInTheDocument();
  });

  it("starts from the schema defaults", () => {
    renderForm();
    expect(screen.getByLabelText(/Greeting/)).toHaveValue("hello");
    expect(screen.getByRole("switch", { name: "Groups only" })).toHaveAttribute(
      "aria-checked",
      "false",
    );
  });

  it("shows required errors without submitting", async () => {
    const { onSubmit, user } = renderForm();
    await user.click(screen.getByRole("button", { name: "Add connector" }));
    expect(await screen.findAllByText("Required")).toHaveLength(2); // name and bot token
    expect(screen.getByLabelText("Bot token")).toHaveAttribute("aria-invalid", "true");
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("rejects a malformed URL", async () => {
    const { onSubmit, user } = renderForm();
    await user.type(screen.getByLabelText("Name"), "ops-bot");
    await user.type(screen.getByLabelText("Bot token"), "123:abc");
    await user.type(screen.getByLabelText(/^API URL/), "not a url");
    await user.click(screen.getByRole("button", { name: "Add connector" }));
    expect(await screen.findByText(/Enter a full URL/)).toBeInTheDocument();
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("submits typed values: text trimmed, bool, list of tags", async () => {
    const { onSubmit, user } = renderForm();
    await user.type(screen.getByLabelText("Name"), "  ops-bot ");
    await user.type(screen.getByLabelText("Bot token"), "123:abc");
    await user.type(screen.getByLabelText(/^API URL/), "https://api.example.test");
    await user.click(screen.getByRole("switch", { name: "Groups only" }));
    const tags = screen.getByRole("textbox", { name: /Allowed chats/ });
    await user.type(tags, "-1001{Enter}-1002,");
    expect(screen.getByRole("button", { name: "Remove -1001" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Remove -1001" }));
    await user.type(tags, "-1003{Enter}");
    await user.click(screen.getByRole("button", { name: "Add connector" }));
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    expect(onSubmit.mock.calls[0]?.[0]).toEqual({
      name: "ops-bot",
      config: {
        bot_token: "123:abc",
        api_url: "https://api.example.test",
        greeting: "hello",
        groups_only: true,
        allowed_chats: ["-1002", "-1003"],
      },
    });
  });

  it("keeps a stored secret as the mask when editing", async () => {
    const { onSubmit, user } = renderForm({
      submitLabel: "Save",
      initial: {
        name: "ops-bot",
        config: { bot_token: SecretMask, greeting: "hi", groups_only: true, allowed_chats: ["x"] },
      },
    });
    expect(screen.getByLabelText("Bot token")).toHaveValue(SecretMask);
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    expect(onSubmit.mock.calls[0]?.[0].config).toMatchObject({
      bot_token: SecretMask,
      greeting: "hi",
      groups_only: true,
      allowed_chats: ["x"],
    });
  });

  it("lets a generated field stay empty and shows its help", async () => {
    const hook = makeConnectorKind({
      name: "webhook",
      label: "Webhook",
      fields: [
        {
          name: "secret",
          label: "Signing secret",
          type: "secret",
          required: true,
          secret: true,
          generated: true,
          help: "Leave empty to generate one; it is shown once",
        },
        { name: "token", label: "Token", type: "secret", required: true, secret: true },
      ],
    });
    const { onSubmit, user } = renderForm({ kind: hook });
    expect(screen.getByText("Leave empty to generate one; it is shown once")).toBeInTheDocument();
    await user.type(screen.getByLabelText("Name"), "ci-hook");
    await user.click(screen.getByRole("button", { name: "Add connector" }));
    expect(await screen.findAllByText("Required")).toHaveLength(1); // the token, not the secret
    expect(screen.getByLabelText(/Signing secret/)).not.toHaveAttribute("aria-invalid", "true");
    expect(onSubmit).not.toHaveBeenCalled();

    await user.type(screen.getByLabelText("Token"), "t0k3n");
    await user.click(screen.getByRole("button", { name: "Add connector" }));
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    expect(onSubmit.mock.calls[0]?.[0].config).toEqual({ secret: "", token: "t0k3n" });
  });

  it("keeps a generated secret as the mask when editing", async () => {
    const hook = makeConnectorKind({
      name: "webhook",
      fields: [
        {
          name: "secret",
          label: "Signing secret",
          type: "secret",
          required: true,
          secret: true,
          generated: true,
        },
      ],
    });
    const { onSubmit, user } = renderForm({
      kind: hook,
      submitLabel: "Save",
      initial: { name: "ci-hook", config: { secret: SecretMask } },
    });
    expect(screen.getByLabelText(/Signing secret/)).toHaveValue(SecretMask);
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    expect(onSubmit.mock.calls[0]?.[0].config).toEqual({ secret: SecretMask });
  });

  it("shows 422 reasons from the server under the matching fields", async () => {
    const { user } = renderForm({
      onSubmit: (_values, form) =>
        form.applyServerErrors({ "config.bot_token": "rejected by Telegram", name: "taken" }),
    });
    await user.type(screen.getByLabelText("Name"), "ops-bot");
    await user.type(screen.getByLabelText("Bot token"), "123:abc");
    await user.click(screen.getByRole("button", { name: "Add connector" }));
    expect(await screen.findByText("rejected by Telegram")).toBeInTheDocument();
    expect(screen.getByText("taken")).toBeInTheDocument();
  });
});
