// Screenshot a single element by selector (CSS, or Playwright text=/role= locator).
// Usage: node scripts/debug/shot-el.mjs <selector> [route] [outFile]
//   node scripts/debug/shot-el.mjs "text=Station Trading" / tab.png
//   node scripts/debug/shot-el.mjs "[data-testid=order-row]" /station orders.png
import { connect, gotoRoute, resolveOut, disconnect } from './_lib.mjs';

const selector = process.argv[2];
if (!selector) {
  console.error('usage: node shot-el.mjs <selector> [route] [outFile]');
  process.exit(2);
}
const route = process.argv[3] ?? '/';
const outFile = resolveOut(process.argv[4] ?? 'el.png');

const { browser, page } = await connect();
await gotoRoute(page, route);

const el = page.locator(selector).first();
await el.waitFor({ state: 'visible', timeout: 5000 });
await el.scrollIntoViewIfNeeded();
await el.screenshot({ path: outFile });
console.log(outFile);
await disconnect(browser);
