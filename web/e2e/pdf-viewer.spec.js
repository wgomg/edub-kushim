import { test, expect } from '@playwright/test';
import { mockApi, seedAuth } from './helpers.js';

test('renders a PDF with pdf.js: canvas, text layer, zoom and find', async ({ page }) => {
	await seedAuth(page);
	await mockApi(page);
	await page.goto('/documents/1');

	await expect(page.getByRole('heading', { name: 'Annual Report 2025' })).toBeVisible();

	const canvas = page.locator('.pdf-page canvas');
	await expect(canvas).toHaveCount(2);
	await expect(canvas.first()).toBeVisible();

	const textLayer = page.locator('.textLayer').first();
	await expect(textLayer).toContainText('Hello World Annual Report');

	await expect(page.getByLabel('Page number')).toHaveValue('1');

	await page.getByLabel('Page number').fill('2');
	await page.getByLabel('Page number').press('Enter');
	await expect(page.getByLabel('Page number')).toHaveValue('2');

	const widthBefore = await canvas.first().evaluate((el) => el.getBoundingClientRect().width);
	await page.getByLabel('Zoom in').click();
	await expect
		.poll(() => canvas.first().evaluate((el) => el.getBoundingClientRect().width))
		.toBeGreaterThan(widthBefore);

	await page.getByLabel('Find in document').click();
	await page.getByLabel('Find text').fill('Annual');
	await expect(page.getByLabel(/^Match \d+ of \d+$/)).toHaveText('1 / 2');
	await expect(page.locator('.textLayer .pdf-find-highlight')).toHaveCount(2);

	await page.getByLabel('Next match').click();
	await expect(page.getByLabel(/^Match \d+ of \d+$/)).toHaveText('2 / 2');
	await expect(page.locator('.textLayer .pdf-find-highlight.pdf-find-current')).toBeVisible();
});
