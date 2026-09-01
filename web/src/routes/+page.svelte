<script>
	import { onMount, onDestroy } from 'svelte';
	import { resolve } from '$app/paths';
	import { api } from '$lib/api';
	import { formatSize } from '$lib/utils/html.js';
	import StoragePanel from '$lib/components/StoragePanel.svelte';
	import BatchOverviewPanel from '$lib/components/BatchOverviewPanel.svelte';
	import ActiveTasksStrip from '$lib/components/ActiveTasksStrip.svelte';
	import DocumentAnalyticsPanel from '$lib/components/DocumentAnalyticsPanel.svelte';
	import ProcessingHealthPanel from '$lib/components/ProcessingHealthPanel.svelte';

	let recentDocs = $state([]);
	let dashboard = $state();
	let autoRefresh = $state(false);
	let refreshInterval = null;
	let fetching = $state(false);

	async function fetchDashboard() {
		if (fetching) return;
		fetching = true;
		try {
			[recentDocs, dashboard] = await Promise.all([api.documents.list(15, 0), api.dashboard()]);
		} finally {
			fetching = false;
		}
	}

	onMount(() => {
		fetchDashboard();
	});

	$effect(() => {
		if (refreshInterval) clearInterval(refreshInterval);
		refreshInterval = null;
		if (autoRefresh) {
			refreshInterval = setInterval(() => {
				fetchDashboard();
			}, 10000);
		}
		return () => {
			if (refreshInterval) clearInterval(refreshInterval);
		};
	});

	onDestroy(() => {
		if (refreshInterval) clearInterval(refreshInterval);
	});

	function chunk(arr, size) {
		const result = [];
		for (let i = 0; i < arr.length; i += size) {
			result.push(arr.slice(i, i + size));
		}
		return result;
	}

	let docColumns = $derived(chunk(recentDocs, 5));
</script>

