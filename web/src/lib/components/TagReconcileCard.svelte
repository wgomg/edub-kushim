<script>
	import { onMount } from 'svelte';
	import { resolve } from '$app/paths';
	import { api } from '$lib/api';
	import { formatNumber } from '$lib/utils/html.js';
	import { affectedDocuments, changedDecisions, worklistTotal } from '$lib/utils/tagReconcile.js';

	let status = $state('loading');
	let data = $state(null);

	onMount(async () => {
		const res = await api.tags.reconcileReport();
		if (res.ok && res.data && typeof res.data === 'object') {
			data = res.data;
			status = 'ok';
		} else if (res.status === 404) {
			status = 'missing';
		} else {
			status = 'error';
		}
	});

	const report = $derived(
		data?.result && typeof data.result === 'object' && !Array.isArray(data.result)
			? data.result
			: {}
	);
	const reportOnly = $derived(!!report.report_only);
	const checked = $derived(worklistTotal(report.worklist));
	const changed = $derived(changedDecisions(report));
	const documents = $derived(affectedDocuments(report));
	const runDate = $derived(data?.completed_at ? new Date(data.completed_at).toLocaleString() : '—');
	const provisional = $derived(report.provisional ?? null);
	const provisionalParts = $derived.by(() => {
		const p = provisional;
		if (!p?.worklist) return [];
		const parts = [];
		if (p.promoted) parts.push(`${formatNumber(p.promoted)} promoted`);
		if (p.rejudged) parts.push(`${formatNumber(p.rejudged)} re-judged`);
		if (p.deferred) parts.push(`${formatNumber(p.deferred)} deferred to review`);
		if (p.no_judge) parts.push(`${formatNumber(p.no_judge)} awaiting the judge`);
		if (p.zero_assignment) parts.push(`${formatNumber(p.zero_assignment)} without documents`);
		if (p.skipped) parts.push(`${formatNumber(p.skipped)} skipped`);
		if (p.failed) parts.push(`${formatNumber(p.failed)} failed`);
		if (parts.length === 0) parts.push('nothing to act on');
		return parts;
	});
</script>

<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
	{#if status === 'loading'}
		<p class="text-sm text-parchment-500" aria-live="polite">Loading tag reconciliation…</p>
	{:else if status === 'missing'}
		<h2 class="text-sm font-semibold text-parchment-200">Tag reconciliation</h2>
		<p class="mt-1 text-sm text-parchment-500">
			Tag reconciliation has never run.
			<a
				href={resolve('/settings')}
				class="rounded-sm text-lapis-400 hover:text-lapis-300 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
				>Open settings</a
			>
		</p>
	{:else if status === 'error'}
		<p class="text-sm text-terracotta-500" role="alert">
			Failed to load the tag reconciliation report.
		</p>
	{:else}
		<div class="flex flex-wrap items-center justify-between gap-2">
			<h2 class="text-sm font-semibold text-parchment-200">Tag reconciliation</h2>
			<span
				class="inline-block rounded-full px-2.5 py-0.5 text-xs font-medium {reportOnly
					? 'bg-amber-600/20 text-amber-400'
					: 'bg-emerald-600/20 text-emerald-500'}"
			>
				{reportOnly ? 'Report only' : 'Applied'}
			</span>
		</div>

		<p class="mt-1 text-xs text-parchment-500">
			{reportOnly
				? `Last checked ${runDate} — report only, nothing was changed`
				: `Last run ${runDate}`}
		</p>

		<div class="mt-3 grid grid-cols-3 gap-3">
			<div>
				<p class="text-lg font-semibold text-parchment-200 tabular-nums">
					{formatNumber(checked)}
				</p>
				<p class="text-xs text-parchment-500">pairs re-checked</p>
			</div>
			<div>
				<p class="text-lg font-semibold text-parchment-200 tabular-nums">
					{formatNumber(changed)}
				</p>
				<p class="text-xs text-parchment-500">decisions changed</p>
			</div>
			<div>
				<p class="text-lg font-semibold text-parchment-200 tabular-nums">
					{formatNumber(documents)}
				</p>
				<p class="text-xs text-parchment-500">documents affected</p>
			</div>
		</div>

		{#if provisionalParts.length > 0}
			<p class="mt-3 text-xs text-parchment-500">
				New tags past the provisional grace window: {formatNumber(provisional.worklist)} —
				{provisionalParts.join(' · ')}
			</p>
		{/if}

		<a
			href={resolve(`/tasks/${data.task_id}`)}
			class="mt-3 inline-block rounded-sm text-xs text-lapis-400 hover:text-lapis-300 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
		>
			View full report
		</a>
	{/if}
</div>
