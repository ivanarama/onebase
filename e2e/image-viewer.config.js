// Separate authenticated fixture: it must not change the shared trade database.
const path = require('node:path');
const base = require('./playwright.config');
module.exports = {
  ...base,
  testDir: './image-viewer-tests',
  timeout: 60_000,
  webServer: {
    ...base.webServer,
    env: {
      ...process.env,
      OB_PROJECT: path.resolve(__dirname, 'fixtures/image-viewer'),
      OB_USER_ROLE: 'Viewer',
      OB_WORK: process.env.OB_WORK || '/tmp/ob-image-viewer-e2e',
    },
  },
};
