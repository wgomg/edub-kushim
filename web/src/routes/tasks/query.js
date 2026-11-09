import { DEFAULT_PAGE_SIZES, parseView, serializeView } from '#lib/list/listQuery.svelte.js';

/**
 * Codec for the task list slice. `batch` and `status` scope the list; the `tasks_*` parameters are
 * its view state. `batch` is written by the batches table when a row is opened.
 */
export const tasksQuery = {
	keys: ['batch', 'status', 'tasks_page', 'tasks_size', 'tasks_sort', 'tasks_order'],
	defaults: {
		batch: '',
		status: '',
		page: 0,
		pageSize: 25,
		sortBy: 'task_type',
		sortOrder: 'desc'
	},
	parse(searchParams) {
		return {
			batch: searchParams.get('batch') ?? '',
			status: searchParams.get('status') ?? '',
			...parseView(searchParams, {
				prefix: 'tasks',
				pageSizes: DEFAULT_PAGE_SIZES,
				defaultPageSize: 25,
				defaultSortBy: 'task_type',
				defaultSortOrder: 'desc'
			})
		};
	},
	serialize(state) {
		const entries = [];
		if (state.batch) entries.push(['batch', state.batch]);
		if (state.status) entries.push(['status', state.status]);
		entries.push(
			...serializeView(state, {
				prefix: 'tasks',
				defaults: { pageSize: 25, sortBy: 'task_type', sortOrder: 'desc' }
			})
		);
		return entries;
	}
};

/** Codec for the batches list slice; `task_status` filters it. */
export const batchesQuery = {
	keys: ['task_status', 'batches_page', 'batches_size', 'batches_sort', 'batches_order'],
	defaults: {
		taskStatus: '',
		page: 0,
		pageSize: 25,
		sortBy: 'batch_id',
		sortOrder: 'desc'
	},
	parse(searchParams) {
		return {
			taskStatus: searchParams.get('task_status') ?? '',
			...parseView(searchParams, {
				prefix: 'batches',
				pageSizes: DEFAULT_PAGE_SIZES,
				defaultPageSize: 25,
				defaultSortBy: 'batch_id',
				defaultSortOrder: 'desc'
			})
		};
	},
	serialize(state) {
		const entries = [];
		if (state.taskStatus) entries.push(['task_status', state.taskStatus]);
		entries.push(
			...serializeView(state, {
				prefix: 'batches',
				defaults: { pageSize: 25, sortBy: 'batch_id', sortOrder: 'desc' }
			})
		);
		return entries;
	}
};
