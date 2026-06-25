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
	import { confirmStore } from '$lib/stores/confirmStore.svelte.js';

	let selectedDocs = $state([]);

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
				`<a href="/api/v1/documents/${row.id}/file?download=true" class="inline-flex items-center justify-center rounded-md p-1.5 text-parchment-500 hover:text-gold-500 hover:bg-clay-800 transition-colors" title="Download PDF">&darr;</a>`
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
		mimeType: '',
		dateCreated: { from: null, to: null },
		dateModified: { from: null, to: null },
		fileSize: { min: null, max: null }
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
			mimeType: f.mimeType,
			dateCreated: f.dateCreated,
			dateModified: f.dateModified,
			fileSize: f.fileSize
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
			mime_type: filter.mimeType || ''
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

	onMount(() => {
		api.autocomplete.peopleTypes().then(setPersonTypes);
		refreshSavedSearches();
	});

	function fetch({ sortBy, sortOrder, limit, offset }) {
		const body = {
			query: filter.query,
			tags: filter.tags,
			people: filter.people,
			document_type: filter.documentType || '',
			language: filter.language || '',
			mime_type: filter.mimeType || '',
			sort_by: sortBy,
			sort_order: sortOrder,
			limit,
			offset
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
				class="rounded-lg bg-gold-600 px-3 py-2 text-sm font-medium text-clay-950 hover:bg-gold-500 shrink-0"
			>
				Download selected ({selectedDocs.length})
			</button>
		{/if}
		<div class="relative flex-1">
			<SearchBar
				query={filter.query}
				tags={filter.tags}
				people={filter.people}
				documentType={filter.documentType}
				language={filter.language}
				mimeType={filter.mimeType}
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
		<button
			onclick={openSaveInput}
			disabled={saving}
			class="rounded-lg border border-clay-800 px-3 py-2 text-sm font-medium text-parchment-400 hover:bg-clay-800 hover:text-parchment-200 disabled:opacity-50"
		>
			{saving ? 'Saving…' : 'Save'}
		</button>
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
									<button
										onclick={() => handleDelete(s.id)}
										class="shrink-0 rounded p-0.5 text-parchment-500 opacity-0 group-hover:opacity-100 hover:text-red-400"
										title="Delete">&times;</button
									>
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
						bind:value={saveName}
						placeholder="e.g. Invoices from Q1"
						class="border-clay-700 placeholder-parchment-600 mb-2 w-full rounded-md border bg-clay-900 px-3 py-1.5 text-sm text-parchment-200 focus:border-gold-500 focus:ring-0 focus:outline-none"
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
	/>
</div>
