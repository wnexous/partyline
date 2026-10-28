import { screen, within } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import { json, mockApi } from "@/test/fetch";
import { makeAI, makeMe } from "@/test/fixtures";
import { useLiveStore } from "@/test/live-store";
import { renderWithRouter } from "@/test/render";
import { IntelligenceRow } from "./intelligence-row";

beforeEach(() => useLiveStore.setState({ locale: "en" }));

describe("IntelligenceRow", () => {
  it("shows avatar, name and @handle, with the meta, sessions and actions it is given", async () => {
    mockApi({ "GET /me": json(200, makeMe()) });
    renderWithRouter(
      <ul>
        <IntelligenceRow
          testId="row-a1"
          intelligence={makeAI()}
          meta={<span>Online</span>}
          sessions={<p>principal · :pi-2</p>}
          actions={<button type="button">More</button>}
        />
      </ul>,
    );
    const row = within(await screen.findByTestId("row-a1"));
    expect(row.getByTestId("intelligence-avatar")).toBeInTheDocument();
    expect(row.getByRole("link", { name: "Atlas" })).toBeInTheDocument();
    expect(row.getByText("@atlas")).toBeInTheDocument();
    expect(row.getByText("Online")).toBeInTheDocument();
    expect(row.getByText("principal · :pi-2")).toBeInTheDocument();
    expect(row.getByRole("button", { name: "More" })).toBeInTheDocument();
  });

  it("renders no actions column and no meta line when it has none", async () => {
    mockApi({ "GET /me": json(200, makeMe()) });
    renderWithRouter(
      <ul>
        <IntelligenceRow testId="row-a1" intelligence={makeAI()} />
      </ul>,
    );
    const row = await screen.findByTestId("row-a1");
    expect(row.querySelectorAll("p")).toHaveLength(0);
    expect(within(row).queryByRole("button")).toBeNull();
  });
});
