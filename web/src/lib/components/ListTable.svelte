<script>
	import Icon from '#lib/components/Icon.svelte';
	import { listWindow } from '#lib/list/listQuery.svelte.js';

	/**
	 * Presentational table for a server-paginated list. It owns no data, no URL state and no fetching:
	 * the route passes rows and view state in, and receives sort/page/size/selection events out. Cells
	 * render through the `cell(column, row)` snippet supplied by the route.
	 */
	let {
		columns = [],
		rows = [],
		total = null,
		loading = false,
		error = '',
		page = 0,
		pageSize = 25,
		pageSizes = null,
		sortBy = '',
		sortOrder = 'desc',
		keyField = 'id',
		title = '',
		selectedKeys = null,
		onSort = null,
		onPage = null,
		onPageSize = null,
		onRowClick = null,
		onToggleRow = null,
		onToggleAll = null,
		cell = null
	} = $props();

	const selectable = $derived(selectedKeys != null);
	const pageCount = $derived(total != null ? Math.ceil(total / pageSize) : null);
	const pagerWindow = $derived(listWindow({ page, pageSize, rowCount: rows.length, total }));
	const allSelected = $derived(
		rows.length > 0 && selectedKeys != null && rows.every((row) => selectedKeys.has(row[keyField]))
	);

	function handleSort(key) {
		if (onSort) onSort(key);
	}

	function handlePageSize(event) {
		if (onPageSize) onPageSize(parseInt(event.target.value));
	}
</script>

