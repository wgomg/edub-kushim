<script>
	import { api } from '$lib/api';
	import DataTable from '$lib/components/DataTable.svelte';
	import { escapeHtml, formatNumber } from '$lib/utils/html.js';

	let refreshKey = $state(0);
	let readingsTotal = $state(null);
	let snapshot = $state(null);

	const columns = [
		{
			key: 'a',
			label: 'Tag pair',
			sortable: false,
			cell: (_v, row) => `${escapeHtml(row.a)} ⊇ ${escapeHtml(row.b)}`
		},
		{
			key: 'containment',
			label: 'Overlap',
			sortable: false,
			width: '130px',
			cell: (_v, row) => `${Math.round((row.containment ?? 0) * 100)}%`
		},
		{
			key: 'mass_a',
			label: 'Documents',
			sortable: false,
			width: '130px',
			cell: (_v, row) => `${formatNumber(row.mass_a)} · ${formatNumber(row.mass_b)}`
		},
		{
			key: 'gray',
			label: 'Status',
			sortable: false,
			width: '130px',
			cell: (_v, row) => (row.gray ? 'Needs review' : 'For reference')
		}
	];

	async function fetchSiblings({ limit, offset }) {
		const data = await api.tags.siblings(limit, offset);
		snapshot = data;
		return {
			results: (data.results ?? []).map((row) => ({ ...row, id: `${row.a} ⊇ ${row.b}` })),
			total: data.total ?? 0
		};
	}
</script>

<div class="mb-4 flex flex-wrap items-center gap-3">
	<span class="ms-auto text-[0.8125rem] text-parchment-500 tabular-nums">
		<b class="font-semibold text-parchment-300">{formatNumber(readingsTotal ?? 0)}</b> overlapping pairs
	</span>
</div>

<p class="mb-4 max-w-3xl text-[0.8125rem] text-parchment-500">
	Tags whose documents overlap: most documents tagged with one are also tagged with the other. A
	narrower tag inside a broader one is often exactly right, so nothing is merged or removed on its
	own. This list is for review — the pairs marked “Needs review” are the ones worth a closer look.
</p>

{#if snapshot?.absent}
	<p class="text-[0.8125rem] text-parchment-500">
		No measurement run yet. Run the tag measurement sweep to check for overlapping tags.
	</p>
{:else}
	<DataTable
		{columns}
		fetch={fetchSiblings}
		defaultPageSize={25}
		pageSizes={[25, 50, 100]}
		{refreshKey}
		onTotal={(t) => (readingsTotal = t)}
	/>
{/if}
