<script>
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import Icon from '#lib/components/Icon.svelte';
	import ListTable from '#lib/components/ListTable.svelte';
	import Modal from '#lib/components/Modal.svelte';
	import TagReconcileStrip from '#lib/components/tags/TagReconcileStrip.svelte';
	import FoldTagModal from '#lib/components/tags/FoldTagModal.svelte';
	import SplitTagModal from '#lib/components/tags/SplitTagModal.svelte';
	import ProvisionalReview from '#lib/components/tags/ProvisionalReview.svelte';
	import CoverageReview from '#lib/components/tags/CoverageReview.svelte';
	import SiblingReview from '#lib/components/tags/SiblingReview.svelte';
	import DeletedTags from '#lib/components/tags/DeletedTags.svelte';
	import SplitRules from '#lib/components/tags/SplitRules.svelte';
	import { createListQuery, effectiveUrl, toggleSortState } from '#lib/list/listQuery.svelte.js';
	import { QueryDraft } from '#lib/list/queryDraft.svelte.js';
	import { formatDate, formatNumber } from '#lib/utils/html.js';
	import { confirmStore } from '#lib/stores/confirmStore.svelte.js';
	import { toastStore } from '#lib/stores/toastStore.svelte.js';
	import { api } from '#lib/api.js';
	import * as authStore from '#lib/stores/authStore.svelte.js';
	import { tagsQuery } from './query.js';

	const TABS = ['vocabulary', 'provisional', 'coverage', 'siblings', 'deleted', 'split-rules'];

	// main's p-6 is the sticky view rectangle's origin, so -top-6 lets the strip stick flush under the
	// topbar instead of leaving a 24px band over the panel
	const tabStrip =
		'sticky -top-6 z-20 mb-6 flex items-end gap-1 overflow-x-auto overflow-y-hidden border-b border-clay-800 bg-clay-950 pt-1';

	const tabBase =
		'-mb-px inline-flex items-center gap-1.5 rounded-t-md border-b-2 px-3.5 pt-2 pb-2.5 text-sm font-medium whitespace-nowrap transition-colors focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none';
	const tabActive = 'border-gold-500 text-parchment-200';
	const tabIdle =
		'border-transparent text-parchment-400 hover:bg-clay-900 hover:text-parchment-200';
	const tabBadge =
		'rounded-full border border-gold-800 px-1.5 font-mono text-[0.6875rem] font-semibold text-gold-500';
	let showModal = $state(false);
	let editingTag = $state(null);
	let formName = $state('');
	let error = $state('');
	let savingTag = $state(false);
	let folding = $state(null);
	let splitting = $state(null);
	let counts = $state({ provisional: 0, deleted: 0, coverage: 0, siblings: 0, splitRules: 0 });

	let activeTab = $derived(
		TABS.includes(effectiveUrl().hash.slice(1)) ? effectiveUrl().hash.slice(1) : 'vocabulary'
	);

	const compactButton =
		'rounded p-0.5 text-parchment-500 hover:bg-clay-800 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none';

	async function fetchVocabulary({ limit, offset, query, signal }) {
		const data = await api.tags.list(query.q, limit, offset, signal);
		if (!data) throw new Error('Failed to load tags');
		return { items: data.results ?? [], total: data.total ?? null };
	}

	const vocabulary = createListQuery({
		fetcher: fetchVocabulary,
		codec: tagsQuery,
		sortKeys: ['name', 'document_count'],
		keyField: 'id'
	});

	const draft = new QueryDraft(vocabulary);

	function sortByColumn(key) {
		vocabulary.update(toggleSortState(vocabulary.query, key));
	}

	const columns = [
		{ key: 'name', label: 'Name', sortable: true, width: '100%' },
		{ key: 'document_count', label: 'Documents', sortable: true, width: '100px' },
		{ key: 'created_at', label: 'Created', sortable: false, width: '150px' },
		{ key: 'actions', label: 'Actions', sortable: false, cellClass: 'whitespace-nowrap' }
	];

	function canModerate() {
		return !authStore.authEnabled() || authStore.isEditor();
	}

	function tabClass(tab) {
		return `${tabBase} ${activeTab === tab ? tabActive : tabIdle}`;
	}

	function selectTab(tab) {
		const url = effectiveUrl();
		goto(resolve(`${url.pathname}${url.search}#${tab}`), {
			shallow: true,
			replace: true
		});
	}

	async function refreshCounts() {
		const [provisional, deleted, coverage, splitRules] = await Promise.all([
			api.tags.provisionalList(1, 0),
			api.tags.deletedList({ limit: 1 }),
			api.tags.auditFlags(1, 0),
			api.tags.splitRulesList({ limit: 1 })
		]);
		counts = {
			...counts,
			provisional: provisional.ok ? (provisional.data?.total ?? 0) : 0,
			deleted: deleted?.total ?? 0,
			coverage: coverage?.total ?? 0,
			splitRules: splitRules?.total ?? 0
		};
	}

	// the sibling readings belong to the latest measurement run, so the review actions on this page never
	// change them; the badge is read once per visit instead of on every counts refresh
	async function refreshSiblingCount() {
		const siblings = await api.tags.siblings(1, 0);
		counts = { ...counts, siblings: siblings?.absent ? 0 : (siblings?.total ?? 0) };
	}

	function onReviewChanged() {
		vocabulary.reload();
		refreshCounts();
	}

	function filterByTag(row) {
		const params = new URLSearchParams({ tag: row.name });
		goto(resolve(`documents?${params.toString()}`));
	}

	function openNew() {
		editingTag = null;
		formName = '';
		error = '';
		showModal = true;
	}

	function openEdit(tagId, tagName) {
		editingTag = { id: tagId, name: tagName };
		formName = tagName;
		error = '';
		showModal = true;
	}

	async function save() {
		error = '';
		const name = formName.trim();
		if (!name) {
			error = 'Tag name is required';
			return;
		}
		savingTag = true;
		let result;
		if (editingTag) {
			result = await api.tags.update(editingTag.id, name);
		} else {
			result = await api.tags.create(name);
		}
		savingTag = false;
		if (result.ok) {
			showModal = false;
			vocabulary.reload();
		} else if (result.status === 409) {
			error = 'Tag already exists';
		} else {
			toastStore.error('Failed to save tag');
		}
	}

	async function handleDelete(tagId, tagName) {
		const ok = await confirmStore.confirm({
			title: 'Delete tag',
			message: `Delete tag "${tagName}"?`,
			danger: true
		});
		if (!ok) return;
		await api.tags.delete(tagId);
		vocabulary.reload();
		refreshCounts();
	}

	async function handleDemote(tagId, tagName) {
		const ok = await confirmStore.confirm({
			title: 'Demote tag',
			message: `Demote tag "${tagName}"? It keeps its documents but loses its standing in the vocabulary until it re-qualifies.`
		});
		if (!ok) return;
		const result = await api.tags.demote(tagId);
		if (result.ok) {
			toastStore.success(`Demoted "${tagName}"`);
		} else if (result.status === 409) {
			toastStore.error(result.data?.error ?? 'Tag cannot be demoted');
		} else {
			toastStore.error('Failed to demote tag');
		}
		onReviewChanged();
		refreshCounts();
	}

	async function handleRemoveAlias(aliasName, tagName) {
		const ok = await confirmStore.confirm({
			title: 'Remove alias',
			message: `"${aliasName}" stops resolving to "${tagName}". Its documents stay on "${tagName}", and filters that still use "${aliasName}" stop matching.`
		});
		if (!ok) return;
		const result = await api.tags.removeAlias(aliasName);
		if (result.ok) {
			toastStore.success(`Removed alias "${aliasName}"`);
			vocabulary.reload();
		} else if (result.status === 409) {
			toastStore.error(result.data?.error ?? 'Alias cannot be removed');
		} else {
			toastStore.error('Failed to remove alias');
		}
	}

	async function handlePromoteAlias(aliasName, tagName) {
		const ok = await confirmStore.confirm({
			title: 'Promote alias to tag',
			message: `"${aliasName}" becomes a separate tag. Its documents stay on "${tagName}", so the new tag starts empty.`
		});
		if (!ok) return;
		const result = await api.tags.promoteAlias(aliasName);
		if (result.ok) {
			toastStore.success(`Promoted "${aliasName}" to a tag`);
			onReviewChanged();
		} else if (result.status === 409) {
			toastStore.error(result.data?.error ?? 'Alias cannot be promoted');
		} else {
			toastStore.error('Failed to promote alias');
		}
	}

	onMount(() => {
		if (canModerate()) {
			refreshCounts();
			refreshSiblingCount();
		}
	});
