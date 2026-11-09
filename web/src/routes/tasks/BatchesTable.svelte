<script>
	import { api } from '#lib/api.js';
	import Icon from '#lib/components/Icon.svelte';
	import ListTable from '#lib/components/ListTable.svelte';
	import {
		createListQuery,
		toggleSortState,
		writeRouteParams
	} from '#lib/list/listQuery.svelte.js';
	import { originBadgeClasses } from '#lib/utils/statusChip.js';
	import { confirmStore } from '#lib/stores/confirmStore.svelte.js';
	import * as authStore from '#lib/stores/authStore.svelte.js';
	import { batchesQuery } from './query.js';

	const RESUMABLE_BATCH_STATUSES = ['failed', 'completed', 'cancelled', 'paused'];

	// the task states the batch filter accepts; keep in sync with the vocabulary in internal/types/registry.go
	const TASK_STATES = [
		{ value: 'waiting', label: 'Waiting' },
		{ value: 'pending', label: 'Pending' },
		{ value: 'processing', label: 'Processing' },
		{ value: 'completed', label: 'Completed' },
		{ value: 'failed', label: 'Failed' },
		{ value: 'cancelled', label: 'Cancelled' },
		{ value: 'discarded', label: 'Discarded' }
	];

	async function fetchBatches({ limit, offset, query, signal }) {
		const data = await api.batches.list(query.taskStatus, limit, offset, signal);
		if (!data) throw new Error('Failed to load batches');
		return { items: data, total: null };
	}

	const batches = createListQuery({
		fetcher: fetchBatches,
		codec: batchesQuery,
		sortKeys: [
			'batch_id',
			'origin',
			'total',
			'completed',
			'waiting',
			'pending',
			'processing',
			'failed',
			'cancelled',
			'discarded'
		],
		keyField: 'batch_id'
	});

	const columns = [
		{ key: 'batch_id', label: 'Batch', sortable: true, width: '100%' },
		{ key: 'origin', label: 'Origin', sortable: true, minWidth: '110px' },
		{ key: 'total', label: 'Total', sortable: true, minWidth: '80px' },
		{ key: 'completed', label: 'Completed', sortable: true, minWidth: '100px' },
		{ key: 'waiting', label: 'Waiting', sortable: true, minWidth: '90px' },
		{ key: 'pending', label: 'Pending', sortable: true, minWidth: '90px' },
		{ key: 'processing', label: 'Processing', sortable: true, minWidth: '110px' },
		{ key: 'failed', label: 'Failed', sortable: true, minWidth: '80px' },
		{ key: 'cancelled', label: 'Cancelled', sortable: true, minWidth: '100px' },
		{ key: 'discarded', label: 'Discarded', sortable: true, minWidth: '100px' },
		{ key: 'owner_state', label: 'Owner', sortable: false, minWidth: '90px' },
		{ key: 'actions', label: 'Actions', sortable: false, cellClass: 'whitespace-nowrap' }
	];

	function sortByColumn(key) {
		batches.update(toggleSortState(batches.query, key));
	}

	function openBatch(row) {
		writeRouteParams(['batch'], [['batch', row.batch_id]]);
	}

	async function retryBatch(row) {
		await api.batches.resume(row.batch_id, true);
		batches.reload();
	}

	async function resumeBatch(row) {
		await api.batches.resume(row.batch_id);
		batches.reload();
	}

	async function cancelBatch(row) {
		const ok = await confirmStore.confirm({
			title: 'Cancel batch',
			message: `Cancel batch ${row.batch_id}? Queued tasks will be cancelled and running tasks stopped.`,
			danger: true
		});
		if (!ok) return;
		await api.batches.cancel(row.batch_id);
		batches.reload();
	}
</script>

