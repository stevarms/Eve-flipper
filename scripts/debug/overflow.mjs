// Walks every workspace at several widths and reports horizontal overflow.
//
// The design system's rule is that the page body must never scroll sideways:
// wide content scrolls inside its own container. This checks the body, and
// names any element wider than the viewport that is not inside an
// overflow-x:auto ancestor.
//
// Usage: node scripts/debug/overflow.mjs [url] [widths]
import { connect, disconnect } from './_lib.mjs';

const url = process.argv[2] ?? 'http://localhost:13370';
const widths = (process.argv[3] ?? '1920,1440,1280').split(',').map(Number);

const { browser, page } = await connect();
const ctx = page.context();
const p = await ctx.newPage();

const probe = () =>
  p.evaluate(() => {
    const vw = document.documentElement.clientWidth;
    const scrolls = (el) => {
      for (let n = el; n; n = n.parentElement) {
        const ov = getComputedStyle(n).overflowX;
        if (ov === 'auto' || ov === 'scroll' || ov === 'hidden') return true;
      }
      return false;
    };
    const bad = [...document.querySelectorAll('body *')]
      .filter((el) => el.getBoundingClientRect().right > vw + 1 && !scrolls(el))
      .slice(0, 3)
      .map((el) => `${el.tagName.toLowerCase()}.${(el.className || '').toString().split(' ')[0]}`);
    return { bodyScroll: document.body.scrollWidth - vw, bad };
  });

try {
  await p.goto(url, { waitUntil: 'domcontentloaded', timeout: 45000 });
  await p.waitForTimeout(9000);

  for (const w of widths) {
    await p.setViewportSize({ width: w, height: 1000 });
    await p.waitForTimeout(800);
    const rail = p.locator('nav[aria-label="Workspaces"] button');
    const n = await rail.count();
    const out = [];
    for (let i = 0; i < n; i++) {
      const label = await rail.nth(i).getAttribute('aria-label');
      await rail.nth(i).click({ timeout: 5000 }).catch(() => {});
      await p.waitForTimeout(1200);
      const r = await probe();
      out.push(`${label}:${r.bodyScroll}${r.bad.length ? ` [${r.bad.join(', ')}]` : ''}`);
    }
    console.log(`${w}px  overflow px per workspace  ${out.join('  ')}`);
  }
} finally {
  await p.close();
  await disconnect(browser);
}
