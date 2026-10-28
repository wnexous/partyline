import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ChoiceGroup } from "./choice-group";

const options = [
  { value: "system", label: "System", icon: <svg data-testid="icon-system" /> },
  { value: "light", label: "Light" },
  { value: "dark", label: "Dark", lang: "en" },
];

describe("ChoiceGroup", () => {
  it("is a named radio group whose cards are labelled by their option", () => {
    render(
      <ChoiceGroup
        legend="Theme"
        idBase="theme"
        value="light"
        onValueChange={() => {}}
        options={options}
      />,
    );
    const group = screen.getByRole("group", { name: "Theme" });
    const radios = within(group).getAllByRole("radio");
    expect(radios).toHaveLength(3);
    expect(screen.getByRole("radio", { name: "Light" })).toBeChecked();
    expect(screen.getByRole("radio", { name: "System" })).not.toBeChecked();
    expect(screen.getByTestId("icon-system")).toBeInTheDocument();
  });

  it("gives each card the id idBase-value and the option's language", () => {
    const { container } = render(
      <ChoiceGroup
        legend="Theme"
        idBase="theme"
        value="light"
        onValueChange={() => {}}
        options={options}
      />,
    );
    expect(container.querySelector("#theme-dark")).toBeInTheDocument();
    expect(screen.getByText("Dark").closest("label")).toHaveAttribute("lang", "en");
  });

  it("reports the picked value", async () => {
    const onValueChange = vi.fn();
    render(
      <ChoiceGroup
        legend="Theme"
        idBase="theme"
        value="light"
        onValueChange={onValueChange}
        options={options}
      />,
    );
    await userEvent.click(screen.getByText("Dark"));
    expect(onValueChange).toHaveBeenCalledWith("dark");
  });

  it("is disabled as a whole, and shows its children after the cards", () => {
    render(
      <ChoiceGroup
        legend="Theme"
        idBase="theme"
        value="light"
        onValueChange={() => {}}
        options={options}
        disabled
      >
        <p role="alert">Could not save.</p>
      </ChoiceGroup>,
    );
    expect(screen.getByRole("radio", { name: "Dark" })).toBeDisabled();
    expect(screen.getByRole("alert")).toHaveTextContent("Could not save.");
  });
});
