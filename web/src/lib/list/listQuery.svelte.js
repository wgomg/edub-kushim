import { goto } from '$app/navigation';
import { resolve } from '$app/paths';
import { page } from '$app/state';
import { untrack } from 'svelte';
import { SvelteSet, SvelteURLSearchParams } from 'svelte/reactivity';

/** The page-size options every list offers unless a route deliberately differs. */
export const DEFAULT_PAGE_SIZES = [10, 25, 50, 100];

/**
 * The list-state module: one headless controller per row list plus the single URL writer every route
 * uses. The URL is the source of truth in URL mode; memory mode keeps the same shape in component
 * state for lists that do not persist. The controller hides fetching, request ordering, clamping,
 * selection and URL serialization, so routes only supply a fetcher, a codec, and the view.
 */

function parsePositiveInt(raw) {
	const parsed = parseInt(raw ?? '', 10);
	return Number.isInteger(parsed) && parsed >= 0 ? parsed : null;
}

/**
 * Reads the generic view state for one list slice from the URL. Invalid or absent values fall back to
 * the defaults, so a stale or hand-edited link can never produce a bad page or page size.
 */
export function parseView(
	searchParams,
	{
		prefix,
		pageSizes = DEFAULT_PAGE_SIZES,
		defaultPageSize = 25,
		defaultSortBy = '',
		defaultSortOrder = 'desc'
	}
) {
	const size = parsePositiveInt(searchParams.get(`${prefix}_size`));
	const pageNumber = parsePositiveInt(searchParams.get(`${prefix}_page`));
	const order = searchParams.get(`${prefix}_order`);

	return {
		page: pageNumber ?? 0,
		pageSize: size != null && pageSizes.includes(size) ? size : defaultPageSize,
		sortBy: searchParams.get(`${prefix}_sort`) || defaultSortBy,
		sortOrder: order === 'asc' || order === 'desc' ? order : defaultSortOrder
	};
}

/**
 * Serializes the view state for one slice. Default values are omitted so the URL only carries what
 * the user actually changed.
 */
export function serializeView(state, { prefix, defaults }) {
	const entries = [];
	if (state.page > 0) entries.push([`${prefix}_page`, String(state.page)]);
	if (state.pageSize !== defaults.pageSize)
		entries.push([`${prefix}_size`, String(state.pageSize)]);
	if (state.sortBy !== defaults.sortBy) entries.push([`${prefix}_sort`, state.sortBy]);
	if (state.sortOrder !== defaults.sortOrder) entries.push([`${prefix}_order`, state.sortOrder]);
	return entries;
}

/**
 * The URL the address bar shows. A shallow `goto` updates only `page.shallow`, while `page.url`
 * keeps describing the page that is actually rendered, so every read and write of list state must
 * go through this accessor.
 */
export function effectiveUrl() {
	return page.shallow?.url ?? page.url;
}

/**
 * Rewrites the given parameter keys on the current route in a single shallow navigation, preserving
 * every other parameter and the URL hash. This is the only code allowed to write list state into the
 * URL.
 */
export function writeRouteParams(keys, entries) {
	const current = effectiveUrl();
	const next = new SvelteURLSearchParams(current.searchParams);

	for (const key of keys) next.delete(key);
	for (const [key, value] of entries) {
		if (value !== '' && value != null) next.append(key, String(value));
	}

	const search = next.toString();
	if (search === current.searchParams.toString()) return;

	const query = search ? `?${search}` : '';
	goto(resolve(`${current.pathname}${query}${current.hash}`), { shallow: true, replace: true });
}

/**
 * The sort patch a header click produces: the same column flips direction, a different column starts
 * descending. One definition so every list agrees on what a click means.
 */
export function toggleSortState(state, key) {
	const sortOrder = state.sortBy === key ? (state.sortOrder === 'asc' ? 'desc' : 'asc') : 'desc';
	return { sortBy: key, sortOrder };
}

/**
 * Pagination facts shared by the presentational views: whether another page exists and which row
 * range the footer should report.
 */
export function listWindow({ page, pageSize, rowCount, total }) {
	const hasMore = total != null ? page * pageSize + rowCount < total : rowCount >= pageSize;
	return {
		hasMore,
		rangeStart: page * pageSize + 1,
		rangeEnd: page * pageSize + rowCount
	};
}

function normalizeData(result) {
	if (Array.isArray(result)) return { items: result, total: null };
	if (result && Array.isArray(result.items)) return result;
	return { items: [], total: null };
}

