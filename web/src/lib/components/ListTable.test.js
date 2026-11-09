import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import { SvelteSet } from 'svelte/reactivity';

vi.mock('$app/navigation', () => ({ goto: vi.fn() }));
vi.mock('$app/paths', () => ({ resolve: (path) => path }));
vi.mock('$app/state', () => ({ page: { url: new URL('http://localhost/') } }));

import ListTable from './ListTable.svelte';

const columns = [
	{ key: 'name', label: 'Name', sortable: true },
	{ key: 'status', label: 'Status', sortable: false }
];

const rows = [
	{ id: 1, name: 'alpha', status: 'ok' },
	{ id: 2, name: 'beta', status: 'failed' }
];

describe('ListTable', () => {
	it('renders default cell values for every row', () => {
		render(ListTable, { columns, rows, total: 2 });
		expect(screen.getByText('alpha')).toBeTruthy();
		expect(screen.getByText('beta')).toBeTruthy();
		expect(screen.getByText('ok')).toBeTruthy();
		expect(screen.getByText('failed')).toBeTruthy();
	});

	it('asks the route to sort when a sortable header is clicked', async () => {
		const onSort = vi.fn();
		render(ListTable, { columns, rows, total: 2, onSort });
		await fireEvent.click(screen.getByText('Name'));
		expect(onSort).toHaveBeenCalledWith('name');
	});

	it('enables Next while another page exists and moves the page on click', async () => {
		const onPage = vi.fn();
		render(ListTable, {
			columns,
			rows: Array.from({ length: 10 }, (_, i) => ({ id: i, name: `n${i}`, status: 'ok' })),
			total: 25,
			onPage
		});
		expect(screen.getByText(/Showing 1.+10 of 25/)).toBeTruthy();
		expect(screen.getByRole('button', { name: 'Prev' }).disabled).toBe(true);
		expect(screen.getByRole('button', { name: 'Next' }).disabled).toBe(false);
		await fireEvent.click(screen.getByRole('button', { name: 'Next' }));
		expect(onPage).toHaveBeenCalledWith(1);
	});

	it('disables Next on the last page', () => {
		render(ListTable, {
			columns,
			rows: Array.from({ length: 10 }, (_, i) => ({ id: i, name: `n${i}`, status: 'ok' })),
			total: 10
		});
		expect(screen.getByRole('button', { name: 'Next' }).disabled).toBe(true);
	});

	it('shows the empty state when the endpoint returns nothing', () => {
		render(ListTable, { columns, rows: [], total: 0 });
		expect(screen.getByText('No results found.')).toBeTruthy();
	});

	it('shows the loading state instead of rows while fetching', () => {
		render(ListTable, { columns, rows: [], total: null, loading: true });
		expect(screen.getByText('Loading…')).toBeTruthy();
		expect(screen.queryByText('No results found.')).toBeNull();
	});

	it('reports selection through the row and select-all checkboxes', async () => {
		const onToggleRow = vi.fn();
		const onToggleAll = vi.fn();
		render(ListTable, {
			columns,
			rows,
			total: 2,
			selectedKeys: new SvelteSet(),
			onToggleRow,
			onToggleAll
		});

		await fireEvent.click(screen.getByLabelText('Select row 1'));
		expect(onToggleRow).toHaveBeenCalledWith(expect.objectContaining({ id: 1 }));

		await fireEvent.click(screen.getByLabelText('Select all'));
		expect(onToggleAll).toHaveBeenCalledTimes(1);
	});
});
