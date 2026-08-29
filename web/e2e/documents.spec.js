import { test, expect } from '@playwright/test';
import { mockApi, seedAuth } from './helpers.js';

test('renders the mocked document list and opens the detail route', async ({ page }) => {
	await seedAuth(page);
	await mockApi(page);
	await page.goto('/documents');

	await expect(page.getByRole('heading', { name: 'Documents' })).toBeVisible();
	await expect(page.getByText('Annual Report 2025')).toBeVisible();
	await expect(page.getByText('Contract NDA')).toBeVisible();

	await page.getByText('Annual Report 2025').click();
	await expect(page).toHaveURL(/\/documents\/1$/);
	await expect(page.getByRole('heading', { name: 'Annual Report 2025' })).toBeVisible();
});
