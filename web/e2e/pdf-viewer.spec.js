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

test('document actions and inspector toggle live in the PDF toolbar', async ({ page }) => {
	await seedAuth(page);
	await mockApi(page);
	await page.goto('/documents/1');

	await expect(page.getByRole('heading', { name: 'Annual Report 2025' })).toBeVisible();

	const viewerRoot = page.locator('main [role="region"][aria-label^="PDF viewer"]').locator('..');
	const toolbar = viewerRoot.locator('> div').first();

	await expect(toolbar.getByLabel('Download PDF')).toBeVisible();
	await expect(toolbar.getByLabel('Re-enrich', { exact: true })).toBeVisible();
	await expect(toolbar.getByLabel('Delete document')).toBeVisible();

	const inspector = page.locator('main aside');
	await expect(inspector).toBeVisible();

	const toggle = toolbar.getByRole('button', { name: 'Hide document info' });
	await expect(toggle).toHaveAttribute('aria-expanded', 'true');
	await toggle.click();
	await expect(inspector).toHaveCount(0);
	await expect(toolbar.getByRole('button', { name: 'Show document info' })).toHaveAttribute(
		'aria-expanded',
		'false'
	);

	await toolbar.getByRole('button', { name: 'Show document info' }).click();
	await expect(inspector).toBeVisible();
	await expect(toolbar.getByRole('button', { name: 'Hide document info' })).toHaveAttribute(
		'aria-expanded',
		'true'
	);

	await page.getByLabel('Find in document').click();
	const findInput = page.getByLabel('Find text');
	await findInput.fill('Annual');
	await expect(page.getByLabel(/^Match \d+ of \d+$/)).toHaveText('1 / 2');

	await findInput.press('Escape');
	await expect(findInput).toHaveCount(0);
	await expect(page.getByLabel('Find in document')).toHaveAttribute('aria-expanded', 'false');
});
