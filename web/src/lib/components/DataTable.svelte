<script>
	import { onMount } from 'svelte';

	/**
	 * @typedef {Object} Column
	 * @property {string} key - Accessor key on the row object
	 * @property {string} label - Column header text
	 * @property {boolean} [sortable] - Enable click-to-sort on this column
	 * @property {Function} [cell] - Custom cell formatter: (value, row) => string
	 * @property {string} [headerClass] - Extra classes for <th>
	 * @property {string} [cellClass] - Extra classes for <td>
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
	 * title?: string
	 * }}
	 * */
	let {
		columns = [],
		fetch = async () => [],
		onRowClick = null,
		pageSizes = [10, 25, 50, 100],
		defaultPageSize = 25,
		keyField = 'id',
		title = ''
	} = $props();

	let data = $state([]);
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
			const first = columns.find((c) => c.sortable);
			if (first) sortBy = first.key;
			initialized = true;
		}
	});

	onMount(() => {
		load();
	});

	async function load() {
		if (data.length === 0) loading = true;
		error = '';
		try {
			const result = await fetch({
				sortBy: sortBy || columns[0]?.key || 'created_at',
				sortOrder,
				limit: pageSize,
				offset: pageIndex * pageSize
			});
			data = result;
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load data';
			data = [];
		} finally {
			loading = false;
		}
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
					value={pageSize}
					onchange={changePageSize}
					class="rounded-lg border border-clay-800 bg-clay-900 px-3 py-1.5 text-parchment-200"
				>
					{#each pageSizes as size}
						<option value={size}>{size}</option>
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
					{#each columns as col, i (col.key)}
						<th
							class="px-4 py-3 font-medium whitespace-nowrap transition-colors select-none {col.sortable
								? 'cursor-pointer hover:bg-clay-800 hover:text-parchment-200'
								: ''} {col.headerClass || ''}"
							style="width: {col.width ?? 'auto'}; {col.minWidth &&
								`min-width: ${col.minWidth};`} {col.maxWidth && `max-width: ${col.maxWidth};`}"
							scope="col"
							onclick={col.sortable ? () => toggleSort(col.key) : undefined}
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
								? 'cursor-pointer hover:bg-clay-900'
								: ''}"
							onclick={onRowClick ? () => onRowClick(row) : undefined}
						>
							{#each columns as col (col.key)}
								<td
									class="px-4 py-3 text-parchment-200 {col.cellClass || ''} {onRowClick
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
		<div class="flex items-center justify-between text-sm">
			<p class="text-parchment-500">
				Showing {showing} result{showing !== 1 ? 's' : ''}
				{#if pageIndex > 0}
					(starting at #{pageIndex * pageSize + 1})
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
					disabled={data.length < pageSize}
					onclick={nextPage}
					class="rounded-lg border border-clay-800 bg-clay-900 px-3 py-1.5 text-parchment-400 transition-colors hover:bg-clay-800 disabled:opacity-40"
				>
					Next
				</button>
			</div>
		</div>
	{/if}
</div>
