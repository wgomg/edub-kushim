import { defineConfig } from '@playwright/test';

export default defineConfig({
	testDir: 'e2e',
	fullyParallel: true,
	forbidOnly: !!process.env.CI,
	retries: process.env.CI ? 2 : 0,
	reporter: 'list',
	use: {
		baseURL: 'http://localhost:4173'
	},
	webServer: {
		command: 'npm run build && node scripts/serve-static.mjs',
		port: 4173,
		reuseExistingServer: !process.env.CI,
		timeout: 120_000
	}
});