</script>

<div class="mb-5 flex flex-wrap items-start gap-4">
	<div>
		<h1 class="text-[1.625rem] font-semibold tracking-[-0.01em] text-parchment-200">Tags</h1>
		<p class="mt-1.5 text-parchment-500">Curate the shared vocabulary and review tag quality.</p>
	</div>
	{#if canModerate()}
		<div class="ms-auto flex shrink-0 gap-2 pt-1">
			<button
				onclick={openNew}
				class="rounded-lg bg-gold-500 px-4 py-2 text-sm font-medium text-clay-950 hover:bg-gold-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
			>
				+ New Tag
			</button>
		</div>
	{/if}
</div>

<TagReconcileStrip />

<div class={tabStrip} role="tablist" aria-label="Tags sections">
	<button
		id="tab-vocabulary"
		type="button"
		role="tab"
		aria-selected={activeTab === 'vocabulary'}
		aria-controls="panel-vocabulary"
		onclick={() => selectTab('vocabulary')}
		class={tabClass('vocabulary')}
	>
		Vocabulary
	</button>
	{#if canModerate()}
		<button
			id="tab-provisional"
			type="button"
			role="tab"
			aria-selected={activeTab === 'provisional'}
			aria-controls="panel-provisional"
			onclick={() => selectTab('provisional')}
			class={tabClass('provisional')}
		>
			New tags
			{#if counts.provisional}<span class={tabBadge}>{formatNumber(counts.provisional)}</span>{/if}
		</button>
		<button
			id="tab-deleted"
			type="button"
			role="tab"
			aria-selected={activeTab === 'deleted'}
			aria-controls="panel-deleted"
			onclick={() => selectTab('deleted')}
			class={tabClass('deleted')}
		>
			Deleted
			{#if counts.deleted}<span class={tabBadge}>{formatNumber(counts.deleted)}</span>{/if}
		</button>
		<button
			id="tab-split-rules"
			type="button"
			role="tab"
			aria-selected={activeTab === 'split-rules'}
			aria-controls="panel-split-rules"
			onclick={() => selectTab('split-rules')}
			class={tabClass('split-rules')}
		>
			Split rules
			{#if counts.splitRules}<span class={tabBadge}>{formatNumber(counts.splitRules)}</span>{/if}
		</button>
		<button
			id="tab-coverage"
			type="button"
			role="tab"
			aria-selected={activeTab === 'coverage'}
			aria-controls="panel-coverage"
			onclick={() => selectTab('coverage')}
			class={tabClass('coverage')}
		>
			Coverage
			{#if counts.coverage}<span class={tabBadge}>{formatNumber(counts.coverage)}</span>{/if}
		</button>
		<button
			id="tab-siblings"
			type="button"
			role="tab"
			aria-selected={activeTab === 'siblings'}
			aria-controls="panel-siblings"
			onclick={() => selectTab('siblings')}
			class={tabClass('siblings')}
		>
			Overlap
			{#if counts.siblings}<span class={tabBadge}>{formatNumber(counts.siblings)}</span>{/if}
		</button>
	{/if}
</div>

{#if activeTab === 'vocabulary'}
	<div id="panel-vocabulary" role="tabpanel" aria-labelledby="tab-vocabulary">
		{#snippet cell(col, row)}
			{#if col.key === 'name'}
				<div>
					<span>{row.name}</span>
					{#if row.aliases?.length > 0}
						<div class="mt-1 flex flex-wrap items-center gap-1">
							{#each row.aliases as alias (alias.name)}
								<span
									class="inline-flex items-center gap-0.5 rounded-full border border-clay-700 bg-clay-900 py-0.5 ps-2 pe-1 text-[0.6875rem] text-parchment-400"
									title={alias.seeded ? 'seeded alias; the startup seeder restores it' : undefined}
								>
									{alias.name}
									{#if canModerate()}
										<button
											onclick={() => handlePromoteAlias(alias.name, row.name)}
											title="Promote alias {alias.name} to a tag"
											aria-label="Promote alias {alias.name} to a tag"
											class="{compactButton} hover:text-gold-500"><Icon name="promote" /></button
										>
										{#if !alias.seeded}
											<button
												onclick={() => handleRemoveAlias(alias.name, row.name)}
												title="Remove alias {alias.name}"
												aria-label="Remove alias {alias.name}"
												class="{compactButton} hover:text-terracotta-500"
												><Icon name="close" /></button
											>
										{/if}
									{/if}
								</span>
							{/each}
						</div>
					{/if}
				</div>
			{:else if col.key === 'document_count'}
				{String(row.document_count ?? 0)}
			{:else if col.key === 'created_at'}
				{formatDate(row.created_at)}
			{:else if col.key === 'actions'}
				{#if canModerate()}
					<div class="flex items-center">
						{#if row.promoted && !row.seeded}
							<button
								onclick={() => handleDemote(row.id, row.name)}
								title="Demote"
								aria-label="Demote"
								class="{compactButton} hover:text-amber-400"><Icon name="demote" /></button
							>
						{/if}
						{#if !row.seeded}
							<button
								onclick={() =>
									(folding = { name: row.name, document_count: row.document_count ?? 0 })}
								title="Fold into another tag"
								aria-label="Fold into another tag"
								class="{compactButton} hover:text-gold-500"><Icon name="fold" /></button
							>
						{/if}
						{#if row.compound && !row.seeded}
							<button
								onclick={() =>
									(splitting = { name: row.name, document_count: row.document_count ?? 0 })}
								title="Split into components"
								aria-label="Split into components"
								class="{compactButton} hover:text-gold-500"><Icon name="split" /></button
							>
						{/if}
						<button
							onclick={() => openEdit(row.id, row.name)}
							title="Edit"
							aria-label="Edit"
							class="{compactButton} hover:text-gold-500"><Icon name="edit" /></button
						>
						<button
							onclick={() => handleDelete(row.id, row.name)}
							title="Delete"
							aria-label="Delete"
							class="{compactButton} hover:text-terracotta-500"><Icon name="delete" /></button
						>
					</div>
				{/if}
			{:else}
				{row[col.key] ?? ''}
			{/if}
		{/snippet}

		<div class="mb-4 flex flex-wrap items-center gap-3">
			<label for="tag-filter" class="sr-only">Filter tags</label>
			<input
				id="tag-filter"
				name="tag-filter"
				type="text"
				bind:value={draft.value}
				oninput={draft.markEdited}
				onfocus={draft.focus}
				onblur={draft.blur}
				autocomplete="off"
				placeholder="Filter tags…"
				class="w-full max-w-xs rounded-md border border-clay-700 bg-clay-900 px-3 py-1.5 text-sm text-parchment-200 placeholder-parchment-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
			/>
			{#if vocabulary.total != null}
				<span class="ms-auto text-[0.8125rem] text-parchment-500 tabular-nums">
					<b class="font-semibold text-parchment-300">{formatNumber(vocabulary.total)}</b> tags
				</span>
			{/if}
		</div>

		<ListTable
			{columns}
			{cell}
			rows={vocabulary.rows}
			total={vocabulary.total}
			loading={vocabulary.loading}
			error={vocabulary.error}
			page={vocabulary.query.page}
			pageSize={vocabulary.query.pageSize}
			pageSizes={[10, 25, 50, 100]}
			sortBy={vocabulary.query.sortBy}
			sortOrder={vocabulary.query.sortOrder}
			keyField="id"
			onSort={sortByColumn}
			onPage={(next) => vocabulary.update({ page: next })}
			onPageSize={(size) => vocabulary.update({ pageSize: size })}
			onRowClick={filterByTag}
		/>
	</div>
{:else if activeTab === 'provisional'}
	<div id="panel-provisional" role="tabpanel" aria-labelledby="tab-provisional">
		<ProvisionalReview onchanged={onReviewChanged} />
	</div>
{:else if activeTab === 'deleted'}
	<div id="panel-deleted" role="tabpanel" aria-labelledby="tab-deleted">
		<DeletedTags onchanged={onReviewChanged} />
	</div>
{:else if activeTab === 'split-rules'}
	<div id="panel-split-rules" role="tabpanel" aria-labelledby="tab-split-rules">
		<SplitRules onchanged={onReviewChanged} />
	</div>
{:else if activeTab === 'coverage'}
	<div id="panel-coverage" role="tabpanel" aria-labelledby="tab-coverage">
		<CoverageReview onchanged={onReviewChanged} />
	</div>
{:else if activeTab === 'siblings'}
	<div id="panel-siblings" role="tabpanel" aria-labelledby="tab-siblings">
		<SiblingReview />
	</div>
{/if}

<FoldTagModal
	open={folding !== null}
	tag={folding}
	onClose={() => (folding = null)}
	onfolded={() => onReviewChanged()}
/>

<SplitTagModal
	open={splitting !== null}
	tag={splitting}
	onClose={() => (splitting = null)}
	onsplit={() => onReviewChanged()}
/>

<Modal
	open={showModal}
	title={editingTag ? 'Edit Tag' : 'New Tag'}
	onClose={() => (showModal = false)}
>
	<form
		onsubmit={(e) => {
			e.preventDefault();
			save();
		}}
	>
		<div class="space-y-4">
			<div>
				<label for="tag-name" class="mb-1 block text-xs font-medium text-parchment-400">Name</label>
				<input
					id="tag-name"
					name="tag-name"
					type="text"
					bind:value={formName}
					placeholder="Tag name…"
					class="w-full rounded-md border border-clay-700 bg-clay-900 px-3 py-1.5 text-sm text-parchment-200 placeholder-parchment-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
				/>
			</div>
			{#if error}
				<p class="text-sm text-terracotta-500" aria-live="polite">{error}</p>
			{/if}
			<div class="flex justify-end gap-2">
				<button
					type="button"
					onclick={() => (showModal = false)}
					class="rounded-md px-3 py-1.5 text-xs font-medium text-parchment-400 hover:bg-clay-800 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
				>
					Cancel
				</button>
				<button
					type="submit"
					disabled={savingTag}
					class="rounded-md bg-gold-500 px-3 py-1.5 text-xs font-medium text-clay-950 hover:bg-gold-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-50"
				>
					{savingTag ? 'Saving…' : editingTag ? 'Save' : 'Create'}
				</button>
			</div>
		</div>
	</form>
</Modal>
