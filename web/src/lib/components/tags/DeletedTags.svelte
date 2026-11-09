<script>
	import { api } from '#lib/api.js';
	import Icon from '#lib/components/Icon.svelte';
	import ListTable from '#lib/components/ListTable.svelte';
	import { createListQuery } from '#lib/list/listQuery.svelte.js';
	import { QueryDraft } from '#lib/list/queryDraft.svelte.js';
	import { confirmStore } from '#lib/stores/confirmStore.svelte.js';
	import { toastStore } from '#lib/stores/toastStore.svelte.js';
	import * as authStore from '#lib/stores/authStore.svelte.js';
	import { formatDateTime, formatNumber } from '#lib/utils/html.js';

	let { onchanged = null } = $props();

	async function fetchDeleted({ limit, offset, query, signal }) {
		const data = await api.tags.deletedList({ q: query.q, limit, offset, signal });
		if (!data) throw new Error('Failed to load blocked names');
		return { items: data.results ?? [], total: data.total ?? null };
	}

	const deleted = createListQuery({
		fetcher: fetchDeleted,
		defaults: { q: '', page: 0, pageSize: 10, sortBy: '', sortOrder: 'desc' },
		keyField: 'name'
	});

	const draft = new QueryDraft(deleted);

	const columns = [
		{ key: 'name', label: 'Name', sortable: false, width: '100%' },
		{ key: 'deleted_at', label: 'Deleted', sortable: false, width: '180px' },
		{ key: 'actions', label: 'Actions', sortable: false, cellClass: 'whitespace-nowrap' }
	];

	async function restore(name) {
		const ok = await confirmStore.confirm({
			title: 'Restore tag',
			message: `Restore tag "${name}"? Enrichment may assign it again.`
		});
		if (!ok) return;
		const result = await api.tags.restoreDeleted(name);
		if (result.ok) {
			toastStore.success(`Restored "${name}"`);
			deleted.reload();
			if (onchanged) onchanged();
		} else {
			toastStore.error('Failed to restore tag');
		}
	}
</script>

{#snippet cell(col, row)}
	{#if col.key === 'deleted_at'}
		{formatDateTime(row.deleted_at)}
	{:else if col.key === 'actions'}
		{#if !authStore.authEnabled() || authStore.isEditor()}
			<button
				onclick={() => restore(row.name)}
				title="Restore"
				aria-label="Restore"
				class="rounded-md p-1.5 text-parchment-400 hover:bg-clay-800 hover:text-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
			>
				<Icon name="restore" />
			</button>
		{/if}
	{:else}
		{row[col.key] ?? ''}
	{/if}
{/snippet}

<div class="mb-4 flex flex-wrap items-center gap-3">
	<label for="deleted-filter" class="sr-only">Filter blocked names</label>
	<input
		id="deleted-filter"
		name="deleted-filter"
		type="text"
		bind:value={draft.value}
		oninput={draft.markEdited}
		onfocus={draft.focus}
		onblur={draft.blur}
		autocomplete="off"
		placeholder="Filter blocked names…"
		class="w-full max-w-xs rounded-md border border-clay-700 bg-clay-900 px-3 py-1.5 text-sm text-parchment-200 placeholder-parchment-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
	/>
	<span class="ms-auto text-[0.8125rem] text-parchment-500 tabular-nums">
		<b class="font-semibold text-parchment-300">{formatNumber(deleted.total ?? 0)}</b> blocked names
	</span>
</div>

<p class="mb-4 max-w-3xl text-[0.8125rem] text-parchment-500">
	Names listed here are blocked from enrichment. A blocked tag is re-created when restored.
</p>

<ListTable
	{columns}
	{cell}
	rows={deleted.rows}
	total={deleted.total}
	loading={deleted.loading}
	error={deleted.error}
	page={deleted.page}
	pageSize={deleted.pageSize}
	pageSizes={[10, 25, 50]}
	keyField="name"
	onPage={(next) => deleted.update({ page: next })}
	onPageSize={(size) => deleted.update({ pageSize: size })}
/>
