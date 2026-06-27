<script>
	import { onMount } from 'svelte';
	import { api } from '$lib/api';
	import { formatSize } from '$lib/utils/html.js';
	import StoragePanel from '$lib/components/StoragePanel.svelte';
	import BatchOverviewPanel from '$lib/components/BatchOverviewPanel.svelte';
	import ActivityTimeline from '$lib/components/ActivityTimeline.svelte';
	import DocumentAnalyticsPanel from '$lib/components/DocumentAnalyticsPanel.svelte';
	import ProcessingHealthPanel from '$lib/components/ProcessingHealthPanel.svelte';

	let health = $state();
	let recentDocs = $state([]);
	let dashboard = $state();

	onMount(async () => {
		[health, recentDocs, dashboard] = await Promise.all([
			api.health(),
			api.documents.list(15, 0),
			api.dashboard()
		]);
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
	<h1 class="text-2xl font-semibold text-parchment-200">Dashboard</h1>

	<div class="grid grid-cols-2 gap-4 sm:grid-cols-5">
		<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
			<p class="text-sm text-parchment-500">Server</p>
			<p class="mt-1 text-lg font-semibold text-parchment-200">{health?.status ?? '…'}</p>
		</div>
		<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
			<p class="text-sm text-parchment-500">Version</p>
			<p class="mt-1 text-lg font-semibold text-parchment-200">{health?.version ?? '…'}</p>
		</div>
		<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
			<p class="text-sm text-parchment-500">Total Files</p>
			<p class="mt-1 text-lg font-semibold text-parchment-200">{dashboard?.total_files ?? '…'}</p>
		</div>
		<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
			<p class="text-sm text-parchment-500">Total Batches</p>
			<p class="mt-1 text-lg font-semibold text-parchment-200">{dashboard?.total_batches ?? '…'}</p>
		</div>
		<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
			<p class="text-sm text-parchment-500">Total Size</p>
			<p class="mt-1 text-lg font-semibold text-parchment-200">
				{dashboard ? dashboard.total_size_gb.toFixed(2) + ' GB' : '…'}
			</p>
		</div>
	</div>

	{#if dashboard}
		<section>
			<h2 class="mb-3 text-lg font-semibold text-parchment-200">Task Status</h2>
			<div class="grid grid-cols-2 gap-4 sm:grid-cols-7">
				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<p class="text-sm text-parchment-500">Waiting</p>
					<p class="mt-1 text-lg font-semibold text-amber-400">{dashboard.waiting}</p>
				</div>
				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<p class="text-sm text-parchment-500">Pending</p>
					<p class="mt-1 text-lg font-semibold text-parchment-400">{dashboard.pending}</p>
				</div>
				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<p class="text-sm text-parchment-500">Processing</p>
					<p class="mt-1 text-lg font-semibold text-lapis-600">{dashboard.processing}</p>
				</div>
				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<p class="text-sm text-parchment-500">Completed</p>
					<p class="mt-1 text-lg font-semibold text-emerald-500">{dashboard.completed}</p>
				</div>
				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<p class="text-sm text-parchment-500">Failed</p>
					<p class="mt-1 text-lg font-semibold text-terracotta-500">{dashboard.failed}</p>
				</div>
				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<p class="text-sm text-parchment-500">Cancelled</p>
					<p class="mt-1 text-lg font-semibold text-parchment-500">{dashboard.cancelled}</p>
				</div>
				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<p class="text-sm text-parchment-500">Discarded</p>
					<p class="text-terracotta-400 mt-1 text-lg font-semibold">{dashboard.discarded}</p>
				</div>
			</div>
		</section>
	{/if}

	{#if dashboard}
		<section>
			<h2 class="mb-3 text-lg font-semibold text-parchment-200">Recent Batches</h2>
			<BatchOverviewPanel recentBatches={dashboard.recent_batches ?? []} />
		</section>

		<section>
			<h2 class="mb-3 text-lg font-semibold text-parchment-200">Recent Activity</h2>
			<ActivityTimeline activity={dashboard.activity ?? []} />
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
				mimeTypeBreakdown={dashboard.mime_type_breakdown ?? []}
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
				{#each docColumns as col}
					<div class="space-y-2">
						{#each col as doc}
							<a
								href="/documents/{doc.id}"
								class="block rounded-lg border border-clay-800 bg-clay-900 p-3 transition-colors hover:bg-clay-800"
							>
								<p class="truncate text-sm text-parchment-200">{doc.title || 'Untitled'}</p>
								<p class="text-xs text-parchment-500">
									{doc.mime_type} — {formatSize(doc.file_size)}
								</p>
							</a>
						{/each}
					</div>
				{/each}
			</div>
		</section>
	{/if}
</div>
