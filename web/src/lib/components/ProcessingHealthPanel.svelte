<script>
	import { resolve } from '$app/paths';
	import { formatDuration } from '$lib/utils/html.js';

	let { health = null } = $props();

	function successRateClass(rate) {
		if (rate == null) return 'text-parchment-500';
		if (rate > 0.9) return 'text-emerald-500';
		if (rate > 0.5) return 'text-amber-400';
		return 'text-terracotta-500';
	}

	function orphanedClass(count) {
		if (count > 0) return 'text-amber-400';
		return 'text-parchment-200';
	}

	function missingToolsClass(count) {
		if (count > 0) return 'text-terracotta-500';
		return 'text-parchment-200';
	}
</script>

<div class="space-y-6">
	{#if !health}
		<div class="text-center text-parchment-500">No processing data available</div>
	{:else}
		<div class="grid grid-cols-1 gap-4 sm:grid-cols-5">
			<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
				<p class="text-sm text-parchment-500">Success Rate (7d)</p>
				<p class="mt-1 text-lg font-semibold {successRateClass(health.success_rate)}">
					{new Intl.NumberFormat(undefined, {
						style: 'percent',
						maximumFractionDigits: 1
					}).format(health.success_rate)}
				</p>
			</div>
			<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
				<p class="text-sm text-parchment-500">Avg Duration (7d)</p>
				<p class="mt-1 text-lg font-semibold text-parchment-200">
					{formatDuration(health.avg_duration_ms)}
				</p>
			</div>
			<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
				<p class="text-sm text-parchment-500">Active Batches</p>
				<a
					href={resolve(`/tasks`)}
					class="mt-1 block text-lg font-semibold text-gold-500 hover:text-gold-600"
				>
					{health.active_batches}
				</a>
			</div>
			<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
				<p class="text-sm text-parchment-500">Orphaned Batches</p>
				<p class="mt-1 text-lg font-semibold {orphanedClass(health.orphaned_batches)}">
					{health.orphaned_batches}
				</p>
			</div>
			<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
				<p class="text-sm text-parchment-500">Missing Tools</p>
				<a
					href={resolve(`/settings`)}
					class="mt-1 block text-lg font-semibold {missingToolsClass(
						health.missing_tools
					)} hover:underline"
				>
					{health.missing_tools}
				</a>
			</div>
		</div>
	{/if}
</div>
