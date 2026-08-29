import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import DataTable from './DataTable.svelte';

vi.mock('$app/state', () => ({
	page: { url: { searchParams: new URLSearchParams(), pathname: '/documents' } }
}));
vi.mock('$app/navigation', () => ({ replaceState: vi.fn() }));
vi.mock('$app/paths', () => ({ resolve: (p) => p }));

const COLUMNS = [
	{ key: 'title', label: 'Title', sortable: true },
	{ key: 'size', label: 'Size', sortable: true }
];

const ROWS = [
	{ id: 1, title: 'Doc A', size: 10 },
	{ id: 2, title: 'Doc B', size: 20 }
];

function renderTable(fetch) {
	return render(DataTable, {
		props: { columns: COLUMNS, fetch, title: 'Documents' }
	});
}

describe('DataTable', () => {
	it('renders column headers and the fetched rows', async () => {
		const fetch = vi.fn().mockResolvedValue(ROWS);
		renderTable(fetch);

		expect(await screen.findByText('Doc A')).toBeTruthy();
		expect(screen.getByText('Doc B')).toBeTruthy();
		expect(screen.getByText('Title')).toBeTruthy();
		expect(screen.getByText('Size')).toBeTruthy();
		expect(fetch).toHaveBeenCalledWith({
			sortBy: 'title',
			sortOrder: 'desc',
			limit: 25,
			offset: 0
		});
	});

	it('reloads with the toggled sort on header click', async () => {
		const fetch = vi.fn().mockResolvedValue(ROWS);
		renderTable(fetch);
		await screen.findByText('Doc A');

		fireEvent.click(screen.getByText('Size'));
		expect(fetch).toHaveBeenLastCalledWith({
			sortBy: 'size',
			sortOrder: 'desc',
			limit: 25,
			offset: 0
		});

		fireEvent.click(screen.getByText('Size'));
		expect(fetch).toHaveBeenLastCalledWith({
			sortBy: 'size',
			sortOrder: 'asc',
			limit: 25,
			offset: 0
		});
	});

	it('shows the empty state when no rows come back', async () => {
		const fetch = vi.fn().mockResolvedValue([]);
		renderTable(fetch);
		expect(await screen.findByText('No results found.')).toBeTruthy();
	});

	it('surfaces the fetch error message', async () => {
		const fetch = vi.fn().mockRejectedValue(new Error('backend unreachable'));
		renderTable(fetch);
		expect(await screen.findByText('backend unreachable')).toBeTruthy();
	});
});
