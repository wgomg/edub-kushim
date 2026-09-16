<script>
	import { onMount } from 'svelte';
	import { resolve } from '$app/paths';
	import { api } from '$lib/api';
	import { formatNumber } from '$lib/utils/html.js';
	import { affectedDocuments, changedDecisions, worklistTotal } from '$lib/utils/tagReconcile.js';

	const stripClass =
		'mb-4 flex flex-wrap items-center gap-x-3.5 gap-y-2 rounded-lg border border-clay-800 bg-clay-900 px-4 py-2.5 text-[0.8125rem] text-parchment-400';
	const chipReportOnly =
		'rounded-full border border-amber-600/40 bg-amber-600/20 px-2.5 py-0.5 font-mono text-[0.6875rem] font-semibold whitespace-nowrap text-amber-400';
	const chipApplied =
		'rounded-full border border-emerald-600/40 bg-emerald-600/20 px-2.5 py-0.5 font-mono text-[0.6875rem] font-semibold whitespace-nowrap text-emerald-500';
	const linkClass =
		'rounded-sm text-lapis-300 hover:text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none';

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

<div class={stripClass}>
	{#if status === 'loading'}
		<p class="text-parchment-500" aria-live="polite">Loading tag reconciliation…</p>
	{:else if status === 'missing'}
		<p class="text-parchment-500">
			Tag reconciliation has never run.
			<a href={resolve('/settings')} class={linkClass}>Open settings</a>
		</p>
	{:else if status === 'error'}
		<p class="text-terracotta-500" role="alert">Failed to load the tag reconciliation report.</p>
	{:else}
		<span
			class="h-[0.45rem] w-[0.45rem] shrink-0 rounded-full {reportOnly
				? 'bg-amber-400'
				: 'bg-emerald-400'}"
			aria-hidden="true"
		></span>
		<strong class="font-semibold text-parchment-200">Reconcile</strong>
		<span class={reportOnly ? chipReportOnly : chipApplied}>
			{reportOnly ? 'report only' : 'applied'}
		</span>
		<span class="font-mono text-xs text-parchment-500">last run {runDate}</span>
		<span class="text-parchment-300">
			<b class="font-semibold text-parchment-200 tabular-nums">{formatNumber(checked)}</b> pairs
			re-checked ·
			<b class="font-semibold text-parchment-200 tabular-nums">{formatNumber(changed)}</b> decisions
			changed ·
			<b class="font-semibold text-parchment-200 tabular-nums">{formatNumber(documents)}</b> docs affected
		</span>
		{#if provisionalParts.length > 0}
			<span class="text-parchment-500">
				New tags past the provisional grace window: {formatNumber(provisional.worklist)} —
				{provisionalParts.join(' · ')}
			</span>
		{/if}
		<a href={resolve(`/tasks/${data.task_id}`)} class="{linkClass} text-xs">View full report →</a>
	{/if}
</div>