<div class="space-y-6">
	<div class="flex items-center justify-between">
		<h1 class="text-2xl font-semibold text-parchment-200">Dashboard</h1>
		<button
			onclick={() => (autoRefresh = !autoRefresh)}
			aria-pressed={autoRefresh}
			class="rounded-md px-3 py-1.5 text-xs font-medium transition-colors focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none {autoRefresh
				? 'bg-gold-500 text-clay-950 hover:bg-gold-600'
				: 'bg-clay-800 text-parchment-400 hover:bg-clay-700'}"
		>
			{autoRefresh ? '⟳ On' : '⟳ Off'}
		</button>
	</div>

	<div class="grid grid-cols-2 gap-4 sm:grid-cols-5">
		<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
			<p class="text-sm text-parchment-500">Total Files</p>
			<p class="mt-1 text-lg font-semibold text-parchment-200">{dashboard?.total_files ?? '…'}</p>
		</div>
		<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
			<p class="text-sm text-parchment-500">Total Batches</p>
			<p class="mt-1 text-lg font-semibold text-parchment-200">{dashboard?.total_batches ?? '…'}</p>
		</div>
		<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
			<p class="text-sm text-parchment-500">Files in Inbox</p>
			<p class="mt-1 text-lg font-semibold text-parchment-200">{dashboard?.inbox_files ?? '…'}</p>
		</div>
		<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
			<p class="text-sm text-parchment-500">Originals Size</p>
			<p class="mt-1 text-lg font-semibold text-parchment-200">
				{dashboard ? formatSize(dashboard.originals_size_bytes) : '…'}
			</p>
		</div>
		<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
			<p class="text-sm text-parchment-500">Processed Size</p>
			<p class="mt-1 text-lg font-semibold text-parchment-200">
				{dashboard ? formatSize(dashboard.processed_size_bytes) : '…'}
			</p>
		</div>
	</div>

	{#if dashboard}
		<section>
			<h2 class="mb-3 text-lg font-semibold text-parchment-200">Task Status</h2>
			<div class="grid grid-cols-2 gap-4 sm:grid-cols-7">
				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<p class="text-sm text-parchment-500">Waiting</p>
					<p class="mt-1 text-lg font-semibold text-amber-400 tabular-nums">{dashboard.waiting}</p>
				</div>
				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<p class="text-sm text-parchment-500">Pending</p>
					<p class="mt-1 text-lg font-semibold text-parchment-400 tabular-nums">
						{dashboard.pending}
					</p>
				</div>
				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<p class="text-sm text-parchment-500">Processing</p>
					<p class="mt-1 text-lg font-semibold text-lapis-600 tabular-nums">
						{dashboard.processing}
					</p>
				</div>
				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<p class="text-sm text-parchment-500">Completed</p>
					<p class="mt-1 text-lg font-semibold text-emerald-500 tabular-nums">
						{dashboard.completed}
					</p>
				</div>
				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<p class="text-sm text-parchment-500">Failed</p>
					<p class="mt-1 text-lg font-semibold text-terracotta-500 tabular-nums">
						{dashboard.failed}
					</p>
				</div>
				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<p class="text-sm text-parchment-500">Cancelled</p>
					<p class="mt-1 text-lg font-semibold text-parchment-500 tabular-nums">
						{dashboard.cancelled}
					</p>
				</div>
				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<p class="text-sm text-parchment-500">Discarded</p>
					<p class="mt-1 text-lg font-semibold text-terracotta-400 tabular-nums">
						{dashboard.discarded}
					</p>
				</div>
			</div>
		</section>
	{/if}

	{#if dashboard}
		<ActiveTasksStrip
			tasks={dashboard.running_tasks?.tasks ?? []}
			count={dashboard.running_tasks?.count ?? 0}
		/>

		<section>
			<h2 class="mb-3 text-lg font-semibold text-parchment-200">Recent Batches</h2>
			<BatchOverviewPanel recentBatches={dashboard.recent_batches ?? []} />
		</section>

		{#if dashboard.analytics}
			<section>
				<h2 class="mb-3 text-lg font-semibold text-parchment-200">Document Analytics</h2>
				<DocumentAnalyticsPanel analytics={dashboard.analytics} />
			</section>
		{/if}

		{#if dashboard.processing_health}
			<section>
				<h2 class="mb-3 text-lg font-semibold text-parchment-200">Processing Health</h2>
				<ProcessingHealthPanel health={dashboard.processing_health} />
			</section>
		{/if}
	{/if}

	{#if dashboard}
		<section>
			<h2 class="mb-3 text-lg font-semibold text-parchment-200">Storage</h2>
			<StoragePanel
				originalTypeBreakdown={dashboard.original_type_breakdown ?? []}
				storageTrend={dashboard.storage_trend ?? []}
				avgFileSizeBytes={dashboard.avg_file_size_bytes ?? 0}
				totalPages={dashboard.total_pages ?? 0}
				totalWords={dashboard.total_words ?? 0}
			/>
		</section>
	{/if}

	{#if recentDocs.length > 0}
		<section>
			<h2 class="mb-3 text-lg font-semibold text-parchment-200">Recent Documents</h2>
			<div class="grid grid-cols-1 gap-4 sm:grid-cols-3">
				{#each docColumns as col, i (i)}
					<div class="min-w-0 space-y-2">
						{#each col as doc (doc.id)}
							<a
								href={resolve(`/documents/${doc.id}`)}
								class="block rounded-lg border border-clay-800 bg-clay-900 p-3 transition-colors hover:bg-clay-800"
							>
								<p class="truncate text-sm text-parchment-200">{doc.title || 'Untitled'}</p>
								<p class="text-xs text-parchment-500">
									{doc.original_type} — {formatSize(doc.file_size)}
								</p>
							</a>
						{/each}
					</div>
				{/each}
			</div>
		</section>
	{/if}
</div>
