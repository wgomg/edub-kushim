<script>
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { SvelteSet } from 'svelte/reactivity';
	import { api } from '#lib/api.js';
	import Icon from '#lib/components/Icon.svelte';
	import ListGrid from '#lib/components/ListGrid.svelte';
	import ListTable from '#lib/components/ListTable.svelte';
	import SearchBar from '#lib/components/SearchBar.svelte';
	import FilterPanel from '#lib/components/FilterPanel.svelte';
	import { createListQuery, toggleSortState } from '#lib/list/listQuery.svelte.js';
	import { formatDate, formatSize } from '#lib/utils/html.js';
	import { confirmStore } from '#lib/stores/confirmStore.svelte.js';
	import { toastStore } from '#lib/stores/toastStore.svelte.js';
	import * as authStore from '#lib/stores/authStore.svelte.js';
	import {
		documentsQuery,
		normalizeSavedFilter,
		PERSON_ANY_TYPE,
		savedFilter,
		searchRequestBody,
		snippetSegments
	} from './query.js';

	let batchTagMode = $state('add');
	let batchTagIds = $state([]);
	let tagOptions = $state([]);
	let tagSearchQuery = $state('');
	let showTagPicker = $state(false);
	let tagSearchTimer;

	let showTypePicker = $state(false);
	let batchDocTypeId = $state(null);
	let documentTypeOptions = $state([]);

	let tagPickerEl = $state(null);
	let typePickerEl = $state(null);
	let savedEl = $state(null);

	let showFilters = $state(false);
	let savedSearches = $state([]);
	let showSaved = $state(false);
	let saving = $state(false);
	let showNameInput = $state(false);
	let saveName = $state('');

	let personTypes = $state([]);
	let failedThumbs = new SvelteSet();

	let viewMode = $state(
		typeof localStorage !== 'undefined' && localStorage.getItem('edub:documents:view') === 'grid'
			? 'grid'
			: 'table'
	);

	async function fetchDocuments({ limit, offset, sortBy, sortOrder, query, signal }) {
		const data = await api.documents.searchStructured(
			{
				...searchRequestBody(query),
				sort_by: sortBy,
				sort_order: sortOrder,
				limit,
				offset
			},
			signal
		);
		if (!data) throw new Error('Failed to load documents');
		return { items: data.results ?? [], total: data.total ?? null };
	}

	const documents = createListQuery({
		fetcher: fetchDocuments,
		codec: documentsQuery,
		sortKeys: ['title', 'page_count', 'file_size', 'created_at'],
		keyField: 'id'
	});

	const selectedDocs = $derived(documents.selectedRows);

	$effect(() => {
		if (typeof localStorage !== 'undefined') {
			localStorage.setItem('edub:documents:view', viewMode);
		}
	});

	const columns = $derived.by(() => {
		const cols = [{ key: 'title', label: 'Title', sortable: true }];

		if (documents.query.q) {
			cols.push({ key: 'snippet', label: 'Snippet', sortable: false, width: '40%' });
		}

		cols.push(
			{ key: 'tags', label: 'Tags', sortable: false, minWidth: '200px' },
			{ key: 'people', label: 'People', sortable: false, minWidth: '250px' },
			{
				key: 'page_count',
				label: 'Pages',
				sortable: true,
				cellClass: 'tabular-nums',
				minWidth: '70px'
			},
			{ key: 'file_size', label: 'Size', sortable: true, minWidth: '100px' },
			{ key: 'created_at', label: 'Created', sortable: true, minWidth: '150px' },
			{ key: '_actions', label: '', sortable: false, width: '50px' }
		);

		return cols;
	});

	const sortOptions = [
		{ value: 'created_at', label: 'Created' },
		{ value: 'title', label: 'Title' },
		{ value: 'file_size', label: 'Size' },
		{ value: 'page_count', label: 'Pages' }
	];

	function handleSearch(partial) {
		documents.update(partial);
	}

	function sortByColumn(key) {
		documents.update(toggleSortState(documents.query, key));
	}

	function toggleSortOrder() {
		documents.update(toggleSortState(documents.query, documents.query.sortBy));
	}

	function view(row) {
		goto(resolve(`documents/${row.id}`));
	}

	function personGroups(people) {
		const grouped = {};
		for (const person of people ?? []) {
			const type = person.person_type_name || 'Unknown';
			if (!grouped[type]) grouped[type] = [];
			grouped[type].push(person.name);
		}
		return Object.entries(grouped).map(([type, names]) => ({
			type,
			shown: names.slice(0, 3),
			extra: Math.max(0, names.length - 3)
		}));
	}

	function typeLabel(mimeType) {
		if (!mimeType) return 'FILE';
		if (mimeType === 'application/pdf') return 'PDF';
		if (mimeType.startsWith('image/')) return 'IMAGE';
		if (mimeType === 'application/vnd.openxmlformats-officedocument.wordprocessingml.document')
			return 'DOCX';
		if (mimeType === 'application/vnd.oasis.opendocument.text') return 'ODT';
		return 'FILE';
	}

	function openDocument(event, row) {
		// let modifier clicks fall through to the href (new tab / middle-click)
		if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey)
			return;
		event.preventDefault();
		view(row);
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
		await api.savedSearches.create(name, savedFilter(documents.query));
		saving = false;
		await refreshSavedSearches();
	}

	function handleLoad(saved) {
		documents.update(normalizeSavedFilter(saved.filter));
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
		const res = await api.documents.batchDelete(selectedDocs.map((row) => row.id));
		if (!res.ok) {
			toastStore.error(res.data?.error || 'Batch delete failed');
			return;
		}
		if (res.data?.failed?.length > 0) {
			toastStore.warning(
				`Deleted ${res.data.deleted} of ${selectedDocs.length} documents. ${res.data.failed.length} failed.`
			);
		}
		documents.reload();
	}

	function onTagSearchInput(event) {
		tagSearchQuery = event.target.value;
		clearTimeout(tagSearchTimer);
		tagSearchTimer = setTimeout(async () => {
			try {
				tagOptions = await api.autocomplete.tags(tagSearchQuery, 20);
			} catch {
				tagOptions = [];
			}
		}, 200);
	}

	async function handleBatchAssign() {
		if (batchTagIds.length === 0) return;
		const res = await api.documents.batchAssignTags(
			selectedDocs.map((row) => row.id),
			batchTagIds,
			batchTagMode
		);
		if (!res.ok) {
			toastStore.error(res.data?.error || 'Batch tag assignment failed');
			return;
		}
		if (res.data?.failed?.length > 0) {
			toastStore.warning(
				`Tagged ${res.data.assigned} of ${selectedDocs.length} documents. ${res.data.failed.length} failed.`
			);
		}
		batchTagIds = [];
		showTagPicker = false;
		documents.reload();
	}

	async function handleBatchSetType() {
		if (!batchDocTypeId) return;
		const res = await api.documents.batchSetDocumentType(
			selectedDocs.map((row) => row.id),
			batchDocTypeId
		);
		if (!res.ok) {
			toastStore.error(res.data?.error || 'Batch type assignment failed');
			return;
		}
		if (res.data?.failed?.length > 0) {
			toastStore.warning(
				`Set type on ${res.data.updated} of ${selectedDocs.length} documents. ${res.data.failed.length} failed.`
			);
		}
		batchDocTypeId = null;
		showTypePicker = false;
		documents.reload();
	}

	$effect(() => {
		if (!showTagPicker && !showTypePicker && !showSaved) return;
		const onKey = (e) => {
			if (e.key === 'Escape') {
				showTagPicker = false;
				showTypePicker = false;
				showSaved = false;
			}
		};
		const onClick = (e) => {
			if (tagPickerEl && !tagPickerEl.contains(e.target)) showTagPicker = false;
			if (typePickerEl && !typePickerEl.contains(e.target)) showTypePicker = false;
			if (savedEl && !savedEl.contains(e.target)) showSaved = false;
		};
		document.addEventListener('keydown', onKey);
		document.addEventListener('click', onClick);
		return () => {
			document.removeEventListener('keydown', onKey);
			document.removeEventListener('click', onClick);
		};
	});

	onMount(() => {
		api.autocomplete
			.peopleTypes()
			.then((types) => (personTypes = types.map((type) => type.name)))
			.catch(() => {});
		api.autocomplete
			.documentTypes()
			.then((types) => (documentTypeOptions = types))
			.catch(() => toastStore.error('Failed to load document types'));
		refreshSavedSearches();
	});