{#snippet cell(col, row)}
	{#if col.key === 'batch_id'}
		<span class="font-mono">{row.batch_id ?? '—'}</span>
	{:else if col.key === 'origin'}
		<span
			class="inline-block rounded-full px-2 py-0.5 text-xs font-medium {originBadgeClasses(
				row.origin
			)}">{row.origin || '—'}</span
		>
		{#if row.schedule}
			<span class="ml-1 font-mono text-xs text-parchment-500">{row.schedule}</span>
		{/if}
	{:else if col.key === 'total'}
		<span class="font-semibold text-parchment-200">{row.total}</span>
	{:else if col.key === 'completed'}
		<span class="font-semibold text-emerald-500">{row.completed}</span>
	{:else if col.key === 'waiting'}
		<span class="font-semibold text-amber-400">{row.waiting}</span>
	{:else if col.key === 'pending'}
		<span class="font-semibold text-parchment-400">{row.pending}</span>
	{:else if col.key === 'processing'}
		<span class="font-semibold text-lapis-600">{row.processing}</span>
	{:else if col.key === 'failed'}
		<span class="font-semibold text-terracotta-500">{row.failed}</span>
	{:else if col.key === 'cancelled'}
		<span class="font-semibold text-parchment-500">{row.cancelled}</span>
	{:else if col.key === 'discarded'}
		<span class="font-semibold text-terracotta-400">{row.discarded}</span>
	{:else if col.key === 'owner_state'}
		{#if row.owner_state === 'live'}
			<span class="text-xs text-emerald-500">PID {row.owner_pid}</span>
		{:else if row.orphaned}
			<span class="text-xs text-amber-400">orphaned</span>
		{:else if row.owner_state === 'stale'}
			<span class="text-xs text-amber-400">stale</span>
		{:else}
			<span class="text-xs text-parchment-500">none</span>
		{/if}
	{:else if col.key === 'actions'}
		{#if !authStore.authEnabled() || authStore.isEditor()}
			<div class="flex items-center">
				{#if row.failed && RESUMABLE_BATCH_STATUSES.includes(row.status)}
					<button
						onclick={(e) => {
							e.stopPropagation();
							retryBatch(row);
						}}
						title="Retry"
						aria-label="Retry"
						class="rounded-md p-1.5 text-parchment-400 hover:bg-clay-800 hover:text-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
						><Icon name="retry" /></button
					>
				{/if}
				{#if row.status === 'paused'}
					<button
						onclick={(e) => {
							e.stopPropagation();
							resumeBatch(row);
						}}
						title="Resume"
						aria-label="Resume"
						class="rounded-md p-1.5 text-parchment-400 hover:bg-clay-800 hover:text-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
						><Icon name="resume" /></button
					>
				{/if}
				{#if row.pending || row.processing}
					<button
						onclick={(e) => {
							e.stopPropagation();
							cancelBatch(row);
						}}
						title="Cancel"
						aria-label="Cancel"
						class="rounded-md p-1.5 text-parchment-400 hover:bg-clay-800 hover:text-terracotta-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
						><Icon name="close" /></button
					>
				{/if}
			</div>
		{/if}
	{:else}
		{row[col.key] ?? ''}
	{/if}
{/snippet}

<div class="space-y-4">
	<div class="flex flex-wrap items-center justify-between gap-3">
		<h2 class="text-2xl font-semibold text-parchment-200">Batches</h2>
		<label for="batch-task-status" class="flex items-center gap-2 text-sm text-parchment-400">
			Task state
			<select
				id="batch-task-status"
				name="batch-task-status"
				value={batches.query.taskStatus}
				onchange={(event) => batches.update({ taskStatus: event.target.value })}
				class="rounded-md border border-clay-700 bg-clay-950 px-2 py-1 text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
			>
				<option value="">All</option>
				{#each TASK_STATES as state (state.value)}
					<option value={state.value}>{state.label}</option>
				{/each}
			</select>
		</label>
	</div>

	<ListTable
		{columns}
		{cell}
		rows={batches.rows}
		total={batches.total}
		loading={batches.loading}
		error={batches.error}
		page={batches.query.page}
		pageSize={batches.query.pageSize}
		pageSizes={[10, 25, 50, 100]}
		sortBy={batches.query.sortBy}
		sortOrder={batches.query.sortOrder}
		keyField="batch_id"
		onSort={sortByColumn}
		onPage={(next) => batches.update({ page: next })}
		onPageSize={(size) => batches.update({ pageSize: size })}
		onRowClick={openBatch}
	/>
</div>
