import { DEFAULT_PAGE_SIZES, parseView, serializeView } from '#lib/list/listQuery.svelte.js';

const defaults = {
	q: '',
	page: 0,
	pageSize: 25,
	sortBy: 'document_count',
	sortOrder: 'desc'
};

/**
 * Codec for the tag vocabulary URL slice. `q` is the free-text filter; the `tags_*` parameters are
 * the generic view state.
 */
export const tagsQuery = {
	keys: ['q', 'tags_page', 'tags_size', 'tags_sort', 'tags_order'],
	defaults,
	parse(searchParams) {
		return {
			q: searchParams.get('q') ?? '',
			...parseView(searchParams, {
				prefix: 'tags',
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
		entries.push(...serializeView(state, { prefix: 'tags', defaults }));
		return entries;
	}
};
