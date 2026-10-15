<script>
	import { api } from '$lib/api';
	import DataTable from '$lib/components/DataTable.svelte';
	import { actionButton } from '$lib/icons.js';
	import { confirmStore } from '$lib/stores/confirmStore.svelte.js';
	import { toastStore } from '$lib/stores/toastStore.svelte.js';
	import * as authStore from '$lib/stores/authStore.js';
	import { escapeHtml, formatNumber } from '$lib/utils/html.js';

	let { onchanged = null } = $props();

	let refreshKey = $state(0);
	let flagsTotal = $state(null);
	let running = $state(false);

	const CHECK_ICON =
		'<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="h-4 w-4"><path d="M20 6 9 17l-5-5"/></svg>';
	const DISMISS_ICON =
		'<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="h-4 w-4"><path d="M18 6 6 18M6 6l12 12"/></svg>';
	const PLUS_ICON =
		'<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="h-4 w-4"><path d="M12 5v14M5 12h14"/></svg>';

	const confirmClass =
		'text-parchment-400 hover:text-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none';
	const dismissClass =
		'text-parchment-400 hover:text-terracotta-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none';

	const columns = [
		{ key: 'document_title', label: 'Document', sortable: false, width: '45%' },
		{ key: 'tag_name', label: 'Dimension', sortable: false },
		{ key: 'class', label: 'Class', sortable: false, width: '170px' },
		{ key: 'source', label: 'Source', sortable: false, width: '110px' },
		{
			key: 'actions',
			label: 'Actions',
			sortable: false,
			cellClass: 'whitespace-nowrap',
			cell: (_v, row) => {
				if (!authStore.authEnabled() || !authStore.isEditor()) return '';
				const attrs = { 'data-doc': row.document_id, 'data-tag': escapeHtml(row.tag_name) };
				if (row.class === 'missing') {
					return actionButton(PLUS_ICON, 'Create value', confirmClass, {
						...attrs,
						'data-audit-create': ''
					});
				}
				const apply = actionButton(CHECK_ICON, 'Apply', confirmClass, {
					...attrs,
					'data-audit-apply': ''
				});
				const dismiss = actionButton(DISMISS_ICON, 'Dismiss', dismissClass, {
					...attrs,
					'data-audit-dismiss': ''
				});
				return apply + dismiss;
			}
		}
	];

	async function fetchFlags({ limit, offset }) {
		return await api.tags.auditFlags(limit, offset);
	}

	async function runAudit() {
		running = true;
		try {
			const result = await api.tags.reevaluateAudit();
			if (result.ok) {
				toastStore.success('Coverage audit queued');
				refreshKey++;
			} else {
				toastStore.error('Failed to queue the coverage audit');
			}
		} finally {
			running = false;
		}
	}

	function handleActionClick(e) {
		const btn = e.target.closest('[data-audit-apply], [data-audit-dismiss], [data-audit-create]');
		if (!btn) return;
		const doc = Number(btn.getAttribute('data-doc'));
		const tag = btn.getAttribute('data-tag');
		if (!tag) return;
		if (btn.hasAttribute('data-audit-apply')) apply(doc, tag);
		else if (btn.hasAttribute('data-audit-dismiss')) dismiss(doc, tag);
		else createValue(tag);
	}

	async function apply(documentID, tagName) {
		const result = await api.tags.applyAuditFlag({ document_id: documentID, tag_name: tagName });
		if (result.ok) {
			toastStore.success(`Applied "${tagName}"`);
			refreshKey++;
			if (onchanged) onchanged();
		} else {
			toastStore.error('Failed to apply the coverage flag');
		}
	}

	async function dismiss(documentID, tagName) {
		const ok = await confirmStore.confirm({
			title: 'Dismiss coverage flag',
			message: `Record that "${tagName}" does not apply to this document?`
		});
		if (!ok) return;
		const result = await api.tags.dismissAuditFlag({ document_id: documentID, tag_name: tagName });
		if (result.ok) {
			toastStore.success(`Dismissed "${tagName}"`);
			refreshKey++;
		} else {
			toastStore.error('Failed to dismiss the coverage flag');
		}
	}

	async function createValue(tagName) {
		const result = await api.tags.createAuditValue({ tag_name: tagName });
		if (result.ok) {
			toastStore.success(`Registered "${tagName}" provisionally`);
			refreshKey++;
		} else {
			toastStore.error('Failed to create the value');
		}
	}
</script>

<div class="mb-4 flex flex-wrap items-center gap-3">
	<button
		type="button"
		onclick={runAudit}
		disabled={running}
		class="rounded-md bg-gold-500 px-3 py-1.5 text-xs font-medium text-clay-950 hover:bg-gold-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-50"
	>
		{running ? 'Queueing…' : 'Run coverage audit'}
	</button>
	<span class="ms-auto text-[0.8125rem] text-parchment-500 tabular-nums">
		<b class="font-semibold text-parchment-300">{formatNumber(flagsTotal ?? 0)}</b> open flags
	</span>
</div>

<p class="mb-4 max-w-3xl text-[0.8125rem] text-parchment-500">
	Documents the coverage audit flagged for a missing tag. Apply adds the tag to the document;
	Dismiss records that it does not apply.
</p>

<DataTable
	{columns}
	fetch={fetchFlags}
	keyField="id"
	defaultPageSize={25}
	pageSizes={[25, 50, 100]}
	{refreshKey}
	onActionClick={handleActionClick}
	onTotal={(t) => (flagsTotal = t)}
/>
