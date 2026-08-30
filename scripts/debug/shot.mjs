// Full-page screenshot of a route.
// Usage: node scripts/debug/shot.mjs [route] [outFile]
//   node scripts/debug/shot.mjs /
//   node scripts/debug/shot.mjs /industry industry.png
import { connect, gotoRoute, resolveOut, disconnect } from './_lib.mjs';

const route = process.argv[2] ?? '/';
const outFile = resolveOut(process.argv[3] ?? 'shot.png');

const { browser, page } = await connect();
await gotoRoute(page, route);
await page.screenshot({ path: outFile, fullPage: true });
console.log(outFile);
await disconnect(browser);
