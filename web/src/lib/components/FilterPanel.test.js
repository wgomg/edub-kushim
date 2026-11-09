import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import FilterPanel from './FilterPanel.svelte';
import { toastStore } from '#lib/stores/toastStore.svelte.js';

const apiMocks = vi.hoisted(() => ({
	documentTypes: vi.fn(),
	tags: vi.fn(),
	people: vi.fn(),
	filterLanguages: vi.fn()
}));

vi.mock('#lib/api.js', () => ({
	api: {
		autocomplete: {
			documentTypes: apiMocks.documentTypes,
			tags: apiMocks.tags,
			people: apiMocks.people
		},
		filterLanguages: apiMocks.filterLanguages
	}
}));

function baseFilters(overrides = {}) {
	return {
		tags: [],
		people: [],
		documentType: '',
		language: '',
		dateCreated: { from: null, to: null },
		dateModified: { from: null, to: null },
		fileSize: { min: null, max: null },
		missingLanguage: false,
		missingType: false,
		untagged: false,
		...overrides
	};
}

describe('FilterPanel', () => {
	beforeEach(() => {
		for (const t of toastStore.toasts) toastStore.dismiss(t.id);
		apiMocks.documentTypes.mockReset().mockResolvedValue([
			{ id: 1, name: 'Invoice' },
			{ id: 2, name: 'Contract' }
		]);
		apiMocks.tags.mockReset();
		apiMocks.people.mockReset();
		apiMocks.filterLanguages.mockReset().mockResolvedValue(['eng', 'spa']);
	});

	it('emits the selected document type', async () => {
		const onChange = vi.fn();
		render(FilterPanel, { filters: baseFilters(), onChange });
		const select = await screen.findByLabelText('Document Type');
		fireEvent.change(select, { target: { value: 'Invoice' } });
		await waitFor(() => expect(onChange).toHaveBeenCalledWith({ documentType: 'Invoice' }));
	});

	it('adds a tag from the autocomplete suggestions on Enter', async () => {
		apiMocks.tags.mockResolvedValue([{ name: 'invoice' }, { name: 'invoices' }]);
		const onChange = vi.fn();
		render(FilterPanel, { filters: baseFilters(), onChange });

		const input = screen.getByLabelText('Tags');
		fireEvent.input(input, { target: { value: 'inv' } });
		const option = await screen.findByRole('option', { name: 'invoice' });
		expect(option).toBeTruthy();

		fireEvent.keyDown(input, { key: 'Enter' });
		await waitFor(() => expect(onChange).toHaveBeenCalledWith({ tags: ['invoice'] }));
	});

	it('shows the matched alias hint and selects the canonical tag', async () => {
		apiMocks.tags.mockResolvedValue([{ name: 'netherlands', matched_alias: 'holland' }]);
		const onChange = vi.fn();
		render(FilterPanel, { filters: baseFilters(), onChange });

		const input = screen.getByLabelText('Tags');
		fireEvent.input(input, { target: { value: 'holl' } });
		const option = await screen.findByRole('option', { name: /netherlands/ });
		expect(option.textContent).toContain('netherlands');
		expect(option.textContent).toContain('alias: holland');

		fireEvent.keyDown(input, { key: 'Enter' });
		await waitFor(() => expect(onChange).toHaveBeenCalledWith({ tags: ['netherlands'] }));
	});

	it('shows a toast when tag autocomplete fails', async () => {
		apiMocks.tags.mockRejectedValue(new Error('nope'));
		render(FilterPanel, { filters: baseFilters(), onChange: vi.fn() });

		const input = screen.getByLabelText('Tags');
		fireEvent.input(input, { target: { value: 'inv' } });
		await waitFor(() =>
			expect(toastStore.toasts.some((t) => t.message === 'Could not load suggestions')).toBe(true)
		);
	});
});
