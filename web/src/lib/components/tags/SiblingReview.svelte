<script>
	import { api } from '#lib/api.js';
	import ListTable from '#lib/components/ListTable.svelte';
	import { createListQuery } from '#lib/list/listQuery.svelte.js';
	import { formatNumber } from '#lib/utils/html.js';

	async function fetchSiblings({ limit, offset, signal }) {
		const data = await api.tags.siblings(limit, offset, signal);
		if (!data) throw new Error('Failed to load overlap readings');
		return {
			items: (data.results ?? []).map((row) => ({ ...row, id: `${row.a} ⊇ ${row.b}` })),
			total: data.total ?? null,
			absent: data.absent ?? false
		};
	}

	const siblings = createListQuery({
		fetcher: fetchSiblings,
		defaults: { page: 0, pageSize: 25, sortBy: '', sortOrder: 'desc' },
		keyField: 'id'
	});

	const columns = [
		{ key: 'a', label: 'Tag pair', sortable: false },
		{ key: 'containment', label: 'Overlap', sortable: false, width: '130px' },
		{ key: 'mass_a', label: 'Documents', sortable: false, width: '130px' },
		{ key: 'gray', label: 'Status', sortable: false, width: '130px' }
	];
</script>

{#snippet cell(col, row)}
	{#if col.key === 'a'}
		{row.a} <span class="ui-symbol">⊇</span>
		{row.b}
	{:else if col.key === 'containment'}
		{Math.round((row.containment ?? 0) * 100)}%
	{:else if col.key === 'mass_a'}
		{formatNumber(row.mass_a)} · {formatNumber(row.mass_b)}
	{:else if col.key === 'gray'}
		{row.gray ? 'Needs review' : 'For reference'}
	{:else}
		{row[col.key] ?? ''}
	{/if}
{/snippet}

<div class="mb-4 flex flex-wrap items-center gap-3">
	<span class="ms-auto text-[0.8125rem] text-parchment-500 tabular-nums">
		<b class="font-semibold text-parchment-300">{formatNumber(siblings.total ?? 0)}</b> overlapping pairs
	</span>
</div>

<p class="mb-4 max-w-3xl text-[0.8125rem] text-parchment-500">
	Tags whose documents overlap: most documents tagged with one are also tagged with the other. A
	narrower tag inside a broader one is often exactly right, so nothing is merged or removed on its
	own. This list is for review — the pairs marked “Needs review” are the ones worth a closer look.
</p>

{#if siblings.data?.absent}
	<p class="text-[0.8125rem] text-parchment-500">
		No measurement run yet. Run the tag measurement sweep to check for overlapping tags.
	</p>
{:else}
	<ListTable
		{columns}
		{cell}
		rows={siblings.rows}
		total={siblings.total}
		loading={siblings.loading}
		error={siblings.error}
		page={siblings.page}
		pageSize={siblings.pageSize}
		pageSizes={[25, 50, 100]}
		keyField="id"
		onPage={(next) => siblings.update({ page: next })}
		onPageSize={(size) => siblings.update({ pageSize: size })}
	/>
{/if}
