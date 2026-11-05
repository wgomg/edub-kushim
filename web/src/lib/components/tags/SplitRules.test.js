import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import SplitRules from './SplitRules.svelte';
import * as authStore from '#lib/stores/authStore.svelte.js';

const { splitRulesList, revertSplitRule, confirmMock, toastMock } = vi.hoisted(() => ({
	splitRulesList: vi.fn(),
	revertSplitRule: vi.fn(),
	confirmMock: vi.fn(),
	toastMock: { success: vi.fn(), error: vi.fn() }
}));

vi.mock('#lib/api.js', () => ({ api: { tags: { splitRulesList, revertSplitRule } } }));
vi.mock('#lib/stores/confirmStore.svelte.js', () => ({ confirmStore: { confirm: confirmMock } }));
vi.mock('#lib/stores/toastStore.svelte.js', () => ({ toastStore: toastMock }));

const rule = {
	candidate: 'machine learning',
	parts: ['machine', 'learning'],
	created_at: '2026-01-01T00:00:00Z'
};

beforeEach(() => {
	splitRulesList.mockReset();
	revertSplitRule.mockReset();
	confirmMock.mockReset();
	toastMock.success.mockReset();
	toastMock.error.mockReset();
	authStore.logout();
	authStore.setAuthEnabled(true);
	authStore.setAuthResolved(true);
	authStore.login('token', { role: 'editor' });
});

describe('SplitRules', () => {
	it('lists the recorded rules with their applied parts', async () => {
		splitRulesList.mockResolvedValue({ results: [rule], total: 1 });

		render(SplitRules);

		expect(await screen.findByText('machine learning')).toBeTruthy();
		expect(await screen.findByText('machine, learning')).toBeTruthy();
	});

	it('reverts a rule after confirmation and reports the result', async () => {
		splitRulesList.mockResolvedValue({ results: [rule], total: 1 });
		revertSplitRule.mockResolvedValue({ ok: true, status: 204 });
		confirmMock.mockResolvedValue(true);
		const onchanged = vi.fn();

		render(SplitRules, { onchanged });
		await screen.findByText('machine learning');
		await fireEvent.click(screen.getByTitle('Revert split'));

		await waitFor(() => expect(revertSplitRule).toHaveBeenCalledWith('machine learning'));
		expect(confirmMock).toHaveBeenCalledTimes(1);
		expect(toastMock.success).toHaveBeenCalledTimes(1);
		expect(onchanged).toHaveBeenCalledTimes(1);
	});

	it('keeps the rule when the revert is refused', async () => {
		splitRulesList.mockResolvedValue({ results: [rule], total: 1 });
		revertSplitRule.mockResolvedValue({ ok: false, status: 404 });
		confirmMock.mockResolvedValue(true);
		const onchanged = vi.fn();

		render(SplitRules, { onchanged });
		await screen.findByText('machine learning');
		await fireEvent.click(screen.getByTitle('Revert split'));

		await waitFor(() => expect(toastMock.error).toHaveBeenCalledTimes(1));
		expect(onchanged).not.toHaveBeenCalled();
	});

	it('does not call the API when the revert is declined', async () => {
		splitRulesList.mockResolvedValue({ results: [rule], total: 1 });
		confirmMock.mockResolvedValue(false);

		render(SplitRules);
		await screen.findByText('machine learning');
		await fireEvent.click(screen.getByTitle('Revert split'));

		await waitFor(() => expect(confirmMock).toHaveBeenCalledTimes(1));
		expect(revertSplitRule).not.toHaveBeenCalled();
	});
});
