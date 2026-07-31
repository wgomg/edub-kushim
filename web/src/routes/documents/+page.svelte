<script>
	import { goto } from '$app/navigation';
	import { api } from '$lib/api';
	import DataTable from '$lib/components/DataTable.svelte';
	import SearchBar from '$lib/components/SearchBar.svelte';
	import FilterPanel from '$lib/components/FilterPanel.svelte';
	import { filterStore } from '$lib/stores/filterStore.js';
	import { setPersonTypes } from '$lib/stores/searchFilter.js';
	import { onMount } from 'svelte';
	import { escapeHtml } from '$lib/utils/html.js';
	import { DOWNLOAD_ICON } from '$lib/icons.js';
	import { confirmStore } from '$lib/stores/confirmStore.svelte.js';
	import { toastStore } from '$lib/stores/toastStore.svelte.js';
	import * as authStore from '$lib/stores/authStore.js';

	let selectedDocs = $state([]);

	let batchTagMode = $state('add');
	let batchTagIds = $state([]);
	let tagOptions = $state([]);
	let tagSearchQuery = $state('');
	let showTagPicker = $state(false);
	let tagSearchTimer;

	let columns = $derived.by(() => {
		const cols = [
			{
				key: 'title',
				label: 'Title',
				sortable: true,
				cell: (v, row) =>
					`<a href="/documents/${row.id}" class="underline decoration-parchment-500/30 underline-offset-2 hover:text-gold-500">${escapeHtml(v)}</a>`
			}
		];

		if (filter.query) {
			cols.push({
				key: 'snippet',
				label: 'Snippet',
				sortable: false,
				cell: (v) =>
					v
						? `<span class="text-parchment-300 text-xs leading-relaxed [&>b]:text-gold-500 [&>b]:font-semibold">${v}</span>`
						: '<span class="text-parchment-500 italic">—</span>',
				width: '40%'
			});
		}

		cols.push(
			{
				key: 'tags',
				label: 'Tags',
				sortable: false,
				cell: (v) => {
					if (!v || v.length === 0) return '<span class="text-parchment-500 italic">—</span>';
					return v
						.map(
							(t) =>
								`<span class="inline-block rounded-full bg-clay-800 px-2 py-0.5 text-xs text-parchment-300">${escapeHtml(t.name)}</span>`
						)
						.join(' ');
				},
				minWidth: '200px'
			},
			{
				key: 'people',
				label: 'People',
				sortable: false,
				cell: (v) => {
					if (!v || v.length === 0) return '<span class="text-parchment-500 italic">—</span>';
					const grouped = {};
					for (const p of v) {
						const type = p.person_type_name || 'Unknown';
						if (!grouped[type]) grouped[type] = [];
						grouped[type].push(escapeHtml(p.name));
					}
					return Object.entries(grouped)
						.map(
							([type, names]) =>
								`<span class="text-parchment-400 text-xs">${escapeHtml(type)}:</span> <span class="text-parchment-200">${names.join(', ')}</span>`
						)
						.join('<br>');
				},
				minWidth: '250px'
			},
			{
				key: 'file_size',
				label: 'Size',
				sortable: true,
				cell: (v) => `${(v / 1024).toFixed(0)} KB`,
				minWidth: '100px'
			},
			{
				key: 'created_at',
				label: 'Created',
				sortable: true,
				cell: (v) => new Date(v).toLocaleDateString(),
				minWidth: '150px'
			},
			{
				key: '_actions',
				label: '',
				sortable: false,
				width: '50px',
				cell: (_, row) =>
					`<a href="/api/v1/documents/${row.id}/file?download=true" class="inline-flex items-center justify-center rounded-md p-1.5 text-parchment-500 hover:text-gold-500 hover:bg-clay-800 transition-colors" title="Download PDF">${DOWNLOAD_ICON}</a>`
			}
		);

		return cols;
	});

	let filter = $state({
		query: '',
		tags: [],
		people: [],
		documentType: '',
		language: '',
		dateCreated: { from: null, to: null },
		dateModified: { from: null, to: null },
		fileSize: { min: null, max: null },
		missingLanguage: false,
		missingType: false,
		untagged: false
	});
	let showFilters = $state(false);
	let refreshKey = $state(0);
	let savedSearches = $state([]);
	let showSaved = $state(false);
	let saving = $state(false);
	let showNameInput = $state(false);
	let saveName = $state('');

	let subscribed = false;
	filterStore.subscribe((f) => {
		filter = {
			query: f.query,
			tags: f.tags,
			people: f.people,
			documentType: f.documentType,
			language: f.language,
			dateCreated: f.dateCreated,
			dateModified: f.dateModified,
			fileSize: f.fileSize,
			missingLanguage: f.missingLanguage,
			missingType: f.missingType,
			untagged: f.untagged
		};
		if (subscribed) refreshKey++;
		subscribed = true;
	});

	function handleSearch(partial) {
		filterStore.setPartial(partial);
	}

	function buildFilterBody() {
		const body = {
			query: filter.query,
			tags: filter.tags,
			people: filter.people,
			document_type: filter.documentType || '',
			language: filter.language || '',
			missing_language: filter.missingLanguage || false,
			missing_type: filter.missingType || false,
			untagged: filter.untagged || false
		};
		const dc = filter.dateCreated;
		if (dc.from || dc.to) {
			body.date_created = { from: dc.from || null, to: dc.to || null };
		}
		const dm = filter.dateModified;
		if (dm.from || dm.to) {
			body.date_modified = { from: dm.from || null, to: dm.to || null };
		}
		const fs = filter.fileSize;
		if (fs.min != null || fs.max != null) {
			body.file_size = { min: fs.min, max: fs.max };
		}
		return body;
	}

	async function refreshSavedSearches() {
		savedSearches = await api.savedSearches.list();
	}

	function openSaveInput() {
		saveName = '';
		showNameInput = true;
		showSaved = false;
	}

	async function submitSave() {
		const name = saveName.trim();
		if (!name) return;
		saving = true;
		showNameInput = false;
		await api.savedSearches.create(name, buildFilterBody());
		saving = false;
		await refreshSavedSearches();
	}

	function handleLoad(s) {
		const filterData = JSON.parse(s.filter_json);
		filterStore.setPartial(filterData);
		showSaved = false;
	}

	async function handleDelete(id) {
		const ok = await confirmStore.confirm({
			title: 'Delete saved search',
			message: 'Delete this saved search?',
			danger: true
		});
		if (!ok) return;
		await api.savedSearches.delete(id);
		await refreshSavedSearches();
	}

	async function handleBatchDelete() {
		const ok = await confirmStore.confirm({
			title: 'Delete documents',
			message: `Delete ${selectedDocs.length} document(s)? This action cannot be undone.`,
			danger: true
		});
		if (!ok) return;
		const res = await api.documents.batchDelete(selectedDocs.map((r) => r.id));
		if (!res.ok) {
			toastStore.error(res.data?.error || 'Batch delete failed');
			return;
		}
		if (res.data?.failed?.length > 0) {
			const failed = res.data.failed;
			toastStore.warning(
				`Deleted ${res.data.deleted} of ${selectedDocs.length} documents. ${failed.length} failed.`
			);
		}
		refreshKey++;
	}

	function onTagSearchInput(e) {
		tagSearchQuery = e.target.value;
		clearTimeout(tagSearchTimer);
		tagSearchTimer = setTimeout(async () => {
			tagOptions = await api.autocomplete.tags(tagSearchQuery, 20);
		}, 200);
	}

	async function handleBatchAssign() {
		if (batchTagIds.length === 0) return;
		const res = await api.documents.batchAssignTags(
			selectedDocs.map((r) => r.id),
			batchTagIds,
			batchTagMode
		);
		if (!res.ok) {
			toastStore.error(res.data?.error || 'Batch tag assignment failed');
			return;
		}
		if (res.data?.failed?.length > 0) {
			const failed = res.data.failed;
			toastStore.warning(
				`Tagged ${res.data.assigned} of ${selectedDocs.length} documents. ${failed.length} failed.`
			);
		}
		batchTagIds = [];
		showTagPicker = false;
		refreshKey++;
	}

	onMount(() => {
		api.autocomplete.peopleTypes().then(setPersonTypes);
		refreshSavedSearches();
	});

	function fetch({ sortBy, sortOrder, limit, offset }) {
		const body = {
			...buildFilterBody(),
			sort_by: sortBy,
			sort_order: sortOrder,
			limit,
			offset
		};
		return api.documents.searchStructured(body);
	}

	function view(row) {
		goto(`/documents/${row.id}`);
	}
