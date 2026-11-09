<script>
	import { listWindow } from '#lib/list/listQuery.svelte.js';

	/**
	 * Presentational card grid for a server-paginated list. Like ListTable it owns no data, URL state
	 * or fetching; the route supplies rows, view state, selection and a `card(row)` snippet.
	 */
	let {
		rows = [],
		total = null,
		loading = false,
		error = '',
		page = 0,
		pageSize = 25,
		pageSizes = null,
		sortBy = '',
		sortOrder = 'desc',
		sortOptions = [],
		keyField = 'id',
		title = '',
		selectedKeys = null,
		onSort = null,
		onToggleOrder = null,
		onPage = null,
		onPageSize = null,
		onToggleRow = null,
		card = null
	} = $props();

	const selectable = $derived(selectedKeys != null);
	const pagerWindow = $derived(listWindow({ page, pageSize, rowCount: rows.length, total }));
	const skeletonItems = $derived(
		Array.from({ length: pageSize > 12 ? 12 : pageSize }, (_, i) => i)
	);

	function handlePageSize(event) {
		if (onPageSize) onPageSize(parseInt(event.target.value));
	}

	function handleSort(event) {
		if (onSort) onSort(event.target.value);
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
			{#if sortOptions.length > 0}
				<label for="list-grid-sort" class="text-parchment-400">Sort</label>
				<select
					id="list-grid-sort"
					name="list-grid-sort"
					value={sortBy}
					onchange={handleSort}
					class="rounded-lg border border-clay-800 bg-clay-900 px-3 py-1.5 text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
				>
					{#each sortOptions as option (option.value)}
						<option value={option.value}>{option.label}</option>
					{/each}
				</select>
				<button
					onclick={() => onToggleOrder?.()}
					title="Toggle sort direction"
					aria-label="Toggle sort direction"
					class="rounded-lg border border-clay-800 bg-clay-900 px-3 py-1.5 text-parchment-400 transition-colors hover:bg-clay-800 hover:text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
				>
					{sortOrder === 'asc' ? '▲' : '▼'}
				</button>
			{/if}
			{#if pageSizes && pageSizes.length > 1}
				<label for="list-grid-page-size" class="text-parchment-400">Per page</label>
				<select
					id="list-grid-page-size"
					name="list-grid-page-size"
					value={pageSize}
					onchange={handlePageSize}
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
	{:else if error && rows.length === 0}
		<div
			class="rounded-lg border border-clay-800 bg-clay-950 px-4 py-8 text-center text-terracotta-500"
		>
			{error}
		</div>
	{:else if rows.length === 0}
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
			{#each rows as row (row[keyField])}
				<div
					class="group relative flex flex-col overflow-hidden rounded-lg border border-clay-800 bg-clay-900 transition-colors"
				>
					{#if card}
						{@render card(row)}
					{/if}
					{#if selectable}
						<div class="absolute top-2 left-2 z-10 rounded-md bg-clay-950/70 p-1 backdrop-blur-sm">
							<input
								type="checkbox"
								aria-label={`Select row ${row[keyField]}`}
								checked={selectedKeys.has(row[keyField])}
								onchange={() => onToggleRow?.(row)}
								class="h-4 w-4 cursor-pointer accent-gold-500"
							/>
						</div>
					{/if}
				</div>
			{/each}
		</div>
	{/if}

	{#if !loading || rows.length > 0}
		{@const showing = rows.length}
		<div class="flex items-center justify-between text-sm">
			<p class="text-parchment-500">
				{#if total != null}
					Showing {pagerWindow.rangeStart}–{pagerWindow.rangeEnd} of {total}
				{:else}
					Showing {showing} result{showing !== 1 ? 's' : ''}
					{#if page > 0}
						(starting at #{pagerWindow.rangeStart})
					{/if}
				{/if}
			</p>
			<div class="flex gap-2">
				<button
					disabled={page === 0}
					onclick={() => onPage?.(page - 1)}
					class="rounded-lg border border-clay-800 bg-clay-900 px-3 py-1.5 text-parchment-400 transition-colors hover:bg-clay-800 disabled:opacity-40"
				>
					Previous
				</button>
				<button
					disabled={!pagerWindow.hasMore}
					onclick={() => onPage?.(page + 1)}
					class="rounded-lg border border-clay-800 bg-clay-900 px-3 py-1.5 text-parchment-400 transition-colors hover:bg-clay-800 disabled:opacity-40"
				>
					Next
				</button>
			</div>
		</div>
	{/if}
</div>
