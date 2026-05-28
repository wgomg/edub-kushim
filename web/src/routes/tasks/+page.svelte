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
			pending: 'bg-parchment-500/20 text-parchment-400',
			processing: 'bg-lapis-600/20 text-lapis-600',
			completed: 'bg-emerald-600/20 text-emerald-500',
			failed: 'bg-terracotta-600/20 text-terracotta-500',
			cancelled: 'bg-parchment-500/10 text-parchment-500'
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
		}
	];

	const taskColumns = [
		{
			key: 'file_name',
			label: 'File',
			sortable: true,
			width: '100%'
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
