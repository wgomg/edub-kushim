<script>
	import { api } from '$lib/api';
	import DataTable from '$lib/components/DataTable.svelte';
	import { RESTORE_ICON, DELETE_ICON, actionButton } from '$lib/icons.js';
	import { escapeHtml, formatSize } from '$lib/utils/html.js';
	import { confirmStore } from '$lib/stores/confirmStore.svelte.js';
	import { toastStore } from '$lib/stores/toastStore.svelte.js';
	import * as authStore from '$lib/stores/authStore.js';

	let selectedRows = $state([]);
	let refreshKey = $state(0);

	const columns = [
		{
			key: 'title',
			label: 'Title',
			sortable: false,
			cell: (v) =>
				v
					? `<span class="block max-w-xs truncate" title="${escapeHtml(v)}">${escapeHtml(v)}</span>`
					: '<span class="text-parchment-500 italic">—</span>'
		},
		{
			key: 'original_type',
			label: 'Type',
			sortable: false,
			cell: (v) => (v ? escapeHtml(v) : '<span class="text-parchment-500 italic">—</span>'),
			width: '120px'
		},
		{
			key: 'file_size',
			label: 'Size',
			sortable: false,
			cell: (v) => formatSize(v),
			width: '100px'
		},
		{
			key: 'page_count',
			label: 'Pages',
			sortable: false,
			cell: (v) => (v != null ? String(v) : '—'),
			width: '80px'
		},
		{
			key: 'language',
			label: 'Language',
			sortable: false,
			cell: (v) => (v ? escapeHtml(v) : '<span class="text-parchment-500 italic">—</span>'),
			width: '120px'
		},
		{
			key: 'deleted_at',
			label: 'Deleted',
			sortable: false,
			cell: (v) => formatTime(v),
			minWidth: '170px'
		},
		{
			key: '_actions',
			label: '',
			sortable: false,
			noUnderline: true,
			cellClass: 'whitespace-nowrap',
			width: '90px',
			cell: (_v, row) => {
				if (!authStore.authEnabled() || !authStore.isEditor()) return '';
				const safeTitle = escapeHtml(row.title || 'Untitled document');
				return `${actionButton(RESTORE_ICON, 'Restore', 'text-parchment-400 hover:text-emerald-500', { 'data-restore-doc': row.document_id, 'data-doc-title': safeTitle })}
${actionButton(DELETE_ICON, 'Delete permanently', 'text-parchment-400 hover:text-terracotta-500', { 'data-delete-doc': row.document_id, 'data-doc-title': safeTitle })}`;
			}
		}
	];

	async function fetch({ limit, offset }) {
		return await api.trash.list(limit, offset);
	}

	function formatTime(t) {
		if (!t) return '—';
		return new Date(t).toLocaleString();
	}

	async function handleRestore(documentId, title) {
		const ok = await confirmStore.confirm({
			title: 'Restore document',
			message: `Restore "${title}" from trash?`
		});
		if (!ok) return;
		const res = await api.trash.restore(documentId);
		if (res?.ok || res?.status === 204) {
			toastStore.success('Document restored');
			selectedRows = [];
			refreshKey++;
		} else {
			toastStore.error(res.data?.error || 'Restore failed');
		}
	}

	async function handlePermanentDelete(documentId, title) {
		const ok = await confirmStore.confirm({
			title: 'Delete permanently',
			message: `Permanently delete "${title}"? This cannot be undone.`,
			danger: true
		});
		if (!ok) return;
		const res = await api.trash.permanentDelete(documentId);
		if (res?.ok || res?.status === 204) {
			toastStore.success('Document permanently deleted');
			selectedRows = [];
			refreshKey++;
		} else {
			toastStore.error(res.data?.error || 'Permanent delete failed');
		}
	}

	async function handleBatchRestore() {
		const n = selectedRows.length;
		const ok = await confirmStore.confirm({
			title: 'Restore documents',
			message: `Restore ${n} document(s) from trash?`
		});
		if (!ok) return;
		const res = await api.trash.batchRestore(selectedRows.map((r) => r.document_id));
		if (!res.ok) {
			toastStore.error(res.data?.error || 'Batch restore failed');
			return;
		}
		if (res.data?.failed?.length > 0) {
			toastStore.warning(
				`Restored ${res.data.restored} of ${n} documents; ${res.data.failed.length} failed.`
			);
		} else {
			toastStore.success(`Restored ${res.data.restored} document(s)`);
		}
		selectedRows = [];
		refreshKey++;
	}

	async function handleBatchDelete() {
		const n = selectedRows.length;
		const ok = await confirmStore.confirm({
			title: 'Delete permanently',
			message: `Permanently delete ${n} document(s)? This cannot be undone.`,
			danger: true
		});
		if (!ok) return;
		const res = await api.trash.batchPermanentDelete(selectedRows.map((r) => r.document_id));
		if (!res.ok) {
			toastStore.error(res.data?.error || 'Batch permanent delete failed');
			return;
		}
		if (res.data?.failed?.length > 0) {
			toastStore.warning(
				`Permanently deleted ${res.data.deleted} of ${n} documents; ${res.data.failed.length} failed.`
			);
		} else {
			toastStore.success(`Permanently deleted ${res.data.deleted} document(s)`);
		}
		selectedRows = [];
		refreshKey++;
	}

	async function handlePurge() {
		const ok = await confirmStore.confirm({
			title: 'Purge expired',
			message: 'Permanently delete all expired documents? This cannot be undone.',
			danger: true
		});
		if (!ok) return;
		const res = await api.trash.purge();
		if (!res.ok) {
			toastStore.error(res.data?.error || 'Purge failed');
			return;
		}
		if (res.data?.purged > 0) {
			toastStore.success(`Purged ${res.data.purged} document(s)`);
		} else {
			toastStore.success('No expired documents to purge');
		}
		selectedRows = [];
		refreshKey++;
	}

	function handlePageClick(e) {
		const restoreBtn = e.target.closest('[data-restore-doc]');
		if (restoreBtn) {
			handleRestore(
				restoreBtn.getAttribute('data-restore-doc'),
				restoreBtn.getAttribute('data-doc-title') || 'Untitled document'
			);
			return;
		}
		const deleteBtn = e.target.closest('[data-delete-doc]');
		if (deleteBtn) {
			handlePermanentDelete(
				deleteBtn.getAttribute('data-delete-doc'),
				deleteBtn.getAttribute('data-doc-title') || 'Untitled document'
			);
			return;
		}
	}
