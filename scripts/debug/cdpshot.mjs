// Raw CDP screenshot — bypasses Playwright's stabilization waits.
import { connect, resolveOut, disconnect } from './_lib.mjs';
const outFile = resolveOut(process.argv[2] ?? 'cdpshot.png');
const { browser, page } = await connect();
const cdp = await page.context().newCDPSession(page);
const { data } = await cdp.send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false });
const fs = await import('node:fs');
fs.writeFileSync(outFile, Buffer.from(data, 'base64'));
console.log(outFile, fs.statSync(outFile).size);
await disconnect(browser);
