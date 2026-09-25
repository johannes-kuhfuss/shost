const { defineConfig } = require('@playwright/test');

module.exports = defineConfig({
  testDir: '.',
  testMatch: '*.spec.js',
  workers: 1,
  timeout: 30000,
  use: { baseURL: 'http://127.0.0.1:18081', browserName: 'chromium' },
  webServer: {
    command: process.env.DEMO_SERVICE_BINARY || 'go run .',
    cwd: '..',
    url: 'http://127.0.0.1:18081/health/ready',
    reuseExistingServer: false,
    env: {
      USE_TLS: 'false', SERVER_HOST: '127.0.0.1', SERVER_PORT: '18081',
      TEMPLATE_PATH: './templates', DRAIN_REQUESTS_TIME: '0',
      GRACEFUL_SHUTDOWN_TIME: '1', LOG_LEVEL: 'error'
    }
  }
});
