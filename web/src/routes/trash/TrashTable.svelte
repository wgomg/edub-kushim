<script>
	import { api } from '#lib/api.js';
	import Icon from '#lib/components/Icon.svelte';
	import ListTable from '#lib/components/ListTable.svelte';
	import { createListQuery } from '#lib/list/listQuery.svelte.js';
	import { formatDateTime, formatSize } from '#lib/utils/html.js';
	import { confirmStore } from '#lib/stores/confirmStore.svelte.js';
	import { toastStore } from '#lib/stores/toastStore.svelte.js';
	import * as authStore from '#lib/stores/authStore.svelte.js';
	import { trashQuery } from './query.js';

	async function fetchTrash({ limit, offset, signal }) {
		const data = await api.trash.list(limit, offset, signal);
		if (!data) throw new Error('Failed to load trash');
		return { items: data.results ?? [], total: data.total ?? null };
	}

	const trash = createListQuery({
		fetcher: fetchTrash,
		codec: trashQuery,
		sortKeys: [],
		keyField: 'document_id'
	});

	const selectedDocs = $derived(trash.selectedRows);

	const columns = [
		{ key: 'title', label: 'Title', sortable: false },
		{ key: 'original_type', label: 'Type', sortable: false, width: '120px' },
		{ key: 'file_size', label: 'Size', sortable: false, width: '100px' },
		{ key: 'page_count', label: 'Pages', sortable: false, width: '80px' },
		{ key: 'language', label: 'Language', sortable: false, width: '120px' },
		{ key: 'deleted_at', label: 'Deleted', sortable: false, minWidth: '170px' },
		{ key: '_actions', label: '', sortable: false, cellClass: 'whitespace-nowrap', width: '90px' }
	];

	async function handleRestore(documentId, title) {
		const ok = await confirmStore.confirm({
			title: 'Restore document',
			message: `Restore "${title}" from trash?`
		});
		if (!ok) return;
		const res = await api.trash.restore(documentId);
		if (res?.ok || res?.status === 204) {
			toastStore.success('Document restored');
			trash.reload();
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
			trash.reload();
		} else {
			toastStore.error(res.data?.error || 'Permanent delete failed');
		}
	}

	async function handleBatchRestore() {
		const n = selectedDocs.length;
		const ok = await confirmStore.confirm({
			title: 'Restore documents',
			message: `Restore ${n} document(s) from trash?`
		});
		if (!ok) return;
		const res = await api.trash.batchRestore(selectedDocs.map((row) => row.document_id));
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
		trash.reload();
	}

	async function handleBatchDelete() {
		const n = selectedDocs.length;
		const ok = await confirmStore.confirm({
			title: 'Delete permanently',
			message: `Permanently delete ${n} document(s)? This cannot be undone.`,
			danger: true
		});
		if (!ok) return;
		const res = await api.trash.batchPermanentDelete(selectedDocs.map((row) => row.document_id));
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
		trash.reload();
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
		trash.reload();
	}
</script>

{#snippet cell(col, row)}
	{#if col.key === 'title'}
		{#if row.title}
			<span class="block max-w-xs truncate" title={row.title}>{row.title}</span>
		{:else}
			<span class="text-parchment-500 italic">—</span>
		{/if}
	{:else if col.key === 'original_type'}
		{row.original_type ?? '—'}
	{:else if col.key === 'file_size'}
		{formatSize(row.file_size)}
	{:else if col.key === 'page_count'}
		{row.page_count != null ? row.page_count : '—'}
	{:else if col.key === 'language'}
		{#if row.language}{row.language}{:else}<span class="text-parchment-500 italic">—</span>{/if}
	{:else if col.key === 'deleted_at'}
		{formatDateTime(row.deleted_at)}
	{:else if col.key === '_actions'}
		{#if !authStore.authEnabled() || authStore.isEditor()}
			<div class="flex items-center">
				<button
					onclick={() => handleRestore(row.document_id, row.title || 'Untitled document')}
					title="Restore"
					aria-label="Restore"
					class="rounded-md p-1.5 text-parchment-400 hover:bg-clay-800 hover:text-emerald-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					><Icon name="restore" /></button
				>
				<button
					onclick={() => handlePermanentDelete(row.document_id, row.title || 'Untitled document')}
					title="Delete permanently"
					aria-label="Delete permanently"
					class="rounded-md p-1.5 text-parchment-400 hover:bg-clay-800 hover:text-terracotta-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					><Icon name="delete" /></button
				>
			</div>
		{/if}
	{:else}
		{row[col.key] ?? ''}
	{/if}
{/snippet}

<div class="space-y-4">
	<div class="flex items-center gap-3">
		{#if selectedDocs.length > 0}
			<button
				onclick={handleBatchRestore}
				class="shrink-0 rounded-lg bg-gold-600 px-3 py-2 text-sm font-medium text-clay-950 hover:bg-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:ring-offset-2 focus-visible:ring-offset-clay-950 focus-visible:outline-none"
			>
				Restore selected ({selectedDocs.length})
			</button>
			<button
				onclick={handleBatchDelete}
				class="shrink-0 rounded-lg bg-terracotta-700 px-3 py-2 text-sm font-medium text-parchment-200 hover:bg-terracotta-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:ring-offset-2 focus-visible:ring-offset-clay-950 focus-visible:outline-none"
			>
				Delete permanently ({selectedDocs.length})
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

	<ListTable
		{columns}
		{cell}
		rows={trash.rows}
		total={trash.total}
		loading={trash.loading}
		error={trash.error}
		page={trash.query.page}
		pageSize={trash.query.pageSize}
		pageSizes={[10, 25, 50, 100]}
		sortBy={trash.query.sortBy}
		sortOrder={trash.query.sortOrder}
		keyField="document_id"
		title="Trash"
		selectedKeys={trash.selectedKeys}
		onPage={(next) => trash.update({ page: next })}
		onPageSize={(size) => trash.update({ pageSize: size })}
		onToggleRow={trash.toggle}
		onToggleAll={trash.toggleAll}
	/>
</div>
