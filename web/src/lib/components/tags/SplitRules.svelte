<!--
  Lists the standing operator split rules: the names future emissions replace with their recorded components.
  Reverting a rule is the inverse of the manual split — the rule is deleted and the name is judged again on
  its next emission — so the action stays available after the tag row itself is gone.
-->
<script>
	import { api } from '#lib/api.js';
	import DataTable from '#lib/components/DataTable.svelte';
	import { RESTORE_ICON, actionButton } from '#lib/icons.js';
	import { confirmStore } from '#lib/stores/confirmStore.svelte.js';
	import { toastStore } from '#lib/stores/toastStore.svelte.js';
	import * as authStore from '#lib/stores/authStore.svelte.js';
	import { escapeHtml, formatNumber } from '#lib/utils/html.js';

	let { refreshKey = 0, onchanged = null } = $props();

	let rulesTotal = $state(null);

	const columns = [
		{
			key: 'candidate',
			label: 'Name',
			sortable: false,
			width: '100%'
		},
		{
			key: 'parts',
			label: 'Applied components',
			sortable: false,
			width: '220px',
			cell: (v) => (v ?? []).join(', ')
		},
		{
			key: 'created_at',
			label: 'Recorded',
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
					'Revert split',
					'text-parchment-400 hover:text-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none',
					{ 'data-revert-split-rule': escapeHtml(row.candidate) }
				);
			}
		}
	];

	async function fetchRules({ limit, offset }) {
		return await api.tags.splitRulesList({ limit, offset });
	}

	function handleActionClick(e) {
		const revertBtn = e.target.closest('[data-revert-split-rule]');
		if (!revertBtn) return;
		revert(revertBtn.getAttribute('data-revert-split-rule'));
	}

	async function revert(candidate) {
		const ok = await confirmStore.confirm({
			title: 'Revert split',
			message: `Remove the split rule for "${candidate}"? Future emissions judge the name again instead of replacing it with its recorded components.`
		});
		if (!ok) return;
		const result = await api.tags.revertSplitRule(candidate);
		if (result.ok) {
			toastStore.success(`Reverted split "${candidate}"`);
			if (onchanged) onchanged();
		} else if (result.status === 404) {
			toastStore.error('This split rule no longer exists');
		} else {
			toastStore.error('Failed to revert the split rule');
		}
	}
</script>

<div class="mb-4 flex flex-wrap items-center gap-3">
	<span class="ms-auto text-[0.8125rem] text-parchment-500 tabular-nums">
		<b class="font-semibold text-parchment-300">{formatNumber(rulesTotal ?? 0)}</b> split rules
	</span>
</div>

<p class="mb-4 max-w-3xl text-[0.8125rem] text-parchment-500">
	Every manual split is recorded as a rule: when the name is emitted again, its recorded components
	replace it. Reverting a rule leaves the components and the documents already moved onto them
	untouched.
</p>

<DataTable
	{columns}
	fetch={fetchRules}
	keyField="candidate"
	defaultPageSize={10}
	pageSizes={[10, 25, 50]}
	{refreshKey}
	onActionClick={handleActionClick}
	onTotal={(t) => (rulesTotal = t)}
/>
