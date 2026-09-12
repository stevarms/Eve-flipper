/* @vitest-environment jsdom */

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { CopyPrice } from "./CopyPrice";
import { I18nProvider } from "@/lib/i18n";
import { ToastProvider } from "@/components/Toast";

/**
 * The point of CopyPrice is that what reaches the clipboard is a PLAIN number.
 * EVE's price field rejects "1.23 M", "1,234.56" and every other formatted
 * form, so a well-meaning refactor that routes this through formatIsk breaks
 * pasting silently — the button still appears to work. These assertions are
 * the guard.
 */

const writeText = vi.fn((_text: string) => Promise.resolve());

beforeEach(() => {
  writeText.mockClear();
  Object.assign(navigator, { clipboard: { writeText } });
});

afterEach(cleanup);

function renderPrice(props: Parameters<typeof CopyPrice>[0]) {
  return render(
    <I18nProvider>
      <ToastProvider>
        <CopyPrice {...props} />
      </ToastProvider>
    </I18nProvider>,
  );
}

async function copied(props: Parameters<typeof CopyPrice>[0]): Promise<string> {
  renderPrice(props);
  await userEvent.click(screen.getByRole("button"));
  expect(writeText).toHaveBeenCalledTimes(1);
  return writeText.mock.calls[0][0];
}

describe("CopyPrice", () => {
  it("writes plain digits with no thousands separators or tier suffix", async () => {
    const text = await copied({ value: 1234567.891, label: "Copy price" });
    expect(text).toBe("1234567.89");
    expect(text).not.toMatch(/[,\s]/);
    expect(text).not.toMatch(/[kMBT]/);
  });

  it("keeps sub-10-ISK undercuts intact when given a grid step", async () => {
    // toFixed(2) would round 5.499 up to 5.50 — back above the price it was
    // undercutting. The whole reason `step` exists.
    const text = await copied({ value: 5.499, step: 0.001, label: "Copy price" });
    expect(text).toBe("5.499");
  });

  it("snaps to EVE's 4-significant-digit grid on large prices", async () => {
    const text = await copied({ value: 12350000, step: 10000, label: "Copy price" });
    expect(text).toBe("12350000");
    expect(text).not.toContain(".");
  });

  it("is parseable back to the number it displayed", async () => {
    const text = await copied({ value: 42.5, label: "Copy price" });
    expect(Number(text)).toBeCloseTo(42.5, 2);
  });
});
