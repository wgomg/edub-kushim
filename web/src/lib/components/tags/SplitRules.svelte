<!--
  Lists the standing operator split rules: the names future emissions replace with their recorded components.
  Reverting a rule is the inverse of the manual split — the rule is deleted and the name is judged again on
  its next emission — so the action stays available after the tag row itself is gone.
-->
<script>
	import { api } from '#lib/api.js';
	import Icon from '#lib/components/Icon.svelte';
	import ListTable from '#lib/components/ListTable.svelte';
	import { createListQuery } from '#lib/list/listQuery.svelte.js';
	import { confirmStore } from '#lib/stores/confirmStore.svelte.js';
	import { toastStore } from '#lib/stores/toastStore.svelte.js';
	import * as authStore from '#lib/stores/authStore.svelte.js';
	import { formatDateTime, formatNumber } from '#lib/utils/html.js';

	let { onchanged = null } = $props();

	async function fetchRules({ limit, offset, signal }) {
		const data = await api.tags.splitRulesList({ limit, offset, signal });
		if (!data) throw new Error('Failed to load split rules');
		return { items: data.results ?? [], total: data.total ?? null };
	}

	const rules = createListQuery({
		fetcher: fetchRules,
		defaults: { page: 0, pageSize: 10, sortBy: '', sortOrder: 'desc' },
		keyField: 'candidate'
	});

	const columns = [
		{ key: 'candidate', label: 'Name', sortable: false, width: '100%' },
		{ key: 'parts', label: 'Applied components', sortable: false, width: '220px' },
		{ key: 'created_at', label: 'Recorded', sortable: false, width: '180px' },
		{ key: 'actions', label: 'Actions', sortable: false, cellClass: 'whitespace-nowrap' }
	];

	async function revert(candidate) {
		const ok = await confirmStore.confirm({
			title: 'Revert split',
			message: `Remove the split rule for "${candidate}"? Future emissions judge the name again instead of replacing it with its recorded components.`
		});
		if (!ok) return;
		const result = await api.tags.revertSplitRule(candidate);
		if (result.ok) {
			toastStore.success(`Reverted split "${candidate}"`);
			rules.reload();
			if (onchanged) onchanged();
		} else if (result.status === 404) {
			toastStore.error('This split rule no longer exists');
		} else {
			toastStore.error('Failed to revert the split rule');
		}
	}
</script>

{#snippet cell(col, row)}
	{#if col.key === 'parts'}
		{(row.parts ?? []).join(', ')}
	{:else if col.key === 'created_at'}
		{formatDateTime(row.created_at)}
	{:else if col.key === 'actions'}
		{#if !authStore.authEnabled() || authStore.isEditor()}
			<button
				onclick={() => revert(row.candidate)}
				title="Revert split"
				aria-label="Revert split"
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
	<span class="ms-auto text-[0.8125rem] text-parchment-500 tabular-nums">
		<b class="font-semibold text-parchment-300">{formatNumber(rules.total ?? 0)}</b> split rules
	</span>
</div>

<p class="mb-4 max-w-3xl text-[0.8125rem] text-parchment-500">
	Every manual split is recorded as a rule: when the name is emitted again, its recorded components
	replace it. Reverting a rule leaves the components and the documents already moved onto them
	untouched.
</p>

<ListTable
	{columns}
	{cell}
	rows={rules.rows}
	total={rules.total}
	loading={rules.loading}
	error={rules.error}
	page={rules.page}
	pageSize={rules.pageSize}
	pageSizes={[10, 25, 50]}
	keyField="candidate"
	onPage={(next) => rules.update({ page: next })}
	onPageSize={(size) => rules.update({ pageSize: size })}
/>
