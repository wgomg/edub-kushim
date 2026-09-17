<script>
	import { onMount } from 'svelte';
	import { goto, replaceState } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { filterStore } from '$lib/stores/filterStore.js';
	import { defaultFilter } from '$lib/stores/searchFilter.js';
	import DataTable from '$lib/components/DataTable.svelte';
	import Modal from '$lib/components/Modal.svelte';
	import TagReconcileStrip from '$lib/components/tags/TagReconcileStrip.svelte';
	import MergeReview from '$lib/components/tags/MergeReview.svelte';
	import TagMergeSuggestions from '$lib/components/tags/TagMergeSuggestions.svelte';
	import ProvisionalReview from '$lib/components/tags/ProvisionalReview.svelte';
	import DeletedTags from '$lib/components/tags/DeletedTags.svelte';
	import { EDIT_ICON, DELETE_ICON, CHEVRON_ICON, actionButton } from '$lib/icons.js';
	import { escapeHtml, formatNumber } from '$lib/utils/html.js';
	import { confirmStore } from '$lib/stores/confirmStore.svelte.js';
	import { toastStore } from '$lib/stores/toastStore.svelte.js';
	import { api } from '$lib/api';
	import * as authStore from '$lib/stores/authStore.js';

	const TABS = ['vocabulary', 'merges', 'provisional', 'deleted'];

	// main's p-6 is the sticky view rectangle's origin, so -top-6 lets the strip
	// stick flush under the topbar instead of leaving a 24px band over the panel
	const tabStrip =
		'sticky -top-6 z-20 mb-6 flex items-end gap-1 overflow-x-auto border-b border-clay-800 bg-clay-950 pt-1';
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
	let query = $state(new URL(window.location.href).searchParams.get('q') || '');
	let refreshKey = $state(0);
	let deletedRefreshKey = $state(0);
	let savingTag = $state(false);
	let activeTab = $state('vocabulary');
	let vocabularyTotal = $state(null);
	let mergeSuggestions = $state(null);
	let expandedTagId = $state(null);
	let counts = $state({ merges: 0, provisional: 0, deleted: 0 });

	const columns = [
		{
			key: 'name',
			label: 'Name',
			sortable: true,
			width: '100%'
		},
		{
			key: 'document_count',
			label: 'Documents',
			sortable: true,
			cell: (v) => String(v ?? 0),
			width: '100px'
		},
		{
			key: 'actions',
			label: 'Actions',
			sortable: false,
			cellClass: 'whitespace-nowrap',
			cell: (_v, row) => {
				if (!authStore.authEnabled() || !authStore.isEditor()) return '';
				const safeName = escapeHtml(row.name);
				const actionable =
					'text-parchment-400 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none';
				const expanded = expandedTagId === row.id;
				const chevron = `transition-transform focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none ${expanded ? 'rotate-90 text-gold-500' : 'text-parchment-400 hover:text-gold-500'}`;
				return `${actionButton(CHEVRON_ICON, 'Merge suggestions', chevron, { 'data-toggle-merge': row.id })}
${actionButton(EDIT_ICON, 'Edit', `${actionable} hover:text-gold-500`, { 'data-edit-tag': row.id, 'data-tag-name': safeName })}
${actionButton(DELETE_ICON, 'Delete', `${actionable} hover:text-terracotta-500`, { 'data-delete-tag': row.id, 'data-tag-name': safeName })}`;
			}
		}
	];

	function canModerate() {
		return !authStore.authEnabled() || authStore.isEditor();
	}

	function tabClass(tab) {
		return `${tabBase} ${activeTab === tab ? tabActive : tabIdle}`;
	}

	function selectTab(tab) {
		activeTab = tab;
		const url = new URL(window.location.href);
		replaceState(resolve(`${url.pathname}${url.search}#${tab}`));
	}

	async function refreshCounts() {
		const [merges, provisional, deleted] = await Promise.all([
			api.tags.mergeSuggestions(50, { includeDismissed: true }),
			api.tags.provisionalList(1, 0),
			api.tags.deletedList({ limit: 1 })
		]);
		const mergeList = merges.ok ? (merges.data?.results ?? []) : null;
		mergeSuggestions = mergeList;
		counts = {
			merges: mergeList ? mergeList.filter((s) => !s.dismissed).length : 0,
			provisional: provisional.ok ? (provisional.data?.total ?? 0) : 0,
			deleted: deleted?.total ?? 0
		};
	}

	function onReviewChanged() {
		refreshKey++;
		deletedRefreshKey++;
		refreshCounts();
	}

	function filterByTag(row) {
		filterStore.set({ ...defaultFilter, tags: [row.name] });
		goto(resolve('/documents'));
	}

	async function fetchVocabulary({ limit, offset }) {
		return await api.tags.list(query, limit, offset);
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
			refreshKey++;
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
		refreshKey++;
		deletedRefreshKey++;
		refreshCounts();
	}

	function handlePageClick(e) {
		const toggleBtn = e.target.closest('[data-toggle-merge]');
		if (toggleBtn) {
			const id = parseInt(toggleBtn.getAttribute('data-toggle-merge'));
			expandedTagId = expandedTagId === id ? null : id;
			return;
		}
		const editBtn = e.target.closest('[data-edit-tag]');
		if (editBtn) {
			const id = parseInt(editBtn.getAttribute('data-edit-tag'));
			const name = editBtn.getAttribute('data-tag-name');
			openEdit(id, name);
			return;
		}
		const deleteBtn = e.target.closest('[data-delete-tag]');
		if (deleteBtn) {
			const id = parseInt(deleteBtn.getAttribute('data-delete-tag'));
			const name = deleteBtn.getAttribute('data-tag-name');
			handleDelete(id, name);
			return;
		}
	}

	onMount(() => {
		const hash = window.location.hash.slice(1);
		if (TABS.includes(hash)) activeTab = hash;
		if (canModerate()) refreshCounts();
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
			id="tab-merges"
			type="button"
			role="tab"
			aria-selected={activeTab === 'merges'}
			aria-controls="panel-merges"
			onclick={() => selectTab('merges')}
			class={tabClass('merges')}
		>
			Merge review
			{#if counts.merges}<span class={tabBadge}>{formatNumber(counts.merges)}</span>{/if}
		</button>
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
	{/if}
</div>

{#if activeTab === 'vocabulary'}
	<div id="panel-vocabulary" role="tabpanel" aria-labelledby="tab-vocabulary">
		<div class="mb-4 flex flex-wrap items-center gap-3">
			<label for="tag-filter" class="sr-only">Filter tags</label>
			<input
				id="tag-filter"
				name="tag-filter"
				type="text"
				bind:value={query}
				oninput={() => {
					refreshKey++;
					const url = new URL(window.location.href);
					if (query) url.searchParams.set('q', query);
					else url.searchParams.delete('q');
					goto(resolve(`${url.pathname}${url.search}${url.hash}`), {
						replaceState: true,
						keepFocus: true
					});
				}}
				autocomplete="off"
				placeholder="Filter tags…"
				class="w-full max-w-xs rounded-md border border-clay-700 bg-clay-900 px-3 py-1.5 text-sm text-parchment-200 placeholder-parchment-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
			/>
			{#if vocabularyTotal != null}
				<span class="ms-auto text-[0.8125rem] text-parchment-500 tabular-nums">
					<b class="font-semibold text-parchment-300">{formatNumber(vocabularyTotal)}</b> tags
				</span>
			{/if}
		</div>

		<DataTable
			{columns}
			fetch={fetchVocabulary}
			{refreshKey}
			defaultSortBy="document_count"
			defaultSortOrder="desc"
			urlSync="tags"
			onRowClick={filterByTag}
			onActionClick={handlePageClick}
			onTotal={(t) => (vocabularyTotal = t)}
			expandedKey={expandedTagId}
			{expansion}
		/>
	</div>
{:else if activeTab === 'merges'}
	<div id="panel-merges" role="tabpanel" aria-labelledby="tab-merges">
		<MergeReview initialSuggestions={mergeSuggestions} onchanged={onReviewChanged} />
	</div>
{:else if activeTab === 'provisional'}
	<div id="panel-provisional" role="tabpanel" aria-labelledby="tab-provisional">
		<ProvisionalReview onchanged={onReviewChanged} />
	</div>
{:else if activeTab === 'deleted'}
	<div id="panel-deleted" role="tabpanel" aria-labelledby="tab-deleted">
		<DeletedTags refreshKey={deletedRefreshKey} onchanged={onReviewChanged} />
	</div>
{/if}

{#snippet expansion(row)}
	<TagMergeSuggestions tag={row} onchanged={onReviewChanged} />
{/snippet}

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
