import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import { documentsLabel } from '#lib/utils/html.js';
import TagDocumentsPanel from './TagDocumentsPanel.svelte';

const { searchStructuredRaw } = vi.hoisted(() => ({
	searchStructuredRaw: vi.fn()
}));

vi.mock('$app/paths', () => ({ resolve: (p) => p }));
vi.mock('#lib/api.js', async (importOriginal) => {
	const actual = await importOriginal();
	return {
		api: { documents: { searchStructuredRaw } },
		encodeCriteria: actual.encodeCriteria
	};
});

function documents(total, results) {
	return {
		ok: true,
		status: 200,
		data: { results, total }
	};
}

function row(overrides = {}) {
	return {
		id: 'doc-1',
		title: 'First document',
		tags: [
			{ id: 2, name: 'sample tag' },
			{ id: 3, name: 'another tag' }
		],
		...overrides
	};
}

beforeEach(() => {
	searchStructuredRaw.mockReset();
	searchStructuredRaw.mockResolvedValue(documents(0, []));
});

describe('TagDocumentsPanel', () => {
	it('lists each document with its tags and links to the detail page', async () => {
		searchStructuredRaw.mockResolvedValue(documents(1, [row()]));
		render(TagDocumentsPanel, { props: { tag: 'sample tag' } });

		await waitFor(() =>
			expect(searchStructuredRaw).toHaveBeenCalledWith({
				tags: ['sample tag'],
				limit: 100,
				offset: 0
			})
		);
		expect(screen.getByText(documentsLabel(1))).toBeTruthy();
		const link = screen.getByRole('link', { name: /First document/ });
		expect(link.getAttribute('href')).toBe('documents/doc-1');
		expect(screen.getByText('sample tag', { selector: 'span' })).toBeTruthy();
		expect(screen.getByText('another tag')).toBeTruthy();
	});

	it('shows an empty state when no live document carries the tag', async () => {
		searchStructuredRaw.mockResolvedValue(documents(0, []));
		render(TagDocumentsPanel, { props: { tag: 'sample tag' } });

		expect(await screen.findByText('No live document carries this tag right now.')).toBeTruthy();
		expect(screen.queryByRole('button', { name: /Load more/ })).toBeNull();
	});

	it('retries after a failed load', async () => {
		searchStructuredRaw.mockResolvedValueOnce({ ok: false, status: 500, data: null });
		render(TagDocumentsPanel, { props: { tag: 'sample tag' } });

		await fireEvent.click(await screen.findByRole('button', { name: 'Retry' }));

		expect(searchStructuredRaw).toHaveBeenCalledTimes(2);
		await waitFor(() =>
			expect(searchStructuredRaw).toHaveBeenLastCalledWith({
				tags: ['sample tag'],
				limit: 100,
				offset: 0
			})
		);
	});

	it('pages through more than one hundred documents', async () => {
		const first = Array.from({ length: 100 }, (_, i) => row({ id: `doc-${i}`, title: `Doc ${i}` }));
		const rest = [row({ id: 'doc-100', title: 'Doc 100' })];
		searchStructuredRaw
			.mockResolvedValueOnce(documents(101, first))
			.mockResolvedValueOnce(documents(101, rest));
		render(TagDocumentsPanel, { props: { tag: 'sample tag' } });

		await screen.findByText('Doc 99');
		expect(searchStructuredRaw).toHaveBeenCalledTimes(1);
		await fireEvent.click(screen.getByRole('button', { name: /Load more \(1 remaining\)/ }));
		await waitFor(() => expect(searchStructuredRaw).toHaveBeenCalledTimes(2));

		await waitFor(() =>
			expect(searchStructuredRaw).toHaveBeenCalledWith({
				tags: ['sample tag'],
				limit: 100,
				offset: 100
			})
		);
		expect(await screen.findByText('Doc 100')).toBeTruthy();
		expect(screen.queryByRole('button', { name: /Load more/ })).toBeNull();
	});
});
