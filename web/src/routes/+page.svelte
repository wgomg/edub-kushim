<script>
	import { onMount } from 'svelte';
	import { api } from '$lib/api';

	let health = $state();
	let summary = $state();
	let recentDocs = $state([]);

	onMount(async () => {
		[health, summary, recentDocs] = await Promise.all([
			api.health(),
			api.summary.get(),
			api.documents.list(15, 0)
		]);
	});

	function formatSize(bytes) {
		if (bytes < 1024) return bytes + ' B';
		if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
		return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
	}

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
			<p class="mt-1 text-lg font-semibold text-parchment-200">{summary?.total_files ?? '…'}</p>
		</div>
		<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
			<p class="text-sm text-parchment-500">Total Batches</p>
			<p class="mt-1 text-lg font-semibold text-parchment-200">{summary?.total_batches ?? '…'}</p>
		</div>
		<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
			<p class="text-sm text-parchment-500">Total Size</p>
			<p class="mt-1 text-lg font-semibold text-parchment-200">
				{summary ? summary.total_size_gb.toFixed(2) + ' GB' : '…'}
			</p>
		</div>
	</div>

	{#if summary}
		<section>
			<h2 class="mb-3 text-lg font-semibold text-parchment-200">Task Status</h2>
			<div class="grid grid-cols-2 gap-4 sm:grid-cols-6">
				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<p class="text-sm text-parchment-500">Waiting</p>
					<p class="mt-1 text-lg font-semibold text-amber-400">{summary.waiting}</p>
				</div>
				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<p class="text-sm text-parchment-500">Pending</p>
					<p class="mt-1 text-lg font-semibold text-parchment-400">{summary.pending}</p>
				</div>
				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<p class="text-sm text-parchment-500">Processing</p>
					<p class="mt-1 text-lg font-semibold text-lapis-600">{summary.processing}</p>
				</div>
				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<p class="text-sm text-parchment-500">Completed</p>
					<p class="mt-1 text-lg font-semibold text-emerald-500">{summary.completed}</p>
				</div>
				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<p class="text-sm text-parchment-500">Failed</p>
					<p class="mt-1 text-lg font-semibold text-terracotta-500">{summary.failed}</p>
				</div>
				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<p class="text-sm text-parchment-500">Cancelled</p>
					<p class="mt-1 text-lg font-semibold text-parchment-500">{summary.cancelled}</p>
				</div>
			</div>
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
