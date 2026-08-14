<script>
	import { onMount, untrack } from 'svelte';
	import { SvelteSet, SvelteURLSearchParams } from 'svelte/reactivity';
	import { page } from '$app/state';
	import { replaceState } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { formatSize } from '$lib/utils/html.js';
	/** @type {{
	 * fetch: (opts: {sortBy:string,sortOrder:string,limit:number,offset:number}) => Promise<{results:Array,total:number}>,
	 * onRowClick?: Function,
	 * pageSizes?: number[],
	 * defaultPageSize?: number,
	 * keyField?: string,
	 * defaultSortBy?: string,
	 * defaultSortOrder?: 'asc' | 'desc',
	 * title?: string,
	 * selectable?: boolean,
	 * onselectionchange?: (selectedRows: any[]) => void,
	 * urlSync?: string,
	 * refreshKey?: number
	 * }}
	 * */
	let {
		fetch = async () => ({ results: [], total: 0 }),
		onRowClick = null,
		pageSizes = [10, 25, 50, 100],
		defaultPageSize = 25,
		keyField = 'id',
		title = '',
		refreshKey = 0,
		selectable = false,
		onselectionchange = null,
		defaultSortBy = '',
		defaultSortOrder = 'desc',
		urlSync = ''
	} = $props();

	let selectedKeys = $state(new Set());
	let failedImgs = $state(new Set());

	let data = $state([]);
	let total = $state(null);
	let loading = $state(true);
	let error = $state('');

	let sortBy = $state('');
	let sortOrder = $state('desc');
	let pageIndex = $state(0);
	let pageSize = $state(0);
	let initialized = $state(false);

	let skeletonItems = $derived(Array.from({ length: pageSize > 12 ? 12 : pageSize }, (_, i) => i));

	$effect(() => {
		if (!initialized) {
			pageSize = defaultPageSize;
			if (defaultSortBy) sortBy = defaultSortBy;
			if (defaultSortOrder === 'asc') sortOrder = 'asc';
			if (urlSync) {
				const sp = page.url.searchParams;
				const size = parseInt(sp.get(`${urlSync}_size`) || '');
				const idx = parseInt(sp.get(`${urlSync}_page`) || '');
				const sort = sp.get(`${urlSync}_sort`);
				const order = sp.get(`${urlSync}_order`);
				if (pageSizes.includes(size)) pageSize = size;
				if (Number.isInteger(idx) && idx >= 0) pageIndex = idx;
				if (sort) sortBy = sort;
				if (order === 'asc' || order === 'desc') sortOrder = order;
			}
			initialized = true;
		}
	});

	function syncUrl() {
		if (!urlSync) return;
		const sp = new SvelteURLSearchParams(page.url.searchParams);
		const params = [
			[`${urlSync}_size`, String(pageSize)],
			[`${urlSync}_page`, String(pageIndex)],
			[`${urlSync}_sort`, sortBy],
			[`${urlSync}_order`, sortOrder]
		];
		for (const [key, value] of params) {
			if (value) sp.set(key, value);
			else sp.delete(key);
		}
		replaceState(resolve(`${page.url.pathname}?${sp.toString()}`));
	}

	$effect(() => {
		if (refreshKey) {
			untrack(() => {
				pageIndex = 0;
				load();
				syncUrl();
			});
		}
	});

	onMount(() => {
		load();
	});

	async function load() {
		loading = true;
		error = '';
		selectedKeys = new Set();
		failedImgs = new Set();
		try {
			const result = await fetch({
				sortBy: sortBy || 'created_at',
				sortOrder,
				limit: pageSize,
				offset: pageIndex * pageSize
			});
			if (Array.isArray(result)) {
				data = result;
				total = null;
			} else if (result && Array.isArray(result.results)) {
				data = result.results;
				total = result.total ?? null;
			} else {
				data = [];
				total = null;
			}
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load data';
			data = [];
			total = null;
		} finally {
			loading = false;
		}
	}

	function toggleRow(key) {
		const next = new SvelteSet(selectedKeys);
		if (next.has(key)) {
			next.delete(key);
		} else {
			next.add(key);
		}
		selectedKeys = next;
		if (onselectionchange) onselectionchange(getSelectedRows());
	}

	function getSelectedRows() {
		return data.filter((row) => selectedKeys.has(row[keyField]));
	}

	function toggleSort(key) {
		if (sortBy === key) {
			sortOrder = sortOrder === 'asc' ? 'desc' : 'asc';
		} else {
			sortBy = key;
			sortOrder = 'desc';
		}
		pageIndex = 0;
		load();
		syncUrl();
	}

	function changePageSize(e) {
		pageSize = parseInt(e.target.value);
		pageIndex = 0;
		load();
		syncUrl();
	}

	function prevPage() {
		if (pageIndex > 0) {
			pageIndex--;
			load();
			syncUrl();
		}
	}

	function nextPage() {
		if (data.length >= pageSize) {
			pageIndex++;
			load();
			syncUrl();
		}
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

	function escapeHtml(s) {
		return String(s ?? '').replace(/[&<>"']/g, (c) => {
			return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
		});
	}
</script>

<div class="space-y-4">
	<div class="flex flex-wrap items-center justify-between gap-3">
		{#if title}
			<h2 class="text-2xl font-semibold text-parchment-200">{title}</h2>
		{:else}
			<span></span>
		{/if}
		<div class="flex items-center gap-3 text-sm">
			<label for="dg-sort" class="text-parchment-400">Sort</label>
			<select
				id="dg-sort"
				name="dg-sort"
				value={sortBy}
				onchange={(e) => toggleSort(e.target.value)}
				class="rounded-lg border border-clay-800 bg-clay-900 px-3 py-1.5 text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
			>
				<option value="created_at">Created</option>
				<option value="title">Title</option>
				<option value="file_size">Size</option>
			</select>
			<button
				onclick={() => toggleSort(sortBy)}
				title="Toggle sort direction"
				aria-label="Toggle sort direction"
				class="rounded-lg border border-clay-800 bg-clay-900 px-3 py-1.5 text-parchment-400 transition-colors hover:bg-clay-800 hover:text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
			>
				{sortOrder === 'asc' ? '▲' : '▼'}
			</button>
			{#if pageSizes.length > 1}
				<label for="dg-page-size" class="text-parchment-400">Per page</label>
				<select
					id="dg-page-size"
					name="dg-page-size"
					value={pageSize}
					onchange={changePageSize}
					class="rounded-lg border border-clay-800 bg-clay-900 px-3 py-1.5 text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
				>
					{#each pageSizes as size (size)}
						<option value={size}>{size}</option>
					{/each}
				</select>
			{/if}
		</div>
	</div>

	{#if loading}
		<div
			class="grid grid-cols-2 gap-4 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5"
			aria-busy="true"
		>
			{#each skeletonItems as i (i)}
				<div
					class="animate-pulse overflow-hidden rounded-lg border border-clay-800 bg-clay-900 motion-reduce:animate-none"
				>
					<div class="aspect-3/4 w-full bg-clay-800"></div>
					<div class="space-y-2 p-3">
						<div class="h-3 w-3/4 rounded bg-clay-800"></div>
						<div class="h-3 w-1/2 rounded bg-clay-800"></div>
					</div>
				</div>
			{/each}
		</div>
	{:else if error && data.length === 0}
		<div
			class="rounded-lg border border-clay-800 bg-clay-950 px-4 py-8 text-center text-terracotta-500"
		>
			{error}
		</div>
	{:else if data.length === 0}
		<div
			class="rounded-lg border border-clay-800 bg-clay-950 px-4 py-8 text-center text-parchment-500"
		>
			No results found.
		</div>
	{:else}
		<div
			class="grid grid-cols-2 gap-4 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5"
			aria-live="polite"
		>
			{#each data as row (row[keyField])}
				{@const showIcon = failedImgs.has(row[keyField])}
				<div
					class="group relative flex flex-col overflow-hidden rounded-lg border border-clay-800 bg-clay-900 transition-colors {onRowClick
						? 'hover:border-gold-500/60 hover:bg-clay-800'
						: ''}"
				>
					<a
						href={resolve(`/documents/${row[keyField]}`)}
						class="flex flex-1 flex-col focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none focus-visible:ring-inset"
						onclick={onRowClick
							? (e) => {
									// let modifier clicks fall through to the href (new tab / middle-click)
									if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
									e.preventDefault();
									onRowClick(row);
								}
							: undefined}
					>
						<div class="relative aspect-3/4 w-full overflow-hidden bg-clay-950">
							{#if row.has_thumbnail && !showIcon}
								<img
									src={`/api/v1/documents/${row.id}/thumbnail`}
									alt={row.title || 'Document thumbnail'}
									loading="lazy"
									width="400"
									height="533"
									onerror={() => {
										failedImgs = new Set([...failedImgs, row[keyField]]);
									}}
									class="h-full w-full object-cover transition-transform duration-300 group-hover:scale-105 motion-reduce:transform-none motion-reduce:transition-none"
								/>
							{:else}
								<div class="flex h-full w-full flex-col items-center justify-center gap-2">
									{#if row.original_type === 'application/pdf'}
										<svg
											xmlns="http://www.w3.org/2000/svg"
											width="40"
											height="40"
											viewBox="0 0 24 24"
											aria-hidden="true"
											stroke="currentColor"
											stroke-width="1.5"
											fill="none"
											stroke-linecap="round"
											stroke-linejoin="round"
											class="text-terracotta-500"
										>
											<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8Z" />
											<path d="M14 2v6h6" />
											<path d="M9 15h6" />
											<path d="M9 12h2" />
											<path d="M9 18h4" />
										</svg>
									{:else if row.original_type?.startsWith('image/')}
										<svg
											xmlns="http://www.w3.org/2000/svg"
											width="40"
											height="40"
											viewBox="0 0 24 24"
											aria-hidden="true"
											stroke="currentColor"
											stroke-width="1.5"
											fill="none"
											stroke-linecap="round"
											stroke-linejoin="round"
											class="text-sage-500"
										>
											<rect x="3" y="3" width="18" height="18" rx="2" />
											<circle cx="9" cy="9" r="2" />
											<path d="m21 15-3.086-3.086a2 2 0 0 0-2.828 0L6 21" />
										</svg>
									{:else if row.original_type === 'application/vnd.openxmlformats-officedocument.wordprocessingml.document' || row.original_type === 'application/vnd.oasis.opendocument.text'}
										<svg
											xmlns="http://www.w3.org/2000/svg"
											width="40"
											height="40"
											viewBox="0 0 24 24"
											aria-hidden="true"
											stroke="currentColor"
											stroke-width="1.5"
											fill="none"
											stroke-linecap="round"
											stroke-linejoin="round"
											class="text-lapis-400"
										>
											<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8Z" />
											<path d="M14 2v6h6" />
											<path d="M16 13H8" />
											<path d="M16 17H8" />
											<path d="M10 9H8" />
										</svg>
									{:else}
										<svg
											xmlns="http://www.w3.org/2000/svg"
											width="40"
											height="40"
											viewBox="0 0 24 24"
											aria-hidden="true"
											stroke="currentColor"
											stroke-width="1.5"
											fill="none"
											stroke-linecap="round"
											stroke-linejoin="round"
											class="text-parchment-500"
										>
											<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8Z" />
											<path d="M14 2v6h6" />
										</svg>
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
							<h3
								class="line-clamp-2 text-sm leading-snug font-medium wrap-break-word text-parchment-200"
							>
								{row.title || 'Untitled'}
							</h3>
							{#if row.tags?.length > 0}
								<div class="flex flex-wrap gap-1">
									{#each row.tags.slice(0, 3) as tag (tag.id)}
										<span
											class="rounded-full bg-clay-800 px-2 py-0.5 text-[11px] text-parchment-300"
										>
											{escapeHtml(tag.name)}
										</span>
									{/each}
								</div>
							{/if}
							<div class="mt-auto flex items-center justify-between text-[11px] text-parchment-500">
								<span>{row.created_at ? new Date(row.created_at).toLocaleDateString() : ''}</span>
								<span>{formatSize(row.file_size)}</span>
							</div>
						</div>
					</a>
					{#if selectable}
						<div class="absolute top-2 left-2 z-10 rounded-md bg-clay-950/70 p-1 backdrop-blur-sm">
							<input
								type="checkbox"
								aria-label={`Select ${row.title || row[keyField]}`}
								checked={selectedKeys.has(row[keyField])}
								onchange={() => toggleRow(row[keyField])}
								class="h-4 w-4 cursor-pointer accent-gold-500"
							/>
						</div>
					{/if}
				</div>
			{/each}
		</div>
	{/if}

	<!-- Pagination footer -->
	{#if !loading || data.length > 0}
		{@const showing = data.length}
		{@const hasMore = total != null ? pageIndex * pageSize + showing < total : showing >= pageSize}
		<div class="flex items-center justify-between text-sm">
			<p class="text-parchment-500">
				{#if total != null}
					Showing {pageIndex * pageSize + 1}–{pageIndex * pageSize + showing} of {total}
				{:else}
					Showing {showing} result{showing !== 1 ? 's' : ''}
					{#if pageIndex > 0}
						(starting at #{pageIndex * pageSize + 1})
					{/if}
				{/if}
			</p>
			<div class="flex gap-2">
				<button
					disabled={pageIndex === 0}
					onclick={prevPage}
					class="rounded-lg border border-clay-800 bg-clay-900 px-3 py-1.5 text-parchment-400 transition-colors hover:bg-clay-800 disabled:opacity-40"
				>
					Previous
				</button>
				<button
					disabled={!hasMore}
					onclick={nextPage}
					class="rounded-lg border border-clay-800 bg-clay-900 px-3 py-1.5 text-parchment-400 transition-colors hover:bg-clay-800 disabled:opacity-40"
				>
					Next
				</button>
			</div>
		</div>
	{/if}
</div>
