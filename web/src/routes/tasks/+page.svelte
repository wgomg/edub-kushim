<script>
	import { page } from '$app/stores';
	import { goto } from '$app/navigation';
	import { api } from '$lib/api';
	import DataTable from '$lib/components/DataTable.svelte';

	function formatDate(v) {
		if (!v) return '—';
		return new Date(v).toLocaleString();
	}

	function statusBadge(status) {
		const colors = {
			waiting: 'bg-amber-600/20 text-amber-400',
			pending: 'bg-parchment-500/20 text-parchment-400',
			processing: 'bg-lapis-600/20 text-lapis-600',
			completed: 'bg-emerald-600/20 text-emerald-500',
			failed: 'bg-terracotta-600/20 text-terracotta-500',
			cancelled: 'bg-parchment-500/10 text-parchment-500',
			discarded: 'bg-terracotta-600/10 text-terracotta-400'
		};
		const cls = colors[status] ?? 'bg-parchment-500/10 text-parchment-500';
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
				return `<span class="font-mono">${v}</span>`;
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
							`<span class="text-parchment-400 text-xs">document:</span> <span class="font-mono text-parchment-300">${row.payload_doc_id}</span>`
						);
					}
				}
				if (row.file_name) {
					parts.push(
						`<span class="text-parchment-400 text-xs">file:</span> <span class="text-parchment-200">${row.file_name}</span>`
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
				return `<span title="${v}" class="text-terracotta-500">${short}</span>`;
			},
			minWidth: '200px'
		}
	];

	let currentBatch = $state('');

	page.subscribe(($p) => {
		currentBatch = $p.url.searchParams.get('batch') || '';
	});

	async function fetchBatches({ limit, offset }) {
		return await api.batches.list(limit, offset);
	}

	async function fetchTasks({ limit, offset }) {
		const result = await api.tasks.list(currentBatch, '', limit, offset);
		return result?.tasks ?? [];
	}

	function viewBatch(row) {
		goto(`/tasks?batch=${row.batch_id}`);
	}

	function viewTask(row) {
		goto(`/tasks/${row.task_id}`);
	}
</script>

{#if currentBatch}
	<div class="space-y-4">
		<a
			href="/tasks"
			class="inline-flex items-center gap-1 text-sm text-parchment-500 hover:text-parchment-200"
		>
			&larr; Back to batches
		</a>

		<DataTable
			columns={taskColumns}
			fetch={fetchTasks}
			onRowClick={viewTask}
			title={'Batch: ' + currentBatch}
			keyField="task_id"
		/>
	</div>
{:else}
	<DataTable
		columns={batchColumns}
		fetch={fetchBatches}
		onRowClick={viewBatch}
		title="Batches"
		keyField="batch_id"
	/>
{/if}