/**
 * Creates the controller for one list.
 *
 * With `codec` the query lives in the URL: `codec.parse(searchParams)` reads it and
 * `codec.serialize(state)` writes it through `writeRouteParams`. Without `codec` the same state lives
 * in memory. `defaults` seeds memory mode.
 *
 * The fetcher receives `{ limit, offset, sortBy, sortOrder, query, signal }` and must return
 * `{ items, total, ...extras }` (`total` may be null when the endpoint has no count). It must not
 * close over query state: the parsed query is always passed in `query`.
 */
export function createListQuery({
	fetcher,
	codec = null,
	defaults = {},
	sortKeys = [],
	keyField = 'id'
}) {
	const baseDefaults = { page: 0, pageSize: 25, sortBy: '', sortOrder: 'desc', ...defaults };
	const ownedKeys = codec ? codec.keys : [];

	let memoryState = $state({ ...baseDefaults });
	let data = $state(null);
	let loading = $state(true);
	let error = $state('');

	let generation = $state(0);
	let pendingSilent = false;
	let lastKey = '';
	let lastGeneration = -1;

	let sequence = 0;
	let inflight = null;

	const selectedKeys = new SvelteSet();

	const current = $derived.by(() => {
		const state = codec ? codec.parse(effectiveUrl().searchParams) : memoryState;
		if (sortKeys.length > 0 && !sortKeys.includes(state.sortBy)) {
			return { ...state, sortBy: baseDefaults.sortBy, sortOrder: baseDefaults.sortOrder };
		}
		return state;
	});

	const rows = $derived(data?.items ?? []);
	const total = $derived(data?.total ?? null);
	const selectedRows = $derived(rows.filter((row) => selectedKeys.has(row[keyField])));

	function writeState(next) {
		if (codec) writeRouteParams(ownedKeys, codec.serialize(next));
		else memoryState = next;
	}

	/**
	 * Merges a patch into the query. Any change other than a page move resets pagination to the first
	 * page.
	 */
	function update(patch) {
		const next = { ...current, ...patch };
		const pageOnly = Object.keys(patch).length === 1 && 'page' in patch;
		if (!pageOnly) next.page = 0;
		writeState(next);
	}

	/** Refetches the current page; `silent` keeps the current data and loader state. */
	function reload({ silent = false } = {}) {
		pendingSilent = silent;
		generation++;
	}

	function toggle(row) {
		const key = row[keyField];
		if (selectedKeys.has(key)) selectedKeys.delete(key);
		else selectedKeys.add(key);
	}
	function toggleAll() {
		const allSelected = rows.length > 0 && rows.every((row) => selectedKeys.has(row[keyField]));
		if (allSelected) {
			for (const row of rows) selectedKeys.delete(row[keyField]);
		} else {
			for (const row of rows) selectedKeys.add(row[keyField]);
		}
	}

	async function run(state, silent) {
		const mySequence = ++sequence;
		if (inflight) inflight.abort();
		const controller = new AbortController();
		inflight = controller;

		if (!silent) {
			loading = true;
			error = '';
			selectedKeys.clear();
		}

		try {
			const result = await fetcher({
				limit: state.pageSize,
				offset: state.page * state.pageSize,
				sortBy: state.sortBy,
				sortOrder: state.sortOrder,
				query: state,
				signal: controller.signal
			});
			if (mySequence !== sequence) return;

			const normalized = normalizeData(result);
			data = normalized;

			const count = normalized.total;
			if (count != null && state.page > 0 && state.page * state.pageSize >= count) {
				const lastPage = Math.max(0, Math.ceil(count / state.pageSize) - 1);
				if (lastPage !== state.page) update({ page: lastPage });
			}
		} catch (err) {
			if (mySequence !== sequence) return;
			if (err?.name === 'AbortError') return;
			if (silent) return;
			data = null;
			error = err instanceof Error ? err.message : 'Failed to load data';
		} finally {
			if (mySequence === sequence) {
				inflight = null;
				if (!silent) loading = false;
			}
		}
	}

	$effect(() => {
		const state = current;
		const generationValue = generation;
		const key = JSON.stringify(state);
		if (key === lastKey && generationValue === lastGeneration) return;

		lastKey = key;
		lastGeneration = generationValue;
		const silent = pendingSilent;
		pendingSilent = false;

		untrack(() => run(state, silent));
	});

	return {
		get data() {
			return data;
		},
		get rows() {
			return rows;
		},
		get total() {
			return total;
		},
		get loading() {
			return loading;
		},
		get error() {
			return error;
		},
		get query() {
			return current;
		},
		get selectedKeys() {
			return selectedKeys;
		},
		get selectedRows() {
			return selectedRows;
		},
		update,
		reload,
		toggle,
		toggleAll
	};
}
