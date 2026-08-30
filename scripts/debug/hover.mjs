// Hover an element and screenshot to capture its tooltip.
// Also reports tooltip content (role=tooltip / aria-describedby / title / aria-label) to stderr.
// Full-page shot so portal-rendered tooltips are captured even if they escape the trigger's subtree.
//
// Usage: node scripts/debug/hover.mjs <trigger-selector> [route] [outFile]
//   node scripts/debug/hover.mjs "text=Scan Blueprints" /industry scan-btn.png
//   node scripts/debug/hover.mjs "th:has-text('ISK/h')" /industry iskh-header.png
//
// Env:
//   EF_HOVER_TIMEOUT_MS  max wait for a [role=tooltip] to appear (default 800)
//   EF_HOVER_SETTLE_MS   extra settle after tooltip appears / falls back (default 150)
import { connect, gotoRoute, resolveOut, disconnect } from './_lib.mjs';

const selector = process.argv[2];
if (!selector) {
  console.error('usage: node hover.mjs <trigger-selector> [route] [outFile]');
  process.exit(2);
}
const route = process.argv[3] ?? '/';
const outFile = resolveOut(process.argv[4] ?? 'hover.png');
const tipTimeout = parseInt(process.env.EF_HOVER_TIMEOUT_MS ?? '800', 10);
const settleMs = parseInt(process.env.EF_HOVER_SETTLE_MS ?? '150', 10);

const { browser, page } = await connect();
await gotoRoute(page, route);

// Park the mouse first so re-hovering the trigger actually fires a fresh hover.
await page.mouse.move(0, 0);
await page.waitForTimeout(50);

const trigger = page.locator(selector).first();
await trigger.waitFor({ state: 'visible', timeout: 5000 });
await trigger.scrollIntoViewIfNeeded();

const triggerMeta = await trigger.evaluate((el) => ({
  title: el.getAttribute('title'),
  ariaLabel: el.getAttribute('aria-label'),
  ariaDescribedBy: el.getAttribute('aria-describedby'),
  tag: el.tagName.toLowerCase(),
  text: (el.innerText ?? '').slice(0, 80),
}));

await trigger.hover();

let tooltipText = null;
let tooltipSource = null;
try {
  const tip = page.locator('[role="tooltip"]:visible').first();
  await tip.waitFor({ state: 'visible', timeout: tipTimeout });
  tooltipText = (await tip.innerText()).trim();
  tooltipSource = 'role=tooltip';
} catch {
  // No role=tooltip. Try aria-describedby target.
  if (triggerMeta.ariaDescribedBy) {
    const desc = page.locator('#' + triggerMeta.ariaDescribedBy).first();
    if (await desc.isVisible({ timeout: 200 }).catch(() => false)) {
      tooltipText = (await desc.innerText()).trim();
      tooltipSource = 'aria-describedby';
    }
  }
  await page.waitForTimeout(settleMs);
}
if (tooltipText) await page.waitForTimeout(settleMs);

await page.screenshot({ path: outFile, fullPage: true });
console.log(outFile);

console.error(JSON.stringify({
  trigger: { tag: triggerMeta.tag, text: triggerMeta.text },
  tooltip: {
    domText: tooltipText,
    domSource: tooltipSource,
    triggerTitle: triggerMeta.title,        // native title attr, NOT in screenshot
    triggerAriaLabel: triggerMeta.ariaLabel,
    triggerAriaDescribedBy: triggerMeta.ariaDescribedBy,
  },
}, null, 2));

await disconnect(browser);
