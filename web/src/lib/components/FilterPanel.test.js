import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import { get } from 'svelte/store';
import FilterPanel from './FilterPanel.svelte';
import { filterStore } from '$lib/stores/filterStore.js';
import { toastStore } from '$lib/stores/toastStore.svelte.js';

const apiMocks = vi.hoisted(() => ({
	documentTypes: vi.fn(),
	tags: vi.fn(),
	people: vi.fn(),
	filterLanguages: vi.fn()
}));

vi.mock('$lib/api', () => ({
	api: {
		autocomplete: {
			documentTypes: apiMocks.documentTypes,
			tags: apiMocks.tags,
			people: apiMocks.people
		},
		filterLanguages: apiMocks.filterLanguages
	}
}));

describe('FilterPanel', () => {
	beforeEach(() => {
		filterStore.reset();
		for (const t of toastStore.toasts) toastStore.dismiss(t.id);
		apiMocks.documentTypes.mockReset().mockResolvedValue([
			{ id: 1, name: 'Invoice' },
			{ id: 2, name: 'Contract' }
		]);
		apiMocks.tags.mockReset();
		apiMocks.people.mockReset();
		apiMocks.filterLanguages.mockReset().mockResolvedValue(['eng', 'spa']);
	});

	it('emits the selected document type to the filter store', async () => {
		render(FilterPanel);
		const select = await screen.findByLabelText('Document Type');
		fireEvent.change(select, { target: { value: 'Invoice' } });
		await waitFor(() => expect(get(filterStore).documentType).toBe('Invoice'));
	});

	it('adds a tag from the autocomplete suggestions on Enter', async () => {
		apiMocks.tags.mockResolvedValue([{ name: 'invoice' }, { name: 'invoices' }]);
		render(FilterPanel);

		const input = screen.getByLabelText('Tags');
		fireEvent.input(input, { target: { value: 'inv' } });
		const option = await screen.findByRole('option', { name: 'invoice' });
		expect(option).toBeTruthy();

		fireEvent.keyDown(input, { key: 'Enter' });
		await waitFor(() => expect(get(filterStore).tags).toEqual(['invoice']));
	});

	it('shows a toast when tag autocomplete fails', async () => {
		apiMocks.tags.mockRejectedValue(new Error('nope'));
		render(FilterPanel);

		const input = screen.getByLabelText('Tags');
		fireEvent.input(input, { target: { value: 'inv' } });
		await waitFor(() =>
			expect(toastStore.toasts.some((t) => t.message === 'Could not load suggestions')).toBe(true)
		);
	});
});
