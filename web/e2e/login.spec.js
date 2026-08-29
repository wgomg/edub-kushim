import { test, expect } from '@playwright/test';
import { mockApi, TEST_USER } from './helpers.js';

test('signs in with valid credentials and lands on the dashboard', async ({ page }) => {
	await mockApi(page);
	await page.goto('/login');
	await page.fill('#login-username', 'admin');
	await page.fill('#login-password', 'secret');
	await page.getByRole('button', { name: 'Sign In' }).click();

	await expect(page).toHaveURL(/\/$/);
	await expect(page.getByRole('heading', { name: 'Dashboard' })).toBeVisible();
	await expect(page.getByText(TEST_USER.username)).toBeVisible();
});

test('shows an error message for invalid credentials', async ({ page }) => {
	await mockApi(page, {
		'/api/v1/auth/login': (route) =>
			route.fulfill({
				status: 401,
				contentType: 'application/json',
				body: JSON.stringify({ error: 'invalid credentials' })
			})
	});
	await page.goto('/login');
	await page.fill('#login-username', 'admin');
	await page.fill('#login-password', 'wrong');
	await page.getByRole('button', { name: 'Sign In' }).click();

	await expect(
		page.getByText('Invalid username or password. Please check your credentials and try again.')
	).toBeVisible();
});
