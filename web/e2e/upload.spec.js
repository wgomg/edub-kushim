import { test, expect } from '@playwright/test';
import { mockApi, seedAuth } from './helpers.js';

test('queues an uploaded file through the mocked API', async ({ page }) => {
	await seedAuth(page);
	await mockApi(page);
	await page.goto('/documents');

	await page.getByRole('button', { name: 'Upload' }).click();
	const dialog = page.getByRole('dialog', { name: 'Upload documents' });
	await expect(dialog).toBeVisible();

	await page.setInputFiles('input[type="file"]', {
		name: 'test.pdf',
		mimeType: 'application/pdf',
		buffer: Buffer.from('%PDF-1.4 test')
	});
	await expect(dialog.getByText('test.pdf')).toBeVisible();

	await dialog.getByRole('button', { name: 'Upload' }).click();
	await expect(dialog.getByText('1 file(s) queued')).toBeVisible();
	await expect(dialog.getByRole('link', { name: 'View tasks' })).toBeVisible();
});
