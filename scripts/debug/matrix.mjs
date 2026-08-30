// Full-page screenshots at multiple viewport widths (responsive sweep).
// NOTE: resizes the actual Chrome window while running — that's how CDP works.
// Usage: node scripts/debug/matrix.mjs [route] [widths]
//   node scripts/debug/matrix.mjs /industry
//   node scripts/debug/matrix.mjs /station 1920,1600,1280,1024
import { connect, gotoRoute, resolveOut, disconnect } from './_lib.mjs';

const route = process.argv[2] ?? '/';
const widths = (process.argv[3] ?? '1920,1440,1280').split(',').map((w) => parseInt(w, 10));
const HEIGHT = 900;

const { browser, page } = await connect();
await gotoRoute(page, route);

const routeSlug = route.replaceAll('/', '_').replace(/^_/, '') || 'root';
const outputs = [];
for (const w of widths) {
  await page.setViewportSize({ width: w, height: HEIGHT });
  await page.waitForTimeout(300);
  const out = resolveOut(`matrix/${routeSlug}_${w}.png`);
  await page.screenshot({ path: out, fullPage: true });
  outputs.push(out);
}
console.log(outputs.join('\n'));
await disconnect(browser);
