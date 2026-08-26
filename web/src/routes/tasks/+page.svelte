<script>
	import { page } from '$app/stores';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { RETRY_ICON, RESUME_ICON, CANCEL_ICON, actionButton } from '$lib/icons.js';
	import { escapeHtml } from '$lib/utils/html.js';
	import { statusChipClasses } from '$lib/utils/statusChip.js';
	import { api } from '$lib/api';
	import DataTable from '$lib/components/DataTable.svelte';
	import { confirmStore } from '$lib/stores/confirmStore.svelte.js';
	import * as authStore from '$lib/stores/authStore.js';

	let taskRefreshKey = $state(0);
	let batchRefreshKey = $state(0);

	function formatDate(v) {
		if (!v) return '—';
		return new Date(v).toLocaleString();
	}

	function statusBadge(status) {
		const cls = statusChipClasses(status);
		return `<span class="inline-block rounded-full px-2.5 py-0.5 text-xs font-medium ${cls}">${status}</span>`;
	}

	const batchColumns = [
		{
			key: 'batch_id',
			label: 'Batch',
			sortable: true,
			width: '100%',
			cell: (v) => {
				if (!v) return '—';
				return `<span class="font-mono">${escapeHtml(v)}</span>`;
			}
		},
		{
			key: 'total',
			label: 'Total',
			sortable: true,
			cell: (v) => `<span class="font-semibold text-parchment-200">${v}</span>`,
			minWidth: '80px'
		},
		{
			key: 'completed',
			label: 'Completed',
			sortable: true,
			cell: (v) => `<span class="font-semibold text-emerald-500">${v}</span>`,
			minWidth: '100px'
		},
		{
			key: 'waiting',
			label: 'Waiting',
			sortable: true,
			cell: (v) => `<span class="font-semibold text-amber-400">${v}</span>`,
			minWidth: '90px'
		},
		{
			key: 'pending',
			label: 'Pending',
			sortable: true,
			cell: (v) => `<span class="font-semibold text-parchment-400">${v}</span>`,
			minWidth: '90px'
		},
		{
			key: 'processing',
			label: 'Processing',
			sortable: true,
			cell: (v) => `<span class="font-semibold text-lapis-600">${v}</span>`,
			minWidth: '110px'
		},
		{
			key: 'failed',
			label: 'Failed',
			sortable: true,
			cell: (v) => `<span class="font-semibold text-terracotta-500">${v}</span>`,
			minWidth: '80px'
		},
		{
			key: 'cancelled',
			label: 'Cancelled',
			sortable: true,
			cell: (v) => `<span class="font-semibold text-parchment-500">${v}</span>`,
			minWidth: '100px'
		},
		{
			key: 'discarded',
			label: 'Discarded',
			sortable: true,
			cell: (v) => `<span class="font-semibold text-terracotta-400">${v}</span>`,
			minWidth: '100px'
		},
		{
			key: 'owner_state',
			label: 'Owner',
			sortable: false,
			cell: (v, row) => {
				if (v === 'live')
					return `<span class="text-emerald-500 text-xs">PID ${row.owner_pid}</span>`;
				if (row.orphaned) return `<span class="text-amber-400 text-xs">orphaned</span>`;
				if (v === 'stale') return `<span class="text-amber-400 text-xs">stale</span>`;
				return '<span class="text-parchment-500 text-xs">none</span>';
			},
			minWidth: '90px'
		},
		{
			key: 'actions',
			label: 'Actions',
			sortable: false,
			cellClass: 'whitespace-nowrap',
			cell: (_v, row) => {
				if (authStore.authEnabled() && !authStore.isEditor()) return '';
				let buttons = '';
				if (row.failed) {
					buttons += actionButton(RETRY_ICON, 'Retry', 'text-parchment-400 hover:text-gold-500', {
						'data-retry-batch': row.batch_id
					});
				}
				if (row.orphaned) {
					buttons += actionButton(RESUME_ICON, 'Resume', 'text-parchment-400 hover:text-gold-500', {
						'data-resume-batch': row.batch_id
					});
				}
				if (row.pending || row.processing) {
					buttons += actionButton(
						CANCEL_ICON,
						'Cancel',
						'text-parchment-400 hover:text-terracotta-500',
						{ 'data-cancel-batch': row.batch_id }
					);
				}
				return buttons;
			}
		}
	];

	const taskColumns = [
		{
			key: 'task_type',
			label: 'Type',
			sortable: true,
			minWidth: '100px'
		},
		{
			key: 'payload',
			label: 'Payload',
			sortable: false,
			width: '100%',
			cell: (_v, row) => {
				const parts = [];
				if (
					row.task_type === 'enrich' ||
					(row.task_type === 'consume' && row.status === 'completed')
				) {
					if (row.payload_doc_id) {
						parts.push(
							`<span class="text-parchment-400 text-xs">document:</span> <span class="font-mono text-parchment-300">${escapeHtml(row.payload_doc_id)}</span>`
						);
					}
				}
				if (row.file_name) {
					parts.push(
						`<span class="text-parchment-400 text-xs">file:</span> <span class="text-parchment-200 break-all" title="${escapeHtml(row.file_name)}">${escapeHtml(row.file_name)}</span>`
					);
				} else {
					parts.push(`<span class="text-parchment-500 italic">no file</span>`);
				}
				return parts.join('<br>') || '<span class="text-parchment-500 italic">—</span>';
			}
		},
		{
			key: 'status',
			label: 'Status',
			sortable: true,
			cell: (v) => statusBadge(v),
			minWidth: '120px'
		},
		{
			key: 'created_at',
			label: 'Created',
			sortable: true,
			cell: (v) => formatDate(v),
			minWidth: '160px'
		},
		{
			key: 'started_at',
			label: 'Started',
			sortable: true,
			cell: (v) => formatDate(v),
			minWidth: '160px'
		},
		{
			key: 'completed_at',
			label: 'Completed',
			sortable: true,
			cell: (v) => formatDate(v),
			minWidth: '160px'
		},
		{
			key: 'error',
			label: 'Error',
			cell: (v) => {
				if (!v) return '';
				const short = v.length > 40 ? v.slice(0, 40) + '…' : v;
				return `<span title="${escapeHtml(v)}" class="text-terracotta-500">${escapeHtml(short)}</span>`;
			},
			minWidth: '200px'
		},
		{
			key: 'actions',
			label: 'Actions',
			sortable: false,
			cellClass: 'whitespace-nowrap',
			cell: (_v, row) => {
				if (authStore.authEnabled() && !authStore.isEditor()) return '';
				if (row.status !== 'failed') return '';
				return actionButton(RETRY_ICON, 'Retry', 'text-parchment-400 hover:text-gold-500', {
					'data-retry-task': row.task_id
				});
			}
		}
	];

	let currentBatch = $state('');
	let currentStatus = $state('');

	page.subscribe(($p) => {
		currentBatch = $p.url.searchParams.get('batch') || '';
		currentStatus = $p.url.searchParams.get('status') || '';
	});

	async function fetchBatches({ limit, offset }) {
		return await api.batches.list(limit, offset);
	}

	async function fetchTasks({ limit, offset }) {
		const result = await api.tasks.list(currentBatch, currentStatus, limit, offset);
		return result?.tasks ?? [];
	}

	function viewBatch(row) {
		goto(resolve(`/tasks?batch=${row.batch_id}`));
	}

	function viewTask(row) {
		goto(resolve(`/tasks/${row.task_id}`));
	}

	function handlePageClick(e) {
		const taskBtn = e.target.closest('[data-retry-task]');
		if (taskBtn) {
			e.stopPropagation();
			const taskId = taskBtn.getAttribute('data-retry-task');
			api.tasks.retry(taskId).then(() => {
				taskRefreshKey++;
			});
			return;
		}
		const batchBtn = e.target.closest('[data-retry-batch]');
		if (batchBtn) {
			e.stopPropagation();
			const batchId = batchBtn.getAttribute('data-retry-batch');
			api.batches.retry(batchId).then(() => {
				batchRefreshKey++;
			});
			return;
		}
		const resumeBtn = e.target.closest('[data-resume-batch]');
		if (resumeBtn) {
			e.stopPropagation();
			const batchId = resumeBtn.getAttribute('data-resume-batch');
			api.batches.resume(batchId).then(() => {
				batchRefreshKey++;
			});
			return;
		}
		const cancelBtn = e.target.closest('[data-cancel-batch]');
		if (cancelBtn) {
			e.stopPropagation();
			const batchId = cancelBtn.getAttribute('data-cancel-batch');
			confirmStore
				.confirm({
					title: 'Cancel batch',
					message: `Cancel batch ${batchId}? Queued tasks will be cancelled and running tasks stopped.`,
					danger: true
				})
				.then((ok) => {
					if (!ok) return;
					api.batches.cancel(batchId).then(() => {
						batchRefreshKey++;
					});
				});
			return;
		}
	}
</script>

<svelte:window onclick={handlePageClick} />

{#if currentBatch || currentStatus}
	<div class="space-y-4">
		<a
			href={resolve(`/tasks`)}
			class="inline-flex items-center gap-1 text-sm text-parchment-500 hover:text-parchment-200"
		>
			&larr; Back to batches
		</a>

		<DataTable
			columns={taskColumns}
			fetch={fetchTasks}
			onRowClick={viewTask}
			title={currentStatus === 'active'
				? 'Active Tasks'
				: currentBatch
					? 'Batch: ' + currentBatch
					: 'Tasks (' + currentStatus + ')'}
			keyField="task_id"
			refreshKey={taskRefreshKey}
			urlSync="tasks"
		/>
	</div>
{:else}
	<DataTable
		columns={batchColumns}
		fetch={fetchBatches}
		onRowClick={viewBatch}
		title="Batches"
		keyField="batch_id"
		refreshKey={batchRefreshKey}
		urlSync="batches"
	/>
{/if}
