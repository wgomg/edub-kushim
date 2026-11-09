import { beforeEach, describe, it, expect, vi } from 'vitest';

const pageMock = vi.hoisted(() => ({
	current: { url: new URL('http://localhost/rendered') }
}));

vi.mock('$app/navigation', () => ({ goto: vi.fn() }));
vi.mock('$app/paths', () => ({ resolve: (path) => path }));
vi.mock('$app/state', () => ({ page: pageMock.current }));

import { goto } from '$app/navigation';
import {
	effectiveUrl,
	listWindow,
	parseView,
	serializeView,
	toggleSortState,
	writeRouteParams
} from './listQuery.svelte.js';

describe('effectiveUrl', () => {
	it('prefers the shallow target because a shallow goto leaves page.url on the rendered page', () => {
		pageMock.current.url = new URL('http://localhost/documents');
		delete pageMock.current.shallow;
		expect(effectiveUrl()).toBe(pageMock.current.url);

		const target = new URL('http://localhost/documents?q=annual');
		pageMock.current.shallow = { url: target };
		expect(effectiveUrl()).toBe(target);
		delete pageMock.current.shallow;
	});
});

describe('writeRouteParams', () => {
	beforeEach(() => {
		vi.mocked(goto).mockClear();
		pageMock.current.url = new URL('http://localhost/documents');
		pageMock.current.shallow = { url: new URL('http://localhost/documents?tag=finance') };
	});

	it('writes from the shallow target so earlier writes are preserved', () => {
		writeRouteParams(
			['tag', 'q'],
			[
				['tag', 'finance'],
				['q', 'annual']
			]
		);
		expect(goto).toHaveBeenLastCalledWith('/documents?tag=finance&q=annual', {
			shallow: true,
			replace: true
		});
	});

	it('removes a parameter that exists only in the shallow target', () => {
		writeRouteParams(['tag'], []);
		expect(goto).toHaveBeenLastCalledWith('/documents', { shallow: true, replace: true });
	});

	it('preserves the hash of the shallow target', () => {
		pageMock.current.shallow = { url: new URL('http://localhost/tags?a=b#provisional') };
		writeRouteParams(['q'], [['q', 'x']]);
		expect(goto).toHaveBeenLastCalledWith('/tags?a=b&q=x#provisional', {
			shallow: true,
			replace: true
		});
	});
});

describe('toggleSortState', () => {
	it('flips the direction of the active column', () => {
		expect(toggleSortState({ sortBy: 'title', sortOrder: 'asc' }, 'title')).toEqual({
			sortBy: 'title',
			sortOrder: 'desc'
		});
		expect(toggleSortState({ sortBy: 'title', sortOrder: 'desc' }, 'title')).toEqual({
			sortBy: 'title',
			sortOrder: 'asc'
		});
	});

	it('starts a different column descending', () => {
		expect(toggleSortState({ sortBy: 'title', sortOrder: 'asc' }, 'created_at')).toEqual({
			sortBy: 'created_at',
			sortOrder: 'desc'
		});
	});
});

describe('listWindow', () => {
	it('reports the shown range and whether another page exists', () => {
		expect(listWindow({ page: 1, pageSize: 10, rowCount: 10, total: 25 })).toEqual({
			hasMore: true,
			rangeStart: 11,
			rangeEnd: 20
		});
		expect(listWindow({ page: 2, pageSize: 10, rowCount: 5, total: 25 })).toEqual({
			hasMore: false,
			rangeStart: 21,
			rangeEnd: 25
		});
	});

	it('falls back to a full-page heuristic when the endpoint has no count', () => {
		expect(listWindow({ page: 0, pageSize: 10, rowCount: 10, total: null }).hasMore).toBe(true);
		expect(listWindow({ page: 0, pageSize: 10, rowCount: 9, total: null }).hasMore).toBe(false);
	});
});

describe('parseView', () => {
	const config = {
		prefix: 'x',
		pageSizes: [10, 25],
		defaultPageSize: 25,
		defaultSortBy: 'created_at',
		defaultSortOrder: 'desc'
	};

	it('keeps valid values', () => {
		expect(
			parseView(new URLSearchParams('x_page=2&x_size=10&x_sort=title&x_order=asc'), config)
		).toEqual({
			page: 2,
			pageSize: 10,
			sortBy: 'title',
			sortOrder: 'asc'
		});
	});

	it('falls back per key for invalid or absent values', () => {
		expect(parseView(new URLSearchParams('x_page=-1&x_size=999&x_order=sideways'), config)).toEqual(
			{
				page: 0,
				pageSize: 25,
				sortBy: 'created_at',
				sortOrder: 'desc'
			}
		);
	});
});

describe('serializeView', () => {
	const defaults = { pageSize: 25, sortBy: 'created_at', sortOrder: 'desc' };

	it('omits values that equal the defaults', () => {
		const state = { page: 0, pageSize: 25, sortBy: 'created_at', sortOrder: 'desc' };
		expect(serializeView(state, { prefix: 'x', defaults })).toEqual([]);
	});

	it('emits only the changed values with the slice prefix', () => {
		const state = { page: 2, pageSize: 10, sortBy: 'title', sortOrder: 'asc' };
		expect(serializeView(state, { prefix: 'x', defaults })).toEqual([
			['x_page', '2'],
			['x_size', '10'],
			['x_sort', 'title'],
			['x_order', 'asc']
		]);
	});
});
