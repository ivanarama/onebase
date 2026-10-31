// Runs only on an explicit `onebase docs capture`. Playwright is optional.
const fs = require('node:fs');
const path = require('node:path');

async function capture(config, chromium) {
  const scenario = config.scenario;
  const base = new URL(scenario.base_url);
  if (!['http:', 'https:'].includes(base.protocol)) throw new Error('base_url must be HTTP(S)');
  const browser = await chromium.launch({ headless: true });
  try {
    const context = await browser.newContext({
      viewport: scenario.viewport || { width: 1440, height: 1000 },
      locale: scenario.locale || 'ru-RU',
      ...(config.storageState ? { storageState: config.storageState } : {}),
    });
    const page = await context.newPage();
    page.setDefaultTimeout(15000);
    await page.emulateMedia({ reducedMotion: 'reduce' });
    for (const shot of scenario.screenshots || []) {
      const target = new URL(shot.path, base);
      if (target.origin !== base.origin) throw new Error('screenshot path must stay on base_url origin');
      if (!/^[a-zA-Z0-9][a-zA-Z0-9_-]*\.png$/.test(shot.file)) throw new Error('screenshot file must be a simple .png name');
      await page.goto(target.href, { waitUntil: 'domcontentloaded' });
      for (const step of shot.steps || []) {
        const locator = page.locator(step.selector);
        if (step.action === 'fill') await locator.fill(step.value);
        else if (step.action === 'click') await locator.click();
        else if (step.action === 'select') await locator.selectOption(step.value);
        else if (step.action === 'wait') await locator.waitFor({ state: 'visible' });
        else throw new Error(`unknown screenshot step: ${step.action}`);
      }
      if (shot.wait_for) await page.locator(shot.wait_for).waitFor({ state: 'visible' });
      // Agent scenarios must target a demonstration base with synthetic data.
      const masks = (shot.mask || []).map(selector => page.locator(selector));
      const style = (shot.hide || []).map(selector => `${selector}{visibility:hidden!important}`).join('\n');
      const options = { path: path.join(config.output, shot.file), animations: 'disabled', mask: masks, style };
      if (shot.selector) await page.locator(shot.selector).screenshot(options);
      else await page.screenshot({ ...options, fullPage: true });
      process.stdout.write(`${shot.file}\n`);
    }
  } finally {
    await browser.close();
  }
}

function run() {
  let input = '';
  process.stdin.setEncoding('utf8');
  process.stdin.on('data', chunk => { input += chunk; });
  process.stdin.on('end', async () => {
    try {
      const config = JSON.parse(input);
      fs.mkdirSync(config.output, { recursive: true });
      const { chromium } = require(config.module || 'playwright');
      await capture(config, chromium);
    } catch (error) {
      process.stderr.write(`Documentation screenshots: ${error.message}\n`);
      process.exitCode = 1;
    }
  });
}
module.exports = { capture, run };
if (require.main === module) run();
