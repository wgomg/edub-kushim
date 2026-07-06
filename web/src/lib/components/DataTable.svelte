<script>
	import { onMount, untrack } from 'svelte';

	/**
	 * @typedef {Object} Column
	 * @property {string} key - Accessor key on the row object
	 * @property {string} label - Column header text
	 * @property {boolean} [sortable] - Enable click-to-sort on this column
	 * @property {Function} [cell] - Custom cell formatter: (value, row) => string
	 * @property {string} [headerClass] - Extra classes for <th>
	 * @property {string} [cellClass] - Extra classes for <td>
	 * @property {boolean} [noUnderline] - Skip underline decoration when onRowClick is set
	 * @property {string} [width] - Column width.
	 * @property {string} [maxWidth] - Max column width.
	 * @property {string} [minWidth] - Min column width.
	 */

	/** @type {{
	 * columns: Column[],
	 * fetch: (opts: {sortBy:string,sortOrder:string,limit:number,offset:number}) => Promise<Array>,
	 * onRowClick?: Function,
	 * pageSizes?: number[],
	 * defaultPageSize?: number,
	 * keyField?: string,
	 * defaultSortBy?: string,
	 * defaultSortOrder?: 'asc' | 'desc',
	 * title?: string,
	 * selectable?: boolean,
	 * onselectionchange?: (selectedRows: any[]) => void
	 * }}
	 * */
	let {
		columns = [],
		fetch = async () => [],
		onRowClick = null,
		pageSizes = [10, 25, 50, 100],
		defaultPageSize = 25,
		keyField = 'id',
		title = '',
		refreshKey = 0,
		selectable = false,
		onselectionchange = null,
		defaultSortBy = '',
		defaultSortOrder = 'desc'
	} = $props();

	let selectedKeys = $state(new Set());

	let data = $state([]);
	let total = $state(null);
	let loading = $state(true);
	let error = $state('');

	let sortBy = $state('');
	let sortOrder = $state('desc');
	let pageIndex = $state(0);
	let pageSize = $state(0);
	let initialized = $state(false);

	$effect(() => {
		if (!initialized) {
			pageSize = defaultPageSize;
			const target =
				(defaultSortBy && columns.find((c) => c.key === defaultSortBy)) ||
				columns.find((c) => c.sortable) ||
				columns[0];
			if (target) sortBy = target.key;
			if (defaultSortOrder === 'asc') sortOrder = 'asc';
			initialized = true;
		}
	});

	$effect(() => {
		if (refreshKey) {
			untrack(() => {
				pageIndex = 0;
				load();
			});
		}
	});

	onMount(() => {
		load();
	});

	async function load() {
		if (data.length === 0) loading = true;
		error = '';
		selectedKeys = new Set();
		try {
			const result = await fetch({
				sortBy: sortBy || columns[0]?.key || 'created_at',
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
		const next = new Set(selectedKeys);
		if (next.has(key)) {
			next.delete(key);
		} else {
			next.add(key);
		}
		selectedKeys = next;
		if (onselectionchange) onselectionchange(getSelectedRows());
	}

	function toggleAll() {
		const allSelected = data.every((row) => selectedKeys.has(row[keyField]));
		const next = new Set(selectedKeys);
		for (const row of data) {
			if (allSelected) {
				next.delete(row[keyField]);
			} else {
				next.add(row[keyField]);
			}
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
	}

	function changePageSize(e) {
		pageSize = parseInt(e.target.value);
		pageIndex = 0;
		load();
	}

	function prevPage() {
		if (pageIndex > 0) {
			pageIndex--;
			load();
		}
	}

	function nextPage() {
		if (data.length >= pageSize) {
			pageIndex++;
			load();
		}
	}
</script>

<div class="space-y-4">
	<div class="flex items-center justify-between">
		{#if title}
			<h1 class="text-2xl font-semibold text-parchment-200">{title}</h1>
		{:else}
			<span></span>
		{/if}
		<div class="flex items-center gap-2 text-sm">
			{#if pageSizes.length > 1}
				<label for="dt-page-size" class="text-parchment-400">Per page</label>
				<select
					id="dt-page-size"
					name="dt-page-size"
					value={pageSize}
					onchange={changePageSize}
					class="rounded-lg border border-clay-800 bg-clay-900 px-3 py-1.5 text-parchment-200"
				>
					{#each pageSizes as size (size)}
						<option value={size}>{size} </option>
					{/each}
				</select>
			{/if}
		</div>
	</div>

	<!-- Table wrapper: horizontal scroll on narrow screens -->
	<div class="overflow-x-auto rounded-lg border border-clay-800">
		<table class="w-full table-auto text-sm">
			<thead class="sticky top-0 bg-clay-900 text-left text-parchment-400">
				<tr>
					{#if selectable}
						<th class="w-10 px-4 py-3 font-medium whitespace-nowrap select-none" scope="col">
							<input
								type="checkbox"
								checked={data.length > 0 && data.every((row) => selectedKeys.has(row[keyField]))}
								onchange={toggleAll}
								class="h-4 w-4 cursor-pointer accent-gold-500"
							/>
						</th>
					{/if}
					{#each columns as col, i (col.key)}
						<th
							class="px-4 py-3 font-medium whitespace-nowrap transition-colors select-none focus:outline-none {col.sortable
								? 'cursor-pointer hover:bg-clay-800 hover:text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:ring-inset'
								: ''} {col.headerClass || ''}"
							style="width: {col.width ?? 'auto'}; {col.minWidth &&
								`min-width: ${col.minWidth};`} {col.maxWidth && `max-width: ${col.maxWidth};`}"
							scope="col"
							tabindex={col.sortable ? '0' : undefined}
							onclick={col.sortable ? () => toggleSort(col.key) : undefined}
							onkeydown={col.sortable
								? (e) => {
										if (e.key === 'Enter' || e.key === ' ') {
											e.preventDefault();
											toggleSort(col.key);
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
			<tbody class="divide-y divide-clay-800">
				{#if loading && data.length === 0}
					<tr class="bg-clay-950">
						{#if selectable}
							<td class="w-10 px-4 py-8"></td>
						{/if}
						{#each columns as col (col.key)}
							<td class="px-4 py-8 text-parchment-500">
								{#if col.key === columns[0].key}
									Loading…
								{/if}
							</td>
						{/each}
					</tr>
				{:else if error && data.length === 0}
					<tr class="bg-clay-950">
						{#if selectable}
							<td class="w-10 px-4 py-8"></td>
						{/if}
						{#each columns as col (col.key)}
							<td class="px-4 py-8 text-terracotta-500">
								{#if col.key === columns[0].key}
									{error}
								{/if}
							</td>
						{/each}
					</tr>
				{:else if data.length === 0}
					<tr class="bg-clay-950">
						{#if selectable}
							<td class="w-10 px-4 py-8"></td>
						{/if}
						{#each columns as col (col.key)}
							<td class="px-4 py-8 text-parchment-500">
								{#if col.key === columns[0].key}
									No results found.
								{/if}
							</td>
						{/each}
					</tr>
				{:else}
					{#each data as row (row[keyField])}
						<tr
							class="bg-clay-950 transition-colors {onRowClick
								? 'cursor-pointer hover:bg-clay-900 focus:outline-none focus-visible:ring-2 focus-visible:ring-gold-500'
								: ''}"
							tabindex={onRowClick ? '0' : undefined}
							role={onRowClick ? 'link' : undefined}
							onclick={onRowClick
								? (e) => {
										if (e.target.closest('button, a, input')) return;
										onRowClick(row);
									}
								: undefined}
							onkeydown={onRowClick
								? (e) => {
										if (e.key === 'Enter') onRowClick(row);
									}
								: undefined}
						>
							{#if selectable}
								<td class="w-10 px-4 py-3">
									<input
										type="checkbox"
										checked={selectedKeys.has(row[keyField])}
										onchange={() => toggleRow(row[keyField])}
										class="h-4 w-4 cursor-pointer accent-gold-500"
										onclick={(e) => e.stopPropagation()}
									/>
								</td>
							{/if}
							{#each columns as col (col.key)}
								<td
									class="px-4 py-3 text-parchment-200 {col.cellClass || ''} {onRowClick &&
									!col.noUnderline
										? 'underline decoration-parchment-500/30 underline-offset-2'
										: ''}"
								>
									{#if col.cell}
										{@html col.cell(row[col.key], row)}
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
