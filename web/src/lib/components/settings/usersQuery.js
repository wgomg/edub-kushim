import { DEFAULT_PAGE_SIZES, parseView, serializeView } from '#lib/list/listQuery.svelte.js';

const defaults = {
	page: 0,
	pageSize: 25,
	sortBy: 'username',
	sortOrder: 'desc'
};

/** Codec for the users list view state inside the settings page. */
export const usersQuery = {
	keys: ['users_page', 'users_size', 'users_sort', 'users_order'],
	defaults,
	parse(searchParams) {
		return parseView(searchParams, {
			prefix: 'users',
			pageSizes: DEFAULT_PAGE_SIZES,
			defaultPageSize: defaults.pageSize,
			defaultSortBy: defaults.sortBy,
			defaultSortOrder: defaults.sortOrder
		});
	},
	serialize(state) {
		return serializeView(state, { prefix: 'users', defaults });
	}
};
