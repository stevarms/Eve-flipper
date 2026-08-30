// One-off: get the worktree's throwaway instance past first-run setup and
// into Station Trade with real rows, so UI work can be verified against
// live data instead of built blind.
import { connect, disconnect } from './_lib.mjs';
import fs from 'node:fs';

const url = process.argv[2] ?? 'http://localhost:1420';
const { browser, page } = await connect();
const ctx = page.context();
const p = await ctx.newPage();

const shot = async (name) => {
  const cdp = await ctx.newCDPSession(p);
  const { data } = await cdp.send('Page.captureScreenshot', { format: 'png' });
  fs.writeFileSync(`.debug/${name}.png`, Buffer.from(data, 'base64'));
  console.log('shot ->', name);
};

try {
  await p.setViewportSize({ width: 1600, height: 1000 });
  await p.goto(url, { waitUntil: 'domcontentloaded', timeout: 45000 });
  await p.waitForTimeout(8000);

  // First run: pick the standard vault so the modal goes away.
  const vault = p.getByRole('button', { name: /use standard vault/i });
  if (await vault.count()) {
    await vault.first().click({ timeout: 8000 }).catch((e) => console.log('vault click:', e.message));
    await p.waitForTimeout(4000);
    console.log('vault configured');
  }

  // Close anything else modal-ish that is in the way.
  await p.keyboard.press('Escape').catch(() => {});
  await p.waitForTimeout(1000);

  console.log('sde/header:', await p.locator('header, [class*="header"]').first().innerText().catch(() => '(n/a)'));
  await shot('worktree-ready');
} finally {
  await p.close();
  await disconnect(browser);
}
