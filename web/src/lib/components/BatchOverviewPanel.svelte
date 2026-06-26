<script>
	import { formatDuration } from '$lib/utils/html.js';

	let { recentBatches = [] } = $props();

	function truncateId(id) {
		if (!id) return '';
		return id.length > 8 ? id.slice(0, 8) + '…' : id;
	}

	function formatDate(dateStr) {
		if (!dateStr) return '—';
		try {
			const d = new Date(dateStr);
			return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
		} catch {
			return dateStr;
		}
	}

	function statusBarStyle(batch) {
		const total = batch.total || 1;
		const segments = [
			{ count: batch.completed, class: 'bg-emerald-500' },
			{ count: batch.failed, class: 'bg-terracotta-500' },
			{ count: batch.pending, class: 'bg-amber-400' },
			{ count: batch.processing, class: 'bg-lapis-600' },
			{ count: batch.cancelled, class: 'bg-parchment-500' },
			{ count: batch.discarded, class: 'bg-terracotta-400' }
		];
		return segments
			.filter((s) => s.count > 0)
			.map((s) => ({ width: (s.count / total) * 100, class: s.class }));
	}
</script>

<div class="rounded-lg border border-clay-800 bg-clay-900">
	{#if recentBatches.length === 0}
		<div class="p-6 text-center text-parchment-500">No batches yet</div>
	{:else}
		<div class="overflow-x-auto">
			<table class="w-full text-sm">
				<thead>
					<tr class="border-b border-clay-800 text-left text-parchment-500">
						<th class="px-4 py-3 font-medium">Batch ID</th>
						<th class="px-4 py-3 font-medium">Source</th>
						<th class="px-4 py-3 font-medium">Created</th>
						<th class="px-4 py-3 font-medium">Status</th>
						<th class="px-4 py-3 font-medium">Duration</th>
						<th class="px-4 py-3 font-medium">Owner</th>
					</tr>
				</thead>
				<tbody>
					{#each recentBatches as batch}
						<tr class="border-b border-clay-800/50 hover:bg-clay-800/50">
							<td class="px-4 py-3">
								<a
									href="/tasks?batch={batch.batch_id}"
									class="font-mono text-xs text-gold-500 hover:text-gold-600"
								>
									{truncateId(batch.batch_id)}
								</a>
							</td>
							<td class="px-4 py-3 text-parchment-400">{batch.source}</td>
							<td class="px-4 py-3 text-parchment-400">{formatDate(batch.created_at)}</td>
							<td class="px-4 py-3">
								<div class="flex h-4 w-28 overflow-hidden rounded-full bg-clay-800 sm:w-36">
									{#each statusBarStyle(batch) as seg}
										<div
											class="{seg.class} h-full transition-[width]"
											style="width: {seg.width}%"
										></div>
									{/each}
								</div>
							</td>
							<td class="px-4 py-3 text-parchment-400">{formatDuration(batch.duration_ms)}</td>
							<td class="px-4 py-3">
								{#if batch.orphaned}
									<span class="inline-block rounded-full bg-terracotta-500/20 px-2 py-0.5 text-xs font-medium text-terracotta-400">
										orphan
									</span>
								{:else if batch.owner_state === 'live'}
									<span class="inline-block rounded-full bg-emerald-500/20 px-2 py-0.5 text-xs font-medium text-emerald-400">
										live
									</span>
								{:else if batch.owner_state === 'stale'}
									<span class="inline-block rounded-full bg-amber-400/20 px-2 py-0.5 text-xs font-medium text-amber-400">
										stale
									</span>
								{:else}
									<span class="text-parchment-500">—</span>
								{/if}
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	{/if}
</div>