<div class="space-y-4">
	{#if title}
		<h2 class="text-2xl font-semibold text-parchment-200">{title}</h2>
	{/if}

	<div class="rounded-lg border border-clay-800 bg-clay-900">
		<div class="overflow-x-auto">
			<table class="w-full table-auto text-sm">
				<thead class="sticky top-0 bg-clay-900 text-left text-parchment-500">
					<tr>
						{#if selectable}
							<th class="w-10 px-4 py-3 whitespace-nowrap select-none" scope="col">
								<input
									type="checkbox"
									aria-label="Select all"
									checked={allSelected}
									onchange={() => onToggleAll?.()}
									class="h-4 w-4 cursor-pointer accent-gold-500"
								/>
							</th>
						{/if}
						{#each columns as col (col.key)}
							<th
								class="px-4 py-3 font-mono text-[0.6875rem] font-semibold tracking-[0.12em] whitespace-nowrap uppercase transition-colors select-none focus:outline-none {col.sortable
									? 'cursor-pointer hover:bg-clay-800 hover:text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:ring-inset'
									: ''} {col.headerClass || ''}"
								style="width: {col.width ?? 'auto'}; {col.minWidth &&
									`min-width: ${col.minWidth};`} {col.maxWidth && `max-width: ${col.maxWidth};`}"
								scope="col"
								aria-sort={col.sortable && sortBy === col.key
									? sortOrder === 'asc'
										? 'ascending'
										: 'descending'
									: undefined}
								tabindex={col.sortable ? '0' : undefined}
								onclick={col.sortable ? () => handleSort(col.key) : undefined}
								onkeydown={col.sortable
									? (e) => {
											if (e.key === 'Enter' || e.key === ' ') {
												e.preventDefault();
												handleSort(col.key);
											}
										}
									: undefined}
							>
								{col.label}
								{#if col.sortable && sortBy === col.key}
									<span class="ms-1 inline-block w-4 text-center text-gold-500">
										{sortOrder === 'asc' ? '▲' : '▼'}
									</span>
								{/if}
							</th>
						{/each}
					</tr>
				</thead>
				<tbody class="divide-y divide-clay-800" aria-live="polite">
					{#if loading}
						<tr>
							<td
								colspan={columns.length + (selectable ? 1 : 0)}
								class="px-4 py-8 text-center text-parchment-500"
							>
								<Icon
									name="spinner"
									size={20}
									class="inline-block animate-spin motion-reduce:animate-none"
								/>
								<span class="ml-2">Loading…</span>
							</td>
						</tr>
					{:else if error && rows.length === 0}
						<tr>
							{#if selectable}
								<td class="w-10 px-4 py-8"></td>
							{/if}
							{#each columns as col (col.key)}
								<td class="px-4 py-8 text-terracotta-500">
									{#if col.key === columns[0]?.key}
										{error}
									{/if}
								</td>
							{/each}
						</tr>
					{:else if rows.length === 0}
						<tr>
							{#if selectable}
								<td class="w-10 px-4 py-8"></td>
							{/if}
							{#each columns as col (col.key)}
								<td class="px-4 py-8 text-parchment-500">
									{#if col.key === columns[0]?.key}
										No results found.
									{/if}
								</td>
							{/each}
						</tr>
					{:else}
						{#each rows as row (row[keyField])}
							<tr
								class="transition-colors {onRowClick
									? 'cursor-pointer hover:bg-clay-800/60 focus:outline-none focus-visible:ring-2 focus-visible:ring-gold-500'
									: ''}"
								tabindex={onRowClick ? '0' : undefined}
								role={onRowClick ? 'link' : undefined}
								onclick={onRowClick
									? (e) => {
											if (e.target.closest('a, input, button')) return;
											onRowClick(row);
										}
									: undefined}
								onkeydown={onRowClick
									? (e) => {
											if (e.key === 'Enter' || e.key === ' ') {
												e.preventDefault();
												onRowClick(row);
											}
										}
									: undefined}
							>
								{#if selectable}
									<td class="w-10 px-4 py-3">
										<input
											type="checkbox"
											aria-label={`Select row ${row[keyField]}`}
											checked={selectedKeys.has(row[keyField])}
											onchange={() => onToggleRow?.(row)}
											class="h-4 w-4 cursor-pointer accent-gold-500"
											onclick={(e) => e.stopPropagation()}
										/>
									</td>
								{/if}
								{#each columns as col (col.key)}
									<td class="px-4 py-3 text-parchment-200 {col.cellClass || ''}">
										{#if cell}
											{@render cell(col, row)}
										{:else}
											{row[col.key]}
										{/if}
									</td>
								{/each}
							</tr>
						{/each}
					{/if}
				</tbody>
			</table>
		</div>

		{#if !loading || rows.length > 0}
			{@const showing = rows.length}
			<div class="flex flex-wrap items-center justify-between gap-3 px-4 pt-1 pb-3 text-sm">
				<p class="text-parchment-500">
					{#if total != null}
						Showing {pagerWindow.rangeStart}–{pagerWindow.rangeEnd} of {total}
					{:else}
						Showing {showing} result{showing !== 1 ? 's' : ''}
						{#if page > 0}
							(starting at #{pagerWindow.rangeStart})
						{/if}
					{/if}
					{#if pageCount != null && pageCount > 0}
						<span class="text-parchment-600"> · page {page + 1} of {pageCount}</span>
					{/if}
				</p>
				<div class="flex items-center gap-3">
					{#if pageSizes && pageSizes.length > 1}
						<label class="flex items-center gap-2 text-parchment-400">
							Per page
							<select
								id="list-page-size"
								name="list-page-size"
								value={pageSize}
								onchange={handlePageSize}
								class="rounded-md border border-clay-700 bg-clay-950 px-2 py-1 text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							>
								{#each pageSizes as size (size)}
									<option value={size}>{size}</option>
								{/each}
							</select>
						</label>
					{/if}
					<button
						disabled={page === 0}
						onclick={() => onPage?.(page - 1)}
						class="rounded-md border border-clay-700 px-2.5 py-1 text-parchment-400 hover:border-gold-500 hover:text-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-40"
					>
						Prev
					</button>
					<button
						disabled={!pagerWindow.hasMore}
						onclick={() => onPage?.(page + 1)}
						class="rounded-md border border-clay-700 px-2.5 py-1 text-parchment-400 hover:border-gold-500 hover:text-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-40"
					>
						Next
					</button>
				</div>
			</div>
		{/if}
	</div>
</div>
