import { DEFAULT_PAGE_SIZES, parseView, serializeView } from '#lib/list/listQuery.svelte.js';

const defaults = {
	q: '',
	page: 0,
	pageSize: 50,
	sortBy: 'name',
	sortOrder: 'desc'
};

/**
 * Codec for the people list URL slice. `q` is the free-text filter; the `people_*` parameters are
 * the generic view state. Parsing is lenient: an invalid or absent value falls back to the defaults.
 */
export const peopleQuery = {
	keys: ['q', 'people_page', 'people_size', 'people_sort', 'people_order'],
	defaults,
	parse(searchParams) {
		return {
			q: searchParams.get('q') ?? '',
			...parseView(searchParams, {
				prefix: 'people',
				pageSizes: DEFAULT_PAGE_SIZES,
				defaultPageSize: defaults.pageSize,
				defaultSortBy: defaults.sortBy,
				defaultSortOrder: defaults.sortOrder
			})
		};
	},
	serialize(state) {
		const entries = [];
		if (state.q) entries.push(['q', state.q]);
		entries.push(...serializeView(state, { prefix: 'people', defaults }));
		return entries;
	}
};
