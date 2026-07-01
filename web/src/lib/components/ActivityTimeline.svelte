<script>
	let { activity = [] } = $props();

	function formatDate(dateStr) {
		if (!dateStr) return '—';
		try {
			const d = new Date(dateStr);
			return d.toLocaleDateString(undefined, {
				month: 'short',
				day: 'numeric',
				hour: '2-digit',
				minute: '2-digit'
			});
		} catch {
			return dateStr;
		}
	}

	const dotColors = {
		document_uploaded: 'bg-emerald-500',
		task_completed: 'bg-lapis-500',
		task_failed: 'bg-terracotta-500',
		batch_created: 'bg-gold-500'
	};

	const eventLabels = {
		document_uploaded: 'uploaded',
		task_completed: 'completed',
		task_failed: 'failed',
		batch_created: 'batch'
	};
</script>

<div class="rounded-lg border border-clay-800 bg-clay-900">
	{#if activity.length === 0}
		<div class="p-6 text-center text-parchment-500">No recent activity</div>
	{:else}
		<div class="divide-y divide-clay-800/50">
			{#each activity as event}
				<div class="flex items-center gap-2 px-4 py-2">
					<div
						class="h-2 w-2 shrink-0 rounded-full {dotColors[event.event_type] ||
							'bg-parchment-500'}"
					></div>
					<a
						href={event.link}
						class="min-w-0 truncate text-sm font-medium text-parchment-200 transition-colors hover:text-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					>
						{event.title}
					</a>
					<span class="shrink-0 text-xs text-parchment-500"
						>{eventLabels[event.event_type] || event.event_type}</span
					>
					<span class="ml-auto shrink-0 text-xs text-parchment-400"
						>{formatDate(event.timestamp)}</span
					>
				</div>
			{/each}
		</div>
	{/if}
</div>
