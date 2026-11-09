<script>
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { api } from '#lib/api.js';
	import Icon from '#lib/components/Icon.svelte';
	import ListTable from '#lib/components/ListTable.svelte';
	import { createListQuery, toggleSortState } from '#lib/list/listQuery.svelte.js';
	import { statusChipClasses } from '#lib/utils/statusChip.js';
	import { formatDateTime } from '#lib/utils/html.js';
	import * as authStore from '#lib/stores/authStore.svelte.js';
	import { tasksQuery } from './query.js';

	async function fetchTasks({ limit, offset, query, signal }) {
		const data = await api.tasks.list(query.batch, query.status, limit, offset, signal);
		if (!data) throw new Error('Failed to load tasks');
		return { items: data.tasks ?? [], total: null };
	}

	const tasks = createListQuery({
		fetcher: fetchTasks,
		codec: tasksQuery,
		sortKeys: ['task_type', 'status', 'created_at', 'started_at', 'completed_at'],
		keyField: 'task_id'
	});

	const title = $derived(
		tasks.query.status === 'active'
			? 'Active Tasks'
			: tasks.query.batch
				? 'Batch: ' + tasks.query.batch
				: 'Tasks (' + tasks.query.status + ')'
	);

	const columns = [
		{ key: 'task_type', label: 'Type', sortable: true, minWidth: '100px' },
		{ key: 'payload', label: 'Payload', sortable: false, width: '100%' },
		{ key: 'status', label: 'Status', sortable: true, minWidth: '120px' },
		{ key: 'created_at', label: 'Created', sortable: true, minWidth: '160px' },
		{ key: 'started_at', label: 'Started', sortable: true, minWidth: '160px' },
		{ key: 'completed_at', label: 'Completed', sortable: true, minWidth: '160px' },
		{ key: 'error', label: 'Error', minWidth: '200px' },
		{ key: 'actions', label: 'Actions', sortable: false, cellClass: 'whitespace-nowrap' }
	];

	function sortByColumn(key) {
		tasks.update(toggleSortState(tasks.query, key));
	}

	function viewTask(row) {
		goto(resolve(`tasks/${row.task_id}`));
	}

	async function retryTask(row) {
		await api.batches.resume(row.batch_id, true);
		tasks.reload();
	}
</script>

{#snippet cell(col, row)}
	{#if col.key === 'payload'}
		<div class="space-y-0.5">
			{#if row.payload_doc_id}
				<div>
					<span class="text-xs text-parchment-400">document:</span>
					<a
						href={resolve(`documents/${row.payload_doc_id}`)}
						class="font-mono text-lapis-400 hover:text-lapis-300">{row.payload_doc_id}</a
					>
				</div>
			{/if}
			{#if row.file_name}
				<div>
					<span class="text-xs text-parchment-400">file:</span>
					<span class="break-all text-parchment-200" title={row.file_name}>{row.file_name}</span>
				</div>
			{:else}
				<div><span class="text-parchment-500 italic">no file</span></div>
			{/if}
		</div>
	{:else if col.key === 'status'}
		<span
			class="inline-block rounded-full px-2.5 py-0.5 text-xs font-medium {statusChipClasses(
				row.status
			)}">{row.status}</span
		>
	{:else if col.key === 'created_at' || col.key === 'started_at' || col.key === 'completed_at'}
		{formatDateTime(row[col.key])}
	{:else if col.key === 'error'}
		{#if row.error}
			<div class="flex items-center gap-1">
				<span class="max-w-[200px] truncate text-terracotta-500" title={row.error}>{row.error}</span
				>
			</div>
		{/if}
	{:else if col.key === 'actions'}
		{#if (!authStore.authEnabled() || authStore.isEditor()) && row.status === 'failed'}
			<button
				onclick={(event) => {
					event.stopPropagation();
					retryTask(row);
				}}
				title="Retry"
				aria-label="Retry"
				class="rounded-md p-1.5 text-parchment-400 hover:bg-clay-800 hover:text-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
				><Icon name="retry" /></button
			>
		{/if}
	{:else}
		{row[col.key] ?? ''}
	{/if}
{/snippet}

<div class="space-y-4">
	<a
		href={resolve('tasks')}
		class="inline-flex items-center gap-1 text-sm text-parchment-500 hover:text-parchment-200"
		>← Back to batches</a
	>

	<ListTable
		{columns}
		{cell}
		rows={tasks.rows}
		total={tasks.total}
		loading={tasks.loading}
		error={tasks.error}
		page={tasks.query.page}
		pageSize={tasks.query.pageSize}
		pageSizes={[10, 25, 50, 100]}
		sortBy={tasks.query.sortBy}
		sortOrder={tasks.query.sortOrder}
		keyField="task_id"
		{title}
		onSort={sortByColumn}
		onPage={(next) => tasks.update({ page: next })}
		onPageSize={(size) => tasks.update({ pageSize: size })}
		onRowClick={viewTask}
	/>
</div>
