import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/svelte';
import TrashPage from './+page.svelte';
import * as authStore from '#lib/stores/authStore.svelte.js';

const apiMocks = vi.hoisted(() => ({
	trashList: vi.fn()
}));

vi.mock('$app/state', () => ({
	page: { url: new URL('http://localhost/trash') }
}));
vi.mock('$app/navigation', () => ({ goto: vi.fn() }));
vi.mock('$app/paths', () => ({ resolve: (p) => p }));
vi.mock('#lib/api.js', () => ({
	api: {
		trash: {
			list: apiMocks.trashList
		}
	}
}));

beforeEach(() => {
	vi.clearAllMocks();
	apiMocks.trashList.mockResolvedValue({ results: [], total: 0 });
	authStore.logout();
	authStore.setAuthEnabled(true);
	authStore.setAuthResolved(false);
});

describe('trash page authorization gate', () => {
	it('shows the loading state while the auth bootstrap is unresolved', () => {
		render(TrashPage);

		expect(screen.getByText('Loading…')).toBeTruthy();
		expect(screen.queryByText(/do not have permission to view this page/)).toBeNull();
		expect(apiMocks.trashList).not.toHaveBeenCalled();
	});

	it('denies the page once resolved with auth enabled and no editor role', () => {
		authStore.setAuthResolved(true);

		render(TrashPage);

		expect(screen.getByText(/do not have permission to view this page/)).toBeTruthy();
		expect(screen.queryByText('Loading…')).toBeNull();
		expect(apiMocks.trashList).not.toHaveBeenCalled();
	});

	it('renders the page once resolved with auth disabled', async () => {
		authStore.setAuthEnabled(false);
		authStore.setAuthResolved(true);

		render(TrashPage);

		expect(screen.queryByText(/do not have permission to view this page/)).toBeNull();
		expect(await screen.findByText('Purge expired')).toBeTruthy();
		await waitFor(() => expect(apiMocks.trashList).toHaveBeenCalled());
	});
});