</script>

{#snippet cell(col, row)}
	{#if col.key === 'title'}
		<a
			href={resolve(`documents/${row.id}`)}
			class="underline decoration-parchment-500/30 underline-offset-2 hover:text-gold-500"
			>{row.title}</a
		>
	{:else if col.key === 'snippet'}
		{#if row.snippet}
			<span class="text-xs leading-relaxed text-parchment-300">
				{#each snippetSegments(row.snippet) as segment, index (index)}
					{#if segment.bold}<b class="font-semibold text-gold-500">{segment.text}</b
						>{:else}{segment.text}{/if}
				{/each}
			</span>
		{:else}
			<span class="text-parchment-500 italic">—</span>
		{/if}
	{:else if col.key === 'tags'}
		{#if row.tags?.length > 0}
			<div class="flex flex-wrap gap-1">
				{#each row.tags.slice(0, 3) as tag (tag.id)}
					<span class="inline-block rounded-full bg-clay-800 px-2 py-0.5 text-xs text-parchment-300"
						>{tag.name}</span
					>
				{/each}
				{#if row.tags.length > 3}
					<span class="inline-block rounded-full bg-clay-800 px-2 py-0.5 text-xs text-parchment-400"
						>(+{row.tags.length - 3})</span
					>
				{/if}
			</div>
		{:else}
			<span class="text-parchment-500 italic">—</span>
		{/if}
	{:else if col.key === 'people'}
		{#if row.people?.length > 0}
			{#each personGroups(row.people) as group (group.type)}
				<div>
					<span class="text-xs text-parchment-400">{group.type}:</span>
					<span class="text-parchment-200">{group.shown.join(', ')}</span>
					{#if group.extra > 0}
						<span class="text-xs text-parchment-400">(+{group.extra})</span>
					{/if}
				</div>
			{/each}
		{:else}
			<span class="text-parchment-500 italic">—</span>
		{/if}
	{:else if col.key === 'page_count'}
		{#if row.page_count != null && row.page_count > 0}
			{row.page_count}
		{:else}
			<span class="text-parchment-500 italic">—</span>
		{/if}
	{:else if col.key === 'file_size'}
		{formatSize(row.file_size)}
	{:else if col.key === 'created_at'}
		{formatDate(row.created_at)}
	{:else if col.key === '_actions'}
		<a
			href={resolve(`/api/v1/documents/${row.id}/file?download=true`)}
			class="inline-flex items-center justify-center rounded-md p-1.5 text-parchment-500 transition-colors hover:bg-clay-800 hover:text-gold-500"
			title="Download PDF"
			aria-label="Download PDF"><Icon name="download" /></a
		>
	{:else}
		{row[col.key] ?? ''}
	{/if}
{/snippet}

{#snippet card(row)}
	<a
		href={resolve(`documents/${row.id}`)}
		class="flex flex-1 flex-col focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none focus-visible:ring-inset"
		onclick={(event) => openDocument(event, row)}
	>
		<div class="relative aspect-3/4 w-full overflow-hidden bg-clay-950">
			{#if row.has_thumbnail && !failedThumbs.has(row.id)}
				<img
					src={`/api/v1/documents/${row.id}/thumbnail`}
					alt={row.title || 'Document thumbnail'}
					loading="lazy"
					width="400"
					height="533"
					onerror={() => failedThumbs.add(row.id)}
					class="h-full w-full object-cover transition-transform duration-300 group-hover:scale-105 motion-reduce:transform-none motion-reduce:transition-none"
				/>
			{:else}
				<div class="flex h-full w-full flex-col items-center justify-center gap-2">
					{#if row.original_type === 'application/pdf'}
						<Icon name="file-pdf" size={40} strokeWidth={1.5} class="text-terracotta-500" />
					{:else if row.original_type?.startsWith('image/')}
						<Icon name="file-image" size={40} strokeWidth={1.5} class="text-sage-500" />
					{:else if row.original_type === 'application/vnd.openxmlformats-officedocument.wordprocessingml.document' || row.original_type === 'application/vnd.oasis.opendocument.text'}
						<Icon name="file-text" size={40} strokeWidth={1.5} class="text-lapis-400" />
					{:else}
						<Icon name="file" size={40} strokeWidth={1.5} class="text-parchment-500" />
					{/if}
					<span
						class="rounded-full bg-clay-800 px-2 py-0.5 text-[10px] font-medium tracking-wide text-parchment-400"
					>
						{typeLabel(row.original_type)}
					</span>
				</div>
			{/if}
		</div>
		<div class="flex flex-1 flex-col gap-1.5 p-3">
			<h3 class="line-clamp-2 text-sm leading-snug font-medium wrap-break-word text-parchment-200">
				{row.title || 'Untitled'}
			</h3>
			{#if row.tags?.length > 0}
				<div class="flex flex-wrap gap-1">
					{#each row.tags.slice(0, 3) as tag (tag.id)}
						<span class="rounded-full bg-clay-800 px-2 py-0.5 text-[11px] text-parchment-300"
							>{tag.name}</span
						>
					{/each}
				</div>
			{/if}
			<div class="mt-auto flex items-center justify-between text-[11px] text-parchment-500">
				<span>{formatDate(row.created_at)}</span>
				<span>{formatSize(row.file_size)}</span>
			</div>
		</div>
	</a>
{/snippet}

<div class="flex flex-col gap-4">
	<div class="flex items-center gap-3">
		{#if selectedDocs.length > 0}
			<button
				onclick={() => api.documents.downloadBatch(selectedDocs.map((row) => row.id))}
				class="shrink-0 rounded-lg bg-gold-600 px-3 py-2 text-sm font-medium text-clay-950 hover:bg-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
			>
				Download selected ({selectedDocs.length})
			</button>
			{#if !authStore.authEnabled() || authStore.isEditor()}
				<button
					onclick={handleBatchDelete}
					class="shrink-0 rounded-lg bg-terracotta-700 px-3 py-2 text-sm font-medium text-parchment-200 hover:bg-terracotta-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
				>
					Delete selected ({selectedDocs.length})
				</button>
			{/if}
			{#if !authStore.authEnabled() || authStore.isEditor()}
				<div class="relative shrink-0" bind:this={tagPickerEl}>
					<button
						onclick={() => (showTagPicker = !showTagPicker)}
						class="rounded-lg border border-clay-800 px-3 py-2 text-sm font-medium text-parchment-400 hover:bg-clay-800 hover:text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					>
						{batchTagIds.length > 0 ? `Tags (${batchTagIds.length})` : 'Assign tags'}
					</button>
					{#if showTagPicker}
						<div
							class="absolute top-full left-0 z-30 mt-1 w-72 overscroll-contain rounded-lg border border-clay-800 bg-clay-950 shadow-xl"
						>
							<div class="p-3">
								<input
									type="text"
									name="tag-search"
									autocomplete="off"
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
										{#each batchTagIds as tid (tid)}
											{@const tag = tagOptions.find((t) => t.id === tid)}
											{#if tag}
												<span
													class="inline-flex items-center gap-1 rounded-full bg-clay-800 px-2 py-0.5 text-xs text-parchment-300"
												>
													{tag.name}
													<button
														onclick={() => (batchTagIds = batchTagIds.filter((t) => t !== tid))}
														class="rounded text-parchment-500 hover:text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
														aria-label={`Remove tag ${tag.name}`}>×</button
													>
												</span>
											{/if}
										{/each}
									</div>
								{/if}
								<div class="mb-3 flex gap-2">
									<button
										onclick={() => (batchTagMode = 'add')}
										aria-pressed={batchTagMode === 'add'}
										class={`flex-1 rounded-md px-2 py-1 text-xs font-medium focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none ${
											batchTagMode === 'add'
												? 'bg-gold-600 text-clay-950'
												: 'border border-clay-800 text-parchment-400 hover:bg-clay-800'
										}`}
									>
										Add
									</button>
									<button
										onclick={() => (batchTagMode = 'replace')}
										aria-pressed={batchTagMode === 'replace'}
										class={`flex-1 rounded-md px-2 py-1 text-xs font-medium focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none ${
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
			{#if !authStore.authEnabled() || authStore.isEditor()}
				<div class="relative shrink-0" bind:this={typePickerEl}>
					<button
						onclick={() => (showTypePicker = !showTypePicker)}
						class="rounded-lg border border-clay-800 px-3 py-2 text-sm font-medium text-parchment-400 hover:bg-clay-800 hover:text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					>
						Set type
					</button>
					{#if showTypePicker}
						<div
							class="absolute top-full left-0 z-30 mt-1 w-64 overscroll-contain rounded-lg border border-clay-800 bg-clay-950 shadow-xl"
						>
							<div class="p-3">
								<select
									bind:value={batchDocTypeId}
									aria-label="Document type"
									class="mb-2 w-full rounded-md border border-clay-700 bg-clay-900 px-3 py-1.5 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-gold-500"
								>
									<option value={null} disabled>Choose type…</option>
									{#each documentTypeOptions as type (type.id)}
										<option value={type.id}>{type.name}</option>
									{/each}
								</select>
								<button
									onclick={handleBatchSetType}
									disabled={!batchDocTypeId}
									class="w-full rounded-md bg-gold-600 px-3 py-1.5 text-xs font-medium text-clay-950 hover:bg-gold-500 disabled:opacity-50"
								>
									Apply
								</button>
							</div>
						</div>
					{/if}
				</div>
			{/if}
		{/if}
		<div class="relative min-w-0 flex-1">
			<SearchBar
				q={documents.query.q}
				tags={documents.query.tags}
				people={documents.query.people}
				documentType={documents.query.documentType}
				language={documents.query.language}
				dateCreated={documents.query.dateCreated}
				dateModified={documents.query.dateModified}
				fileSize={documents.query.fileSize}
				{personTypes}
				personAnyType={PERSON_ANY_TYPE}
				onSearch={handleSearch}
			/>
		</div>
		<div
			class="flex shrink-0 rounded-lg border border-clay-800 p-0.5"
			role="group"
			aria-label="View mode"
		>
			<button
				onclick={() => (viewMode = 'table')}
				aria-pressed={viewMode === 'table'}
				class="rounded-md px-3 py-1.5 text-sm font-medium focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none {viewMode ===
				'table'
					? 'bg-gold-600 text-clay-950'
					: 'text-parchment-400 hover:text-parchment-200'}"
			>
				Table
			</button>
			<button
				onclick={() => (viewMode = 'grid')}
				aria-pressed={viewMode === 'grid'}
				class="rounded-md px-3 py-1.5 text-sm font-medium focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none {viewMode ===
				'grid'
					? 'bg-gold-600 text-clay-950'
					: 'text-parchment-400 hover:text-parchment-200'}"
			>
				Grid
			</button>
		</div>
		<button
			onclick={() => (showFilters = !showFilters)}
			class="rounded-lg border border-clay-800 px-3 py-2 text-sm font-medium text-parchment-400 hover:bg-clay-800 hover:text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
		>
			{showFilters ? 'Hide Filters' : 'Filters'}
		</button>
		{#if !authStore.authEnabled() || authStore.isEditor()}
			<button
				onclick={openSaveInput}
				disabled={saving}
				class="rounded-lg border border-clay-800 px-3 py-2 text-sm font-medium text-parchment-400 hover:bg-clay-800 hover:text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-50"
			>
				{saving ? 'Saving…' : 'Save'}
			</button>
		{/if}
		<div class="relative" bind:this={savedEl}>
			<button
				onclick={() => (showSaved = !showSaved)}
				class="rounded-lg border border-clay-800 px-3 py-2 text-sm font-medium text-parchment-400 hover:bg-clay-800 hover:text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
			>
				Saved
			</button>
			{#if showSaved}
				<div
					class="absolute top-full right-0 z-30 mt-1 w-72 overscroll-contain rounded-lg border border-clay-800 bg-clay-950 shadow-xl"
				>
					<div class="max-h-64 overflow-y-auto p-2">
						{#if savedSearches.length === 0}
							<p class="px-2 py-3 text-center text-xs text-parchment-500">No saved searches</p>
						{:else}
							{#each savedSearches as search (search.id)}
								<div class="group flex items-center gap-2 rounded-md px-2 py-1.5 hover:bg-clay-800">
									<button
										onclick={() => handleLoad(search)}
										class="min-w-0 flex-1 truncate text-left text-sm text-parchment-200"
									>
										{search.name}
									</button>
									{#if !authStore.authEnabled() || authStore.isEditor()}
										<button
											onclick={() => handleDelete(search.id)}
											class="shrink-0 rounded p-0.5 text-parchment-500 opacity-0 group-hover:opacity-100 group-focus-visible:opacity-100 hover:text-red-400 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
											title="Delete"
											aria-label="Delete saved search">×</button
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
				<label for="save-search-name" class="mb-2 block text-xs font-medium text-parchment-400"
					>Name this search</label
				>
				<form
					onsubmit={(e) => {
						e.preventDefault();
						submitSave();
					}}
				>
					<input
						type="text"
						id="save-search-name"
						name="save-search-name"
						autocomplete="off"
						spellcheck={false}
						bind:value={saveName}
						placeholder="e.g. Invoices from Q1…"
						class="mb-2 w-full rounded-md border border-clay-700 bg-clay-900 px-3 py-1.5 text-sm text-parchment-200 placeholder-parchment-600 focus:border-gold-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-gold-500"
					/>
					<div class="flex justify-end gap-2">
						<button
							type="button"
							onclick={() => (showNameInput = false)}
							class="rounded-md px-3 py-1 text-xs font-medium text-parchment-400 hover:bg-clay-800 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
						>
							Cancel
						</button>
						<button
							type="submit"
							disabled={saving}
							class="rounded-md bg-gold-600 px-3 py-1 text-xs font-medium text-clay-950 hover:bg-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-50"
						>
							{saving ? 'Saving…' : 'Save'}
						</button>
					</div>
				</form>
			</div>
		</div>
	{/if}

	{#if showFilters}
		<FilterPanel filters={documents.query} {personTypes} onChange={handleSearch} />
	{/if}

	{#if viewMode === 'grid'}
		<ListGrid
			rows={documents.rows}
			total={documents.total}
			loading={documents.loading}
			error={documents.error}
			page={documents.query.page}
			pageSize={documents.query.pageSize}
			pageSizes={[10, 25, 50, 100]}
			sortBy={documents.query.sortBy}
			sortOrder={documents.query.sortOrder}
			{sortOptions}
			keyField="id"
			title="Documents"
			selectedKeys={documents.selectedKeys}
			onSort={sortByColumn}
			onToggleOrder={toggleSortOrder}
			onPage={(next) => documents.update({ page: next })}
			onPageSize={(size) => documents.update({ pageSize: size })}
			onToggleRow={documents.toggle}
			{card}
		/>
	{:else}
		<ListTable
			{columns}
			{cell}
			rows={documents.rows}
			total={documents.total}
			loading={documents.loading}
			error={documents.error}
			page={documents.query.page}
			pageSize={documents.query.pageSize}
			pageSizes={[10, 25, 50, 100]}
			sortBy={documents.query.sortBy}
			sortOrder={documents.query.sortOrder}
			keyField="id"
			title="Documents"
			selectedKeys={documents.selectedKeys}
			onSort={sortByColumn}
			onPage={(next) => documents.update({ page: next })}
			onPageSize={(size) => documents.update({ pageSize: size })}
			onRowClick={view}
			onToggleRow={documents.toggle}
			onToggleAll={documents.toggleAll}
		/>
	{/if}
</div>
