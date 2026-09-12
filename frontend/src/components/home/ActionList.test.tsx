/* @vitest-environment jsdom */

import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { TodayAction } from "@/lib/types";

vi.mock("@/lib/api", () => ({
  openMarketInGame: vi.fn(async () => {}),
  setWaypointInGame: vi.fn(async () => {}),
  openContractInGame: vi.fn(async () => {}),
}));

const { ActionList } = await import("./ActionList");

afterEach(cleanup);

function action(id: string, inBudget: boolean, downside: number): TodayAction {
  return {
    id,
    kind: "buy",
    urgency: "soon",
    type_id: 34,
    type_name: `Item ${id}`,
    location_id: 60003760,
    location_name: "Jita IV-4",
    here: true,
    headline: `Buy order at 1013.40`,
    why: "",
    paste_price: 1013.4,
    quantity: 12000,
    expected_isk_7d: downside * 2,
    downside_isk_7d: downside,
    at_risk_isk: 0,
    grade: "likely",
    reliability: { grade: "likely", score: 60, sample_trades: 0, realized_isk: 0, evidence: "" },
    est_seconds: 45,
    risk_adjusted_isk_per_minute: downside,
    cumulative_seconds: 45,
    in_budget: inBudget,
    deep_link: { tab: "station", type_id: 34 },
    done: false,
    skipped: false,
  };
}

describe("ActionList cut line", () => {
  const actions = [
    action("a", true, 21_000_000),
    action("b", true, 14_000_000),
    action("c", false, 2_000_000),
    action("d", false, 1_100_000),
  ];

  function renderList() {
    render(
      <ActionList
        actions={actions}
        budgetSeconds={1200}
        onDone={vi.fn()}
        onSkip={vi.fn()}
        onDetails={vi.fn()}
      />,
    );
  }

  // The ordering only means something if the boundary is visible. Forty more
  // rows is a wall; "3.1M in 2 more actions" is a decision.
  it("shows what fits the budget and collapses the rest behind a count and a total", () => {
    renderList();

    expect(screen.getByText("Item a")).toBeInTheDocument();
    expect(screen.getByText("Item b")).toBeInTheDocument();
    expect(screen.queryByText("Item c")).not.toBeInTheDocument();
    expect(screen.queryByText("Item d")).not.toBeInTheDocument();

    // The cut line states the budget it used and what is past it, so the
    // decision to keep going is an informed one.
    expect(screen.getByText(/that is your 20 minutes/i)).toBeInTheDocument();
    expect(screen.getByText(/2 more worth/i)).toBeInTheDocument();
  });

  it("reveals the rest on request", async () => {
    renderList();
    await userEvent.click(screen.getByRole("button", { name: /show the rest/i }));

    expect(screen.getByText("Item c")).toBeInTheDocument();
    expect(screen.getByText("Item d")).toBeInTheDocument();
  });

  // The queue ranks on the downside, so that is the figure the row leads with.
  // Leading with the expected figure would undo the whole point of the split.
  it("leads each row with the downside figure, not the expected one", () => {
    renderList();
    // 21M downside vs 42M expected for the top row.
    expect(screen.getByText("21 M")).toBeInTheDocument();
    expect(screen.queryByText("42 M")).not.toBeInTheDocument();
  });

  // Without a cut there is nothing to collapse and no boundary to announce.
  it("says nothing about a cut line when everything fits", () => {
    render(
      <ActionList
        actions={[action("a", true, 5_000_000)]}
        budgetSeconds={1200}
        onDone={vi.fn()}
        onSkip={vi.fn()}
        onDetails={vi.fn()}
      />,
    );
    expect(screen.queryByText(/that is your/i)).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /show the rest/i })).not.toBeInTheDocument();
  });
});
