import { DEFAULT_PAGE_SIZES, parseView, serializeView } from '#lib/list/listQuery.svelte.js';

const defaults = {
	page: 0,
	pageSize: 25,
	sortBy: 'deleted_at',
	sortOrder: 'desc'
};

/** Codec for the trash list view state; the list has no filter parameters. */
export const trashQuery = {
	keys: ['trash_page', 'trash_size', 'trash_sort', 'trash_order'],
	defaults,
	parse(searchParams) {
		return parseView(searchParams, {
			prefix: 'trash',
			pageSizes: DEFAULT_PAGE_SIZES,
			defaultPageSize: defaults.pageSize,
			defaultSortBy: defaults.sortBy,
			defaultSortOrder: defaults.sortOrder
		});
	},
	serialize(state) {
		return serializeView(state, { prefix: 'trash', defaults });
	}
};
