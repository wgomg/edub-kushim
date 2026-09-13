<script>
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { filterStore } from '$lib/stores/filterStore.js';
	import { defaultFilter } from '$lib/stores/searchFilter.js';
	import DataTable from '$lib/components/DataTable.svelte';
	import Modal from '$lib/components/Modal.svelte';
	import { EDIT_ICON, DELETE_ICON, actionButton } from '$lib/icons.js';
	import { escapeHtml } from '$lib/utils/html.js';
	import { confirmStore } from '$lib/stores/confirmStore.svelte.js';
	import { toastStore } from '$lib/stores/toastStore.svelte.js';
	import { api } from '$lib/api';
	import * as authStore from '$lib/stores/authStore.js';
	import { formatNumber, documentsLabel } from '$lib/utils/html.js';
	import TagReconcileCard from '$lib/components/TagReconcileCard.svelte';

	let showModal = $state(false);
	let editingTag = $state(null);
	let formName = $state('');
	let error = $state('');
	let query = $state(new URL(window.location.href).searchParams.get('q') || '');
	let refreshKey = $state(0);
	let savingTag = $state(false);
	let showMergeSuggestions = $state(false);
	let mergeSuggestions = $state([]);
	let loadingSuggestions = $state(false);
	let suggestionsError = $state('');
	let mergingPair = $state(null);
	let dismissedPairs = $state(new Set());
	let survivorByPair = $state({});

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
			noUnderline: true,
			cellClass: 'whitespace-nowrap',
			cell: (_v, row) => {
				if (!authStore.authEnabled() || !authStore.isEditor()) return '';
				const safeName = escapeHtml(row.name);
				return `${actionButton(EDIT_ICON, 'Edit', 'text-parchment-400 hover:text-gold-500', { 'data-edit-tag': row.id, 'data-tag-name': safeName })}
${actionButton(DELETE_ICON, 'Delete', 'text-parchment-400 hover:text-terracotta-500', { 'data-delete-tag': row.id, 'data-tag-name': safeName })}`;
			}
		}
	];

	function filterByTag(row) {
		filterStore.set({ ...defaultFilter, tags: [row.name] });
		goto(resolve('/documents'));
	}

	async function fetch({ limit, offset }) {
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
	}

	function handlePageClick(e) {
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

	function pairKey(s) {
		return `${s.tags[0].id}-${s.tags[1].id}`;
	}

	async function toggleMergeSuggestions() {
		showMergeSuggestions = !showMergeSuggestions;
		if (!showMergeSuggestions) return;
		loadingSuggestions = true;
		suggestionsError = '';
		const result = await api.tags.mergeSuggestions(20);
		if (result.ok) {
			mergeSuggestions = result.data?.results ?? [];
		} else {
			mergeSuggestions = [];
			suggestionsError =
				result.status === 503
					? 'Matcher unavailable — merge review is offline.'
					: 'Failed to load merge suggestions.';
		}
		loadingSuggestions = false;
	}

	function visibleSuggestions() {
		return mergeSuggestions.filter((s) => !dismissedPairs.has(pairKey(s)));
	}

	async function handleMerge(s) {
		const key = pairKey(s);
		const survivorId = survivorByPair[key] ?? s.tags[0].id;
		const survivor = s.tags.find((t) => t.id === survivorId);
		const deleted = s.tags.find((t) => t.id !== survivorId);
		const ok = await confirmStore.confirm({
			title: 'Merge tags',
			message: `Delete tag "${deleted.name}" and move its documents into "${survivor.name}"?`,
			danger: true
		});
		if (!ok) return;
		mergingPair = key;
		const result = await api.tags.merge(deleted.id, survivor.id);
		mergingPair = null;
		if (result.ok) {
			mergeSuggestions = mergeSuggestions.filter((x) => pairKey(x) !== key);
			refreshKey++;
			toastStore.success(`Merged "${deleted.name}" into "${survivor.name}"`);
		} else {
			toastStore.error('Failed to merge tags');
		}
	}

	function dismiss(s) {
		const key = pairKey(s);
		dismissedPairs = new Set([...dismissedPairs, key]);
	}
</script>

<div class="space-y-4">
	<div class="flex items-center justify-between">
		<h1 class="text-2xl font-semibold text-balance text-parchment-200">Tags</h1>
		<div class="flex items-center gap-2">
			{#if !authStore.authEnabled() || authStore.isEditor()}
				<button
					onclick={toggleMergeSuggestions}
					class="rounded-lg border border-clay-700 px-4 py-2 text-sm font-medium text-parchment-200 hover:border-gold-500 hover:text-gold-400 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
				>
					{showMergeSuggestions ? 'Hide merges' : 'Review merges'}
				</button>
				<button
					onclick={openNew}
					class="rounded-lg bg-gold-500 px-4 py-2 text-sm font-medium text-clay-950 hover:bg-gold-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
				>
					New Tag
				</button>
			{/if}
		</div>
	</div>

	<TagReconcileCard />

	<div class="flex items-center gap-2">
		<label for="tag-filter" class="sr-only">Filter tags</label>
		<input
			id="tag-filter"
			type="text"
			bind:value={query}
			oninput={() => {
				refreshKey++;
				const url = new URL(window.location.href);
				if (query) url.searchParams.set('q', query);
				else url.searchParams.delete('q');
				goto(resolve(url.pathname + url.search), { replaceState: true, keepFocus: true });
			}}
			name="tag-filter"
			autocomplete="off"
			placeholder="Filter tags…"
			class="w-full max-w-xs rounded-md border border-clay-700 bg-clay-900 px-3 py-1.5 text-sm text-parchment-200 placeholder-parchment-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
		/>
	</div>

	{#if showMergeSuggestions}
		<div class="rounded-lg border border-clay-700 bg-clay-900 p-4">
			<h2 class="mb-3 text-sm font-semibold text-parchment-200">Merge suggestions</h2>
			{#if loadingSuggestions}
				<p class="text-sm text-parchment-500">Loading merge suggestions…</p>
			{:else if suggestionsError}
				<p class="text-sm text-terracotta-500" role="alert">{suggestionsError}</p>
			{:else if visibleSuggestions().length === 0}
				<p class="text-sm text-parchment-500">No merge suggestions.</p>
			{:else}
				<ul class="space-y-3">
					{#each visibleSuggestions() as s (pairKey(s))}
						{@const key = pairKey(s)}
						{@const survivorId = survivorByPair[key] ?? s.tags[0].id}
						<li class="rounded-md border border-clay-800 bg-clay-950 p-3">
							<div class="flex flex-wrap items-center justify-between gap-3">
								<div class="min-w-0">
									<div class="flex flex-wrap items-center gap-2">
										<span class="text-sm font-medium text-parchment-200">{s.tags[0].name}</span>
										<span class="text-xs text-parchment-500">
											· {documentsLabel(s.tags[0].document_count)}
										</span>
										<span class="text-parchment-600" aria-hidden="true">↔</span>
										<span class="text-sm font-medium text-parchment-200">{s.tags[1].name}</span>
										<span class="text-xs text-parchment-500">
											· {documentsLabel(s.tags[1].document_count)}
										</span>
										<span
											class="rounded bg-clay-800 px-1.5 py-0.5 text-xs text-gold-400"
											title="Embedding similarity"
										>
											{Math.round(s.similarity * 100)}%
										</span>
										{#if s.confidence === 'population'}
											<span
												class="rounded bg-clay-800 px-1.5 py-0.5 text-xs text-parchment-300"
												title="This pair has usage history from past enrichment runs"
											>
												backed by usage data
											</span>
										{:else}
											<span
												class="rounded bg-clay-800 px-1.5 py-0.5 text-xs text-parchment-500"
												title="No usage data yet — suggested by name similarity alone"
											>
												similarity only
											</span>
										{/if}
									</div>
									<div class="mt-1 text-xs text-parchment-500">
										{#if s.confidence === 'population'}
											{documentsLabel(s.document_overlap)} carry both tags;
											{formatNumber(s.variant_count + s.related_count)} automatic checks judged them related
											rather than the same concept; {formatNumber(s.same_count)} judged them the same
										{:else}
											no usage data yet — suggested by name similarity alone
										{/if}
									</div>
								</div>
								<div class="flex shrink-0 items-center gap-2">
									<span class="text-xs text-parchment-500">Keep:</span>
									<select
										value={survivorId}
										onchange={(e) => (survivorByPair[key] = parseInt(e.currentTarget.value, 10))}
										class="rounded-md border border-clay-700 bg-clay-900 px-2 py-1 text-xs text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
										aria-label="Surviving tag"
									>
										{#each s.tags as t (t.id)}
											<option value={t.id}>{t.name}</option>
										{/each}
									</select>
									<button
										onclick={() => handleMerge(s)}
										disabled={mergingPair === key}
										class="rounded-md bg-gold-500 px-3 py-1 text-xs font-medium text-clay-950 hover:bg-gold-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-50"
									>
										{mergingPair === key ? 'Merging…' : 'Merge'}
									</button>
									<button
										onclick={() => dismiss(s)}
										class="rounded-md px-2 py-1 text-xs text-parchment-400 hover:bg-clay-800 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
									>
										Dismiss
									</button>
								</div>
							</div>
						</li>
					{/each}
				</ul>
			{/if}
		</div>
	{/if}

	<DataTable
		{columns}
		{fetch}
		title=""
		defaultPageSize={50}
		pageSizes={[10, 25, 50, 100]}
		{refreshKey}
		onRowClick={filterByTag}
		onActionClick={handlePageClick}
		urlSync="tags"
	/>
</div>

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
