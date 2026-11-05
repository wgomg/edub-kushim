import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import TagsPage from './+page.svelte';
import * as authStore from '#lib/stores/authStore.svelte.js';

const apiMocks = vi.hoisted(() => ({
	list: vi.fn(),
	reconcileReport: vi.fn(),
	provisionalList: vi.fn(),
	deletedList: vi.fn(),
	auditFlags: vi.fn(),
	siblings: vi.fn(),
	split: vi.fn(),
	splitRulesList: vi.fn(),
	revertSplitRule: vi.fn()
}));

vi.mock('$app/navigation', () => ({ goto: vi.fn() }));
vi.mock('$app/paths', () => ({ resolve: (p) => p }));

vi.mock('#lib/api.js', () => ({
	api: {
		tags: {
			list: apiMocks.list,
			reconcileReport: apiMocks.reconcileReport,
			provisionalList: apiMocks.provisionalList,
			deletedList: apiMocks.deletedList,
			auditFlags: apiMocks.auditFlags,
			siblings: apiMocks.siblings,
			split: apiMocks.split,
			splitRulesList: apiMocks.splitRulesList,
			revertSplitRule: apiMocks.revertSplitRule
		}
	}
}));

vi.mock('#lib/stores/toastStore.svelte.js', () => ({
	toastStore: { success: vi.fn(), error: vi.fn() }
}));

beforeEach(() => {
	vi.clearAllMocks();
	apiMocks.list.mockResolvedValue({
		results: [
			{
				id: 7,
				name: 'sample compound',
				document_count: 2,
				promoted: true,
				seeded: false,
				compound: true,
				created_at: '2026-01-01T00:00:00Z'
			}
		],
		total: 1
	});
	apiMocks.reconcileReport.mockResolvedValue({ ok: true, data: {} });
	apiMocks.provisionalList.mockResolvedValue({
		ok: true,
		data: { results: [], total: 0, pending_identity_total: 0 }
	});
	apiMocks.deletedList.mockResolvedValue({ results: [], total: 0 });
	apiMocks.auditFlags.mockResolvedValue({ results: [], total: 0 });
	apiMocks.siblings.mockResolvedValue({ total: 0 });
	apiMocks.split.mockResolvedValue({ ok: true, status: 204 });
	apiMocks.splitRulesList.mockResolvedValue({ results: [], total: 0 });
	apiMocks.revertSplitRule.mockResolvedValue({ ok: true, status: 204 });
	authStore.logout();
	authStore.setAuthEnabled(true);
	authStore.setAuthResolved(true);
	authStore.login('token', { role: 'editor' });
});

describe('tags page vocabulary split', () => {
	it('splits a vocabulary compound by name through the shared modal', async () => {
		render(TagsPage);

		await fireEvent.click(await screen.findByTitle('Split into components'));
		expect(await screen.findByRole('dialog', { name: 'Split compound' })).toBeTruthy();

		await fireEvent.input(screen.getByLabelText('Component 1'), {
			target: { value: 'machinery' }
		});
		await fireEvent.click(screen.getByRole('button', { name: 'Split' }));

		await waitFor(() =>
			expect(apiMocks.split).toHaveBeenCalledWith('sample compound', ['machinery'])
		);
	});
});