</script>

{#if authStore.authEnabled() && !authStore.isEditor()}
	<p class="text-parchment-500">You do not have permission to view this page.</p>
{:else}
	<div class="space-y-4" onclick={handlePageClick} onkeydown={() => {}} role="presentation">
		<div class="flex items-center gap-3">
			{#if selectedRows.length > 0}
				<button
					onclick={handleBatchRestore}
					class="shrink-0 rounded-lg bg-gold-600 px-3 py-2 text-sm font-medium text-clay-950 hover:bg-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:ring-offset-2 focus-visible:ring-offset-clay-950 focus-visible:outline-none"
				>
					Restore selected ({selectedRows.length})
				</button>
				<button
					onclick={handleBatchDelete}
					class="shrink-0 rounded-lg bg-terracotta-700 px-3 py-2 text-sm font-medium text-parchment-200 hover:bg-terracotta-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:ring-offset-2 focus-visible:ring-offset-clay-950 focus-visible:outline-none"
				>
					Delete permanently ({selectedRows.length})
				</button>
			{/if}
			{#if !authStore.authEnabled() || authStore.isEditor()}
				<button
					onclick={handlePurge}
					class="shrink-0 rounded-lg border border-terracotta-700 px-3 py-2 text-sm font-medium text-terracotta-400 hover:bg-terracotta-900/50 hover:text-terracotta-300 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:ring-offset-2 focus-visible:ring-offset-clay-950 focus-visible:outline-none"
				>
					Purge expired
				</button>
			{/if}
		</div>

		<DataTable
			{columns}
			{fetch}
			keyField="id"
			title="Trash"
			{refreshKey}
			selectable={true}
			onselectionchange={(rows) => (selectedRows = rows)}
			defaultSortBy="deleted_at"
			defaultSortOrder="desc"
		/>
	</div>
{/if}
