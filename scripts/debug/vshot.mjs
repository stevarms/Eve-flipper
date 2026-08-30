// Viewport-only screenshot (no fullPage) — safe on huge pages.
import { connect, gotoRoute, resolveOut, disconnect } from './_lib.mjs';
const route = process.argv[2] ?? '/';
const outFile = resolveOut(process.argv[3] ?? 'vshot.png');
const { browser, page } = await connect();
await gotoRoute(page, route);
await page.setViewportSize({ width: 1600, height: 1000 }).catch(()=>{});
await page.screenshot({ path: outFile, fullPage: false, timeout: 20000, animations: 'disabled', caret: 'hide' });
console.log(outFile);
await disconnect(browser);
