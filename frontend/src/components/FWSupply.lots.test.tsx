/* @vitest-environment jsdom */

import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import { formatGridPrice, priceStep } from "@/lib/pricing";
import { LotsPanel } from "./FWSupply";
import type { FWLot } from "@/lib/api";

afterEach(cleanup);

/**
 * The lots tab is where a recorded selection gets worked: bought in Jita,
 * shipped, listed. These pin the two pastes it shares with the gaps tab and
 * the quantity edit, which has to save once per edit rather than per keystroke.
 */

function lot(over: Partial<FWLot>): FWLot {
  return {
    lot_id: 1,
    type_id: 1,
    type_name: "Thing",
    state: "planned",
    qty: 10,
    qty_remaining: 10,
    unit_cost_isk: 100,
    listed_price: 150,
    dest_station_id: 0,
    acquired_by_character_id: 0,
    holder_owner_kind: "",
    holder_owner_id: 0,
    holder_name: "",
    ...over,
  };
}

function mount(lots: FWLot[], planPrices: Map<number, number> = new Map()) {
  const onQty = vi.fn();
  const onCost = vi.fn();
  const onPrices = vi.fn();
  render(
    <I18nProvider>
      <LotsPanel
        lots={lots}
        busy={false}
        onAdvance={vi.fn()}
        onDelete={vi.fn()}
        onBulk={vi.fn()}
        onQty={onQty}
        onCost={onCost}
        planPrices={planPrices}
        onPrices={onPrices}
      />
    </I18nProvider>,
  );
  return { onQty, onCost, onPrices };
}

function mockClipboard() {
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
  return writeText;
}

describe("LotsPanel pastes", () => {
  it("disables both pastes with nothing selected", () => {
    mount([lot({})]);
    expect(screen.getByRole("button", { name: /add selected to multibuy/i })).toBeDisabled();
    expect(screen.getByRole("button", { name: /copy selected sell prices/i })).toBeDisabled();
  });

  it("copies the selected lots to multibuy at their remaining quantity", async () => {
    const user = userEvent.setup();
    const writeText = mockClipboard();
    mount([
      lot({ lot_id: 1, type_id: 1, type_name: "Inferno Light Missile", qty: 6000, qty_remaining: 6000 }),
      lot({ lot_id: 2, type_id: 2, type_name: "Warrior II", qty: 40, qty_remaining: 25 }),
    ]);

    await user.click(screen.getByRole("checkbox", { name: "Inferno Light Missile" }));
    await user.click(screen.getByRole("checkbox", { name: "Warrior II" }));
    await user.click(screen.getByRole("button", { name: /add selected to multibuy/i }));

    await waitFor(() => expect(writeText).toHaveBeenCalledWith("Inferno Light Missile 6000\nWarrior II 25"));
  });

  it("copies the selected lots' sell prices snapped to the price grid", async () => {
    const user = userEvent.setup();
    const writeText = mockClipboard();
    mount([lot({ type_name: "Warrior II", listed_price: 123456.789 })]);

    await user.click(screen.getByRole("checkbox", { name: "Warrior II" }));
    await user.click(screen.getByRole("button", { name: /copy selected sell prices/i }));

    const expected = `Warrior II\t${formatGridPrice(123456.789, priceStep(123456.789))}`;
    await waitFor(() => expect(writeText).toHaveBeenCalledWith(expected));
  });
});

describe("LotsPanel quantity edit", () => {
  it("saves once on Enter, with the typed quantity", async () => {
    const user = userEvent.setup();
    const target = lot({ type_name: "Warrior II", qty: 40 });
    const { onQty } = mount([target]);

    const input = screen.getByRole("spinbutton", { name: /quantity for warrior ii/i });
    await user.clear(input);
    await user.type(input, "25{Enter}");

    expect(onQty).toHaveBeenCalledTimes(1);
    expect(onQty).toHaveBeenCalledWith(target, 25);
  });

  it("does not save on Escape, and restores the old quantity", async () => {
    const user = userEvent.setup();
    const { onQty } = mount([lot({ type_name: "Warrior II", qty: 40 })]);

    const input = screen.getByRole("spinbutton", { name: /quantity for warrior ii/i });
    await user.clear(input);
    await user.type(input, "25{Escape}");

    expect(onQty).not.toHaveBeenCalled();
    expect(input).toHaveValue(40);
  });

  it("ignores a zero or empty quantity", async () => {
    const user = userEvent.setup();
    const { onQty } = mount([lot({ type_name: "Warrior II", qty: 40 })]);

    const input = screen.getByRole("spinbutton", { name: /quantity for warrior ii/i });
    await user.clear(input);
    await user.type(input, "0{Enter}");

    expect(onQty).not.toHaveBeenCalled();
    expect(input).toHaveValue(40);
  });
});

describe("LotsPanel unit cost edit", () => {
  it("saves a decimal cost on Enter -- a build cost is rarely a round number", async () => {
    const user = userEvent.setup();
    const target = lot({ type_name: "Warrior II", unit_cost_isk: 100 });
    const { onCost } = mount([target]);

    const input = screen.getByRole("spinbutton", { name: /unit cost for warrior ii/i });
    await user.clear(input);
    await user.type(input, "87.5{Enter}");

    expect(onCost).toHaveBeenCalledTimes(1);
    expect(onCost).toHaveBeenCalledWith(target, 87.5);
  });
});

describe("LotsPanel prices follow the plan", () => {
  it("copies an unlisted lot at the plan's current price and saves it onto the lot", async () => {
    const user = userEvent.setup();
    const writeText = mockClipboard();
    const target = lot({ type_id: 7, type_name: "Warrior II", state: "in_transit", listed_price: 150 });
    const { onPrices } = mount([target], new Map([[7, 180]]));

    await user.click(screen.getByRole("checkbox", { name: "Warrior II" }));
    await user.click(screen.getByRole("button", { name: /copy selected sell prices/i }));

    await waitFor(() => expect(writeText).toHaveBeenCalledWith(`Warrior II\t${formatGridPrice(180, priceStep(180))}`));
    await waitFor(() => expect(onPrices).toHaveBeenCalledWith([{ ...target, listed_price: 180 }]));
  });

  it("leaves a listed lot at its own price -- the in-game order is the truth once it is up", async () => {
    const user = userEvent.setup();
    const writeText = mockClipboard();
    const target = lot({ type_id: 7, type_name: "Warrior II", state: "listed", listed_price: 150 });
    const { onPrices } = mount([target], new Map([[7, 180]]));

    await user.click(screen.getByRole("checkbox", { name: "Warrior II" }));
    await user.click(screen.getByRole("button", { name: /copy selected sell prices/i }));

    await waitFor(() => expect(writeText).toHaveBeenCalledWith(`Warrior II\t${formatGridPrice(150, priceStep(150))}`));
    expect(onPrices).not.toHaveBeenCalled();
  });

  it("does not save anything when the plan agrees with the lot", async () => {
    const user = userEvent.setup();
    const writeText = mockClipboard();
    const { onPrices } = mount([lot({ type_id: 7, type_name: "Warrior II", listed_price: 150 })], new Map([[7, 150]]));

    await user.click(screen.getByRole("checkbox", { name: "Warrior II" }));
    await user.click(screen.getByRole("button", { name: /copy selected sell prices/i }));

    await waitFor(() => expect(writeText).toHaveBeenCalled());
    expect(onPrices).not.toHaveBeenCalled();
  });
});
