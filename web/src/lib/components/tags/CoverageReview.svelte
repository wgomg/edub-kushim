<script>
	import { resolve } from '$app/paths';
	import { api } from '#lib/api.js';
	import Icon from '#lib/components/Icon.svelte';
	import ListTable from '#lib/components/ListTable.svelte';
	import { createListQuery } from '#lib/list/listQuery.svelte.js';
	import { confirmStore } from '#lib/stores/confirmStore.svelte.js';
	import { toastStore } from '#lib/stores/toastStore.svelte.js';
	import * as authStore from '#lib/stores/authStore.svelte.js';
	import { formatNumber } from '#lib/utils/html.js';

	let { onchanged = null } = $props();

	let running = $state(false);

	const confirmClass =
		'rounded-md p-1.5 text-parchment-400 hover:bg-clay-800 hover:text-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none';
	const dismissClass =
		'rounded-md p-1.5 text-parchment-400 hover:bg-clay-800 hover:text-terracotta-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none';

	async function fetchFlags({ limit, offset, signal }) {
		const data = await api.tags.auditFlags(limit, offset, signal);
		if (!data) throw new Error('Failed to load coverage flags');
		return { items: data.results ?? [], total: data.total ?? null };
	}

	const flags = createListQuery({
		fetcher: fetchFlags,
		defaults: { page: 0, pageSize: 25, sortBy: '', sortOrder: 'desc' },
		keyField: 'id'
	});

	const columns = [
		{ key: 'document_title', label: 'Document', sortable: false, width: '45%' },
		{ key: 'tag_name', label: 'Tag', sortable: false },
		{ key: 'class', label: 'Class', sortable: false, width: '170px' },
		{ key: 'source', label: 'Source', sortable: false, width: '110px' },
		{ key: 'actions', label: 'Actions', sortable: false, cellClass: 'whitespace-nowrap' }
	];

	async function runAudit() {
		running = true;
		try {
			const result = await api.tags.reevaluateAudit();
			if (result.ok) {
				toastStore.success('Coverage audit queued');
				flags.reload();
			} else {
				toastStore.error('Failed to queue the coverage audit');
			}
		} finally {
			running = false;
		}
	}

	async function apply(documentID, tagName) {
		const result = await api.tags.applyAuditFlag({ document_id: documentID, tag_name: tagName });
		if (result.ok) {
			toastStore.success(`Applied "${tagName}"`);
			flags.reload();
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
			flags.reload();
			if (onchanged) onchanged();
		} else {
			toastStore.error('Failed to dismiss the coverage flag');
		}
	}

	async function createValue(tagName) {
		const result = await api.tags.createAuditValue({ tag_name: tagName });
		if (result.ok) {
			toastStore.success(`Registered "${tagName}" provisionally`);
			flags.reload();
		} else {
			toastStore.error('Failed to create the value');
		}
	}
</script>

{#snippet cell(col, row)}
	{#if col.key === 'document_title'}
		<a
			href={resolve(`documents/${row.document_uuid}`)}
			class="underline decoration-parchment-500/30 underline-offset-2 hover:text-gold-500"
			>{row.document_title ?? ''}</a
		>
	{:else if col.key === 'actions'}
		{#if !authStore.authEnabled() || authStore.isEditor()}
			<div class="flex gap-1">
				{#if row.class === 'missing'}
					<button
						onclick={() => createValue(row.tag_name)}
						title="Create value"
						aria-label="Create value"
						class={confirmClass}
					>
						<Icon name="plus" />
					</button>
				{:else}
					<button
						onclick={() => apply(row.document_id, row.tag_name)}
						title="Apply"
						aria-label="Apply"
						class={confirmClass}
					>
						<Icon name="check" />
					</button>
					<button
						onclick={() => dismiss(row.document_id, row.tag_name)}
						title="Dismiss"
						aria-label="Dismiss"
						class={dismissClass}
					>
						<Icon name="close" />
					</button>
				{/if}
			</div>
		{/if}
	{:else}
		{row[col.key] ?? ''}
	{/if}
{/snippet}

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
		<b class="font-semibold text-parchment-300">{formatNumber(flags.total ?? 0)}</b> open flags
	</span>
</div>

<p class="mb-4 max-w-3xl text-[0.8125rem] text-parchment-500">
	Documents the coverage audit flagged for a missing tag. Apply adds the tag to the document;
	Dismiss records that it does not apply.
</p>

<dl class="mb-4 max-w-3xl space-y-1 text-[0.8125rem] text-parchment-500">
	<div>
		<dt class="inline font-medium text-parchment-400">Class</dt>
		<dd class="inline">
			— <span class="font-medium text-parchment-300">missing</span>: the audit judged the tag
			applies, but it is not in the vocabulary yet, so “Create value” registers it provisionally.
			<span class="font-medium text-parchment-300">gray</span>: the audit could not decide and left
			it for review, so “Apply” attaches it or “Dismiss” records that it does not apply.
		</dd>
	</div>
	<div>
		<dt class="inline font-medium text-parchment-400">Source</dt>
		<dd class="inline">
			— <span class="font-medium text-parchment-300">trigger</span>: the semantic matcher’s strong
			band proposed the tag. <span class="font-medium text-parchment-300">lexical</span>: a weaker
			matcher hit confirmed by a literal text match.
		</dd>
	</div>
</dl>

<ListTable
	{columns}
	{cell}
	rows={flags.rows}
	total={flags.total}
	loading={flags.loading}
	error={flags.error}
	page={flags.page}
	pageSize={flags.pageSize}
	pageSizes={[25, 50, 100]}
	keyField="id"
	onPage={(next) => flags.update({ page: next })}
	onPageSize={(size) => flags.update({ pageSize: size })}
/>
