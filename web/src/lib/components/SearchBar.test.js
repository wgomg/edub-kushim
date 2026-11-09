import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';

const apiMocks = vi.hoisted(() => ({
	tags: vi.fn(),
	people: vi.fn(),
	documentTypes: vi.fn()
}));

vi.mock('#lib/api.js', () => ({
	api: {
		autocomplete: {
			tags: apiMocks.tags,
			people: apiMocks.people,
			documentTypes: apiMocks.documentTypes
		}
	}
}));

import SearchBar from './SearchBar.svelte';

function renderSearchBar(props = {}) {
	const onSearch = vi.fn();
	render(SearchBar, {
		q: '',
		tags: [],
		people: [],
		documentType: '',
		language: '',
		dateCreated: { from: null, to: null },
		dateModified: { from: null, to: null },
		fileSize: { min: null, max: null },
		personTypes: [],
		...props,
		onSearch
	});
	return onSearch;
}

beforeEach(() => {
	apiMocks.tags.mockReset().mockResolvedValue([]);
	apiMocks.people.mockReset().mockResolvedValue([]);
	apiMocks.documentTypes.mockReset().mockResolvedValue([]);
});

describe('SearchBar free-text commits', () => {
	it('emits free text under the q key of the patch', async () => {
		const onSearch = renderSearchBar();
		const input = screen.getByLabelText('Search documents');

		await fireEvent.input(input, { target: { value: 'annual' } });
		await fireEvent.keyDown(input, { key: 'Enter' });

		await waitFor(() => expect(onSearch).toHaveBeenCalledTimes(1));
		const patch = onSearch.mock.calls[0][0];
		expect(patch).toHaveProperty('q', 'annual');
		expect(patch).not.toHaveProperty('query');
	});

	it('appends free text to the query it already holds', async () => {
		const onSearch = renderSearchBar({ q: 'existing' });
		const input = screen.getByLabelText('Search documents');

		await fireEvent.input(input, { target: { value: 'annual' } });
		await fireEvent.keyDown(input, { key: 'Enter' });

		await waitFor(() => expect(onSearch).toHaveBeenCalledTimes(1));
		expect(onSearch.mock.calls[0][0]).toHaveProperty('q', 'existing annual');
	});

	it('turns a field prefix into a facet while keeping the free text', async () => {
		const onSearch = renderSearchBar({ q: 'annual' });
		const input = screen.getByLabelText('Search documents');

		await fireEvent.input(input, { target: { value: 'tag:invoice' } });
		await fireEvent.keyDown(input, { key: 'Enter' });

		await waitFor(() => expect(onSearch).toHaveBeenCalledTimes(1));
		const patch = onSearch.mock.calls[0][0];
		expect(patch).toEqual(expect.objectContaining({ q: 'annual', tags: ['invoice'] }));
	});

	it('backspace on an empty input removes the last facet, not the free text', async () => {
		const onSearch = renderSearchBar({ q: 'annual', tags: ['finance', 'audit'] });
		const input = screen.getByLabelText('Search documents');

		await fireEvent.keyDown(input, { key: 'Backspace' });

		await waitFor(() => expect(onSearch).toHaveBeenCalledTimes(1));
		expect(onSearch.mock.calls[0][0]).toEqual(
			expect.objectContaining({ q: 'annual', tags: ['finance'] })
		);
	});

	it('keeps an unrecognized field prefix as free text instead of dropping it', async () => {
		const onSearch = renderSearchBar({ q: 'annual' });
		const input = screen.getByLabelText('Search documents');

		await fireEvent.input(input, { target: { value: 'foo:bar' } });
		await fireEvent.keyDown(input, { key: 'Enter' });

		await waitFor(() => expect(onSearch).toHaveBeenCalledTimes(1));
		expect(onSearch.mock.calls[0][0]).toHaveProperty('q', 'annual foo:bar');
	});

	it('keeps an unparsable date value as free text', async () => {
		const onSearch = renderSearchBar({ q: 'annual' });
		const input = screen.getByLabelText('Search documents');

		await fireEvent.input(input, { target: { value: 'created:garbage' } });
		await fireEvent.keyDown(input, { key: 'Enter' });

		await waitFor(() => expect(onSearch).toHaveBeenCalledTimes(1));
		expect(onSearch.mock.calls[0][0]).toHaveProperty('q', 'annual created:garbage');
	});
});