</script>

<div class="flex flex-col gap-4">
	<div class="flex items-center gap-3">
		{#if selectedDocs.length > 0}
			<button
				onclick={() => api.documents.downloadBatch(selectedDocs.map((r) => r.id))}
				class="shrink-0 rounded-lg bg-gold-600 px-3 py-2 text-sm font-medium text-clay-950 hover:bg-gold-500"
			>
				Download selected ({selectedDocs.length})
			</button>
			{#if !authStore.authEnabled() || authStore.isEditor()}
				<button
					onclick={handleBatchDelete}
					class="shrink-0 rounded-lg bg-terracotta-700 px-3 py-2 text-sm font-medium text-parchment-200 hover:bg-terracotta-600"
				>
					Delete selected ({selectedDocs.length})
				</button>
			{/if}
			{#if !authStore.authEnabled() || authStore.isEditor()}
				<div class="relative shrink-0">
					<button
						onclick={() => (showTagPicker = !showTagPicker)}
						class="rounded-lg border border-clay-800 px-3 py-2 text-sm font-medium text-parchment-400 hover:bg-clay-800 hover:text-parchment-200"
					>
						{batchTagIds.length > 0 ? `Tags (${batchTagIds.length})` : 'Assign tags'}
					</button>
					{#if showTagPicker}
						<div
							class="absolute top-full left-0 z-30 mt-1 w-72 rounded-lg border border-clay-800 bg-clay-950 shadow-xl"
						>
							<div class="p-3">
								<input
									type="text"
									placeholder="Search tags…"
									oninput={onTagSearchInput}
									class="mb-2 w-full rounded-md border border-clay-700 bg-clay-900 px-3 py-1.5 text-sm text-parchment-200 placeholder-parchment-600 focus:border-gold-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-gold-500"
									aria-label="Search tags"
								/>
								<div class="mb-2 max-h-40 overflow-y-auto">
									{#each tagOptions as tag (tag.id)}
										<label
											class="flex cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 hover:bg-clay-800"
										>
											<input
												type="checkbox"
												checked={batchTagIds.includes(tag.id)}
												onchange={() => {
													if (batchTagIds.includes(tag.id)) {
														batchTagIds = batchTagIds.filter((t) => t !== tag.id);
													} else {
														batchTagIds = [...batchTagIds, tag.id];
													}
												}}
												class="accent-gold-500"
											/>
											<span class="text-sm text-parchment-200">{tag.name}</span>
										</label>
									{/each}
									{#if tagOptions.length === 0 && tagSearchQuery}
										<p class="px-2 py-3 text-center text-xs text-parchment-500">No tags found</p>
									{/if}
								</div>
								{#if batchTagIds.length > 0}
									<div class="mb-2 flex flex-wrap gap-1">
										{#each batchTagIds as tid}
											{@const tag = tagOptions.find((t) => t.id === tid)}
											{#if tag}
												<span
													class="inline-flex items-center gap-1 rounded-full bg-clay-800 px-2 py-0.5 text-xs text-parchment-300"
												>
													{tag.name}
													<button
														onclick={() => (batchTagIds = batchTagIds.filter((t) => t !== tid))}
														class="text-parchment-500 hover:text-parchment-200"
														aria-label="Remove tag">&times;</button
													>
												</span>
											{/if}
										{/each}
									</div>
								{/if}
								<div class="mb-3 flex gap-2">
									<button
										onclick={() => (batchTagMode = 'add')}
										class={`flex-1 rounded-md px-2 py-1 text-xs font-medium ${
											batchTagMode === 'add'
												? 'bg-gold-600 text-clay-950'
												: 'border border-clay-800 text-parchment-400 hover:bg-clay-800'
										}`}
									>
										Add
									</button>
									<button
										onclick={() => (batchTagMode = 'replace')}
										class={`flex-1 rounded-md px-2 py-1 text-xs font-medium ${
											batchTagMode === 'replace'
												? 'bg-gold-600 text-clay-950'
												: 'border border-clay-800 text-parchment-400 hover:bg-clay-800'
										}`}
									>
										Replace
									</button>
								</div>
								<button
									onclick={handleBatchAssign}
									disabled={batchTagIds.length === 0}
									class="w-full rounded-md bg-gold-600 px-3 py-1.5 text-xs font-medium text-clay-950 hover:bg-gold-500 disabled:opacity-50"
								>
									Assign
								</button>
							</div>
						</div>
					{/if}
				</div>
			{/if}
		{/if}
		<div class="relative flex-1">
			<SearchBar
				query={filter.query}
				tags={filter.tags}
				people={filter.people}
				documentType={filter.documentType}
				language={filter.language}
				dateCreated={filter.dateCreated}
				dateModified={filter.dateModified}
				fileSize={filter.fileSize}
				onSearch={handleSearch}
			/>
		</div>
		<button
			onclick={() => (showFilters = !showFilters)}
			class="rounded-lg border border-clay-800 px-3 py-2 text-sm font-medium text-parchment-400 hover:bg-clay-800 hover:text-parchment-200"
		>
			{showFilters ? 'Hide Filters' : 'Filters'}
		</button>
		{#if !authStore.authEnabled() || authStore.isEditor()}
			<button
				onclick={openSaveInput}
				disabled={saving}
				class="rounded-lg border border-clay-800 px-3 py-2 text-sm font-medium text-parchment-400 hover:bg-clay-800 hover:text-parchment-200 disabled:opacity-50"
			>
				{saving ? 'Saving…' : 'Save'}
			</button>
		{/if}
		<div class="relative">
			<button
				onclick={() => (showSaved = !showSaved)}
				class="rounded-lg border border-clay-800 px-3 py-2 text-sm font-medium text-parchment-400 hover:bg-clay-800 hover:text-parchment-200"
			>
				Saved
			</button>
			{#if showSaved}
				<div
					class="absolute top-full right-0 z-30 mt-1 w-72 rounded-lg border border-clay-800 bg-clay-950 shadow-xl"
				>
					<div class="max-h-64 overflow-y-auto p-2">
						{#if savedSearches.length === 0}
							<p class="px-2 py-3 text-center text-xs text-parchment-500">No saved searches</p>
						{:else}
							{#each savedSearches as s (s.id)}
								<div class="group flex items-center gap-2 rounded-md px-2 py-1.5 hover:bg-clay-800">
									<button
										onclick={() => handleLoad(s)}
										class="min-w-0 flex-1 truncate text-left text-sm text-parchment-200"
									>
										{s.name}
									</button>
									{#if !authStore.authEnabled() || authStore.isEditor()}
										<button
											onclick={() => handleDelete(s.id)}
											class="shrink-0 rounded p-0.5 text-parchment-500 opacity-0 group-hover:opacity-100 hover:text-red-400"
											title="Delete"
											aria-label="Delete saved search">&times;</button
										>
									{/if}
								</div>
							{/each}
						{/if}
					</div>
				</div>
			{/if}
		</div>
	</div>

	{#if showNameInput}
		<div class="relative z-20">
			<div
				class="absolute top-0 right-0 w-80 rounded-lg border border-clay-800 bg-clay-950 p-4 shadow-xl"
			>
				<p class="mb-2 text-xs font-medium text-parchment-400">Name this search</p>
				<form
					onsubmit={(e) => {
						e.preventDefault();
						submitSave();
					}}
				>
					<input
						type="text"
						id="save-search-name"
						bind:value={saveName}
						placeholder="e.g. Invoices from Q1"
						class="mb-2 w-full rounded-md border border-clay-700 bg-clay-900 px-3 py-1.5 text-sm text-parchment-200 placeholder-parchment-600 focus:border-gold-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-gold-500"
					/>
					<div class="flex justify-end gap-2">
						<button
							type="button"
							onclick={() => (showNameInput = false)}
							class="rounded-md px-3 py-1 text-xs font-medium text-parchment-400 hover:bg-clay-800"
						>
							Cancel
						</button>
						<button
							type="submit"
							disabled={!saveName.trim()}
							class="rounded-md bg-gold-600 px-3 py-1 text-xs font-medium text-clay-950 hover:bg-gold-500 disabled:opacity-50"
						>
							Save
						</button>
					</div>
				</form>
			</div>
		</div>
	{/if}

	{#if showFilters}
		<FilterPanel />
	{/if}

	<DataTable
		{columns}
		{fetch}
		onRowClick={view}
		title="Documents"
		{refreshKey}
		selectable={true}
		onselectionchange={(rows) => (selectedDocs = rows)}
		defaultSortBy="created_at"
		defaultSortOrder="desc"
	/>
</div>
