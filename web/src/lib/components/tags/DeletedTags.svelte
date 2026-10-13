<script>
	import { api } from '$lib/api';
	import DataTable from '$lib/components/DataTable.svelte';
	import { RESTORE_ICON, actionButton } from '$lib/icons.js';
	import { confirmStore } from '$lib/stores/confirmStore.svelte.js';
	import { toastStore } from '$lib/stores/toastStore.svelte.js';
	import * as authStore from '$lib/stores/authStore.js';
	import { escapeHtml, formatNumber } from '$lib/utils/html.js';

	let { refreshKey = 0, onchanged = null } = $props();

	let deletedQuery = $state('');
	let queryRefreshKey = $state(0);
	let deletedTotal = $state(null);

	const tableRefreshKey = $derived(refreshKey + queryRefreshKey);

	const columns = [
		{
			key: 'name',
			label: 'Name',
			sortable: false,
			width: '100%'
		},
		{
			key: 'created_at',
			label: 'Deleted',
			sortable: false,
			width: '180px',
			cell: (v) => (v ? new Date(v).toLocaleString() : '')
		},
		{
			key: 'actions',
			label: 'Actions',
			sortable: false,
			cellClass: 'whitespace-nowrap',
			cell: (_v, row) => {
				if (!authStore.authEnabled() || !authStore.isEditor()) return '';
				return actionButton(
					RESTORE_ICON,
					'Restore',
					'text-parchment-400 hover:text-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none',
					{ 'data-restore-tag': escapeHtml(row.name) }
				);
			}
		}
	];

	async function fetchDeleted({ limit, offset }) {
		return await api.tags.deletedList({ q: deletedQuery, limit, offset });
	}

	function handleActionClick(e) {
		const restoreBtn = e.target.closest('[data-restore-tag]');
		if (!restoreBtn) return;
		restore(restoreBtn.getAttribute('data-restore-tag'));
	}

	async function restore(name) {
		const ok = await confirmStore.confirm({
			title: 'Restore tag',
			message: `Restore tag "${name}"? Enrichment may assign it again.`
		});
		if (!ok) return;
		const result = await api.tags.restoreDeleted(name);
		if (result.ok) {
			toastStore.success(`Restored "${name}"`);
			if (onchanged) onchanged();
		} else {
			toastStore.error('Failed to restore tag');
		}
	}
</script>

<div class="mb-4 flex flex-wrap items-center gap-3">
	<label for="deleted-filter" class="sr-only">Filter blocked names</label>
	<input
		id="deleted-filter"
		name="deleted-filter"
		type="text"
		bind:value={deletedQuery}
		oninput={() => queryRefreshKey++}
		autocomplete="off"
		placeholder="Filter blocked names…"
		class="w-full max-w-xs rounded-md border border-clay-700 bg-clay-900 px-3 py-1.5 text-sm text-parchment-200 placeholder-parchment-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
	/>
	<span class="ms-auto text-[0.8125rem] text-parchment-500 tabular-nums">
		<b class="font-semibold text-parchment-300">{formatNumber(deletedTotal ?? 0)}</b> blocked names
	</span>
</div>

<p class="mb-4 max-w-3xl text-[0.8125rem] text-parchment-500">
	Names listed here are blocked from enrichment. A blocked tag is re-created when restored.
</p>

<DataTable
	{columns}
	fetch={fetchDeleted}
	keyField="name"
	defaultPageSize={10}
	pageSizes={[10, 25, 50]}
	refreshKey={tableRefreshKey}
	onActionClick={handleActionClick}
	onTotal={(t) => (deletedTotal = t)}
/>
