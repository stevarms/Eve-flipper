/* @vitest-environment jsdom */

import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { TodayAction } from "@/lib/types";

// useEveUiActions reaches straight into lib/api for the three ESI UI calls.
// Mocking the module rather than the hook keeps the hook's own error handling
// in the test, which is the part Run mode depends on.
const openMarketInGame = vi.fn(async () => {});
const setWaypointInGame = vi.fn(async () => {});
vi.mock("@/lib/api", () => ({
  openMarketInGame: (...args: unknown[]) => openMarketInGame(...(args as [])),
  setWaypointInGame: (...args: unknown[]) => setWaypointInGame(...(args as [])),
  openContractInGame: vi.fn(async () => {}),
}));

const { RunPanel } = await import("./RunPanel");

// This project does not run vitest with `globals: true`, so RTL's automatic
// cleanup is never registered and renders would pile up across tests.
afterEach(cleanup);

let clipboard: string[];
let clipboardFails = false;

beforeEach(() => {
  clipboard = [];
  clipboardFails = false;
  openMarketInGame.mockClear();
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: {
      writeText: vi.fn(async (text: string) => {
        if (clipboardFails) throw new Error("denied");
        clipboard.push(text);
      }),
    },
  });
});

function action(overrides: Partial<TodayAction> = {}): TodayAction {
  return {
    id: "reprice:5:60003760:900",
    kind: "reprice",
    urgency: "today",
    type_id: 5,
    type_name: "Zeugma Integrated Analyzer",
    location_id: 60003760,
    location_name: "Jita IV-4",
    here: true,
    headline: "Reprice to 1237000.00",
    why: "outbid, 3rd of 11",
    current_price: 1240000,
    paste_price: 1237000,
    price_step: 1000,
    quantity: 412,
    expected_isk_7d: 95000,
    downside_isk_7d: 70000,
    at_risk_isk: 5000,
    grade: "proven",
    reliability: {
      grade: "proven",
      score: 88,
      sample_trades: 41,
      realized_isk: 12_000_000,
      evidence: "41 real sales of this have made you 12.0M",
    },
    est_seconds: 20,
    risk_adjusted_isk_per_minute: 210000,
    cumulative_seconds: 20,
    in_budget: true,
    deep_link: { tab: "orders", type_id: 5, station_id: 60003760, order_id: 900 },
    done: false,
    skipped: false,
    ...overrides,
  };
}

function renderPanel(overrides: Partial<TodayAction> = {}) {
  const onDone = vi.fn();
  const onSkip = vi.fn();
  const onDetails = vi.fn();
  render(
    <RunPanel
      action={action(overrides)}
      index={2}
      total={11}
      secondsRemaining={600}
      onDone={onDone}
      onSkip={onSkip}
      onDetails={onDetails}
    />,
  );
  return { onDone, onSkip, onDetails };
}

function press(code: string) {
  document.dispatchEvent(new KeyboardEvent("keydown", { code, bubbles: true }));
}

describe("RunPanel", () => {
  // The whole premise of Run mode: by the time you look at the row, the price
  // is already on the clipboard and you have clicked nothing.
  it("puts the price on the clipboard as soon as the action is shown", async () => {
    renderPanel();
    await waitFor(() => expect(clipboard).toContain("1237000"));
    expect(await screen.findByText(/on your clipboard/i)).toBeInTheDocument();
  });

  // EVE's price field rejects anything but plain digits, so what is displayed
  // and what is copied are deliberately different strings.
  it("copies plain digits, not a formatted ISK figure", async () => {
    renderPanel({ paste_price: 1_237_000 });
    await waitFor(() => expect(clipboard.length).toBeGreaterThan(0));
    for (const text of clipboard) {
      expect(text).toMatch(/^[0-9]+(\.[0-9]+)?$/);
    }
  });

  // A refused clipboard must not be reported as a successful one. Claiming a
  // price is ready to paste when it is not is worse than saying nothing.
  it("does not claim the clipboard holds the price when the write was refused", async () => {
    clipboardFails = true;
    renderPanel();
    await waitFor(() => expect(navigator.clipboard.writeText).toHaveBeenCalled());
    expect(screen.queryByText(/on your clipboard/i)).not.toBeInTheDocument();
    expect(screen.getByText(/click to copy/i)).toBeInTheDocument();
  });

  it("Enter opens the market window in EVE and re-copies the price", async () => {
    renderPanel();
    await waitFor(() => expect(clipboard.length).toBeGreaterThan(0));
    const before = clipboard.length;

    press("Enter");

    await waitFor(() => expect(openMarketInGame).toHaveBeenCalledWith(5));
    await waitFor(() => expect(clipboard.length).toBeGreaterThan(before));
    expect(clipboard[clipboard.length - 1]).toBe("1237000");
  });

  it("Q swaps the clipboard to the quantity", async () => {
    renderPanel();
    await waitFor(() => expect(clipboard.length).toBeGreaterThan(0));

    press("KeyQ");

    await waitFor(() => expect(clipboard[clipboard.length - 1]).toBe("412"));
  });

  it("Space marks the action done and S skips it", async () => {
    const { onDone, onSkip } = renderPanel();

    press("Space");
    expect(onDone).toHaveBeenCalledTimes(1);

    press("KeyS");
    expect(onSkip).toHaveBeenCalledTimes(1);
  });

  // The keys drive the queue; they must not fire while the user is typing
  // into a search box that happens to be on screen.
  it("ignores its keys while an input has focus", () => {
    const { onDone } = renderPanel();
    const input = document.createElement("input");
    document.body.appendChild(input);
    input.focus();

    input.dispatchEvent(new KeyboardEvent("keydown", { code: "Space", bubbles: true }));

    expect(onDone).not.toHaveBeenCalled();
    input.remove();
  });

  // Reward and risk are both on the face of the row. Showing only the
  // expected figure is exactly the over-promising the downside exists to stop.
  it("shows the realistic case and the capital at risk alongside the expected figure", () => {
    renderPanel();
    expect(screen.getByText(/expected/i)).toBeInTheDocument();
    expect(screen.getByText(/realistic case/i)).toBeInTheDocument();
    expect(screen.getByText(/at risk/i)).toBeInTheDocument();
  });

  it("shows the evidence behind the grade without a hover", () => {
    renderPanel();
    expect(screen.getByText(/41 real sales of this/i)).toBeInTheDocument();
  });

  // An action somewhere else offers the route rather than leaving the user to
  // work out where "Botane Sotiyo" is.
  it("offers a destination when the action is not where you are docked", () => {
    renderPanel({ here: false });
    expect(screen.getByRole("button", { name: /set destination/i })).toBeInTheDocument();
  });

  it("does not offer a destination when you are already there", () => {
    renderPanel({ here: true });
    expect(screen.queryByRole("button", { name: /set destination/i })).not.toBeInTheDocument();
  });

  // Collecting finished jobs has no item and no market window; the panel must
  // still render and still advance.
  it("renders an action with no item to open", () => {
    const { onDone } = renderPanel({
      kind: "deliver",
      type_id: undefined,
      type_name: undefined,
      paste_price: undefined,
      quantity: undefined,
      current_price: undefined,
      headline: "Deliver 8 finished jobs",
    });
    expect(screen.getByText("Deliver 8 finished jobs")).toBeInTheDocument();

    press("Space");
    expect(onDone).toHaveBeenCalledTimes(1);
  });
});
