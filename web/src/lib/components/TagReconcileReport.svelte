<script>
	import { documentsLabel, formatNumber } from '$lib/utils/html.js';
	import {
		affectedDocuments,
		changedDecisions,
		notActedGroups,
		splitKeepFlips,
		sumReassigned,
		worklistReasons,
		worklistTotal
	} from '$lib/utils/tagReconcile.js';

	let { report, completedAt } = $props();

	const valid = $derived(report !== null && typeof report === 'object' && !Array.isArray(report));
	const reportOnly = $derived(!!report?.report_only);
	const disabled = $derived(!!report?.adjudicator_disabled);

	const checked = $derived(worklistTotal(report?.worklist));
	const confirmed = $derived(report?.confirmed ?? 0);
	const changed = $derived(changedDecisions(report));
	const documents = $derived(affectedDocuments(report));
	const why = $derived(worklistReasons(report?.worklist));

	const cleared = $derived(report?.invalidated_entries ?? []);
	const aliases = $derived((reportOnly ? report?.planned_aliases : report?.aliases) ?? []);
	const keepFlips = $derived(splitKeepFlips(report));
	const creates = $derived(keepFlips.creates);
	const retags = $derived(
		(reportOnly ? keepFlips.retags : (report?.retag_entries ?? [])).filter(
			(entry) => (entry?.reassigned ?? 0) > 0
		)
	);
	const aliasDocs = $derived(sumReassigned(aliases));
	const createsDocs = $derived(sumReassigned(creates));
	const retagDocs = $derived(sumReassigned(retags));
	const notActed = $derived(notActedGroups(report?.skips, report?.capped, report?.no_documents));
	const notActedTotal = $derived(notActed.reduce((sum, group) => sum + group.count, 0));

	const runDate = $derived(completedAt ? new Date(completedAt).toLocaleString() : '—');

	function plural(count, singular, pluralForm) {
		return count === 1 ? singular : pluralForm;
	}
</script>

{#if valid}
	<section class="rounded-lg border border-clay-800 bg-clay-900 p-5">
		<div class="flex flex-wrap items-center justify-between gap-2">
			<h2 class="text-lg font-semibold text-parchment-200">Tag reconciliation report</h2>
			<div class="flex items-center gap-2">
				<span
					class="inline-block rounded-full px-2.5 py-0.5 text-xs font-medium {reportOnly
						? 'bg-amber-600/20 text-amber-400'
						: 'bg-emerald-600/20 text-emerald-500'}"
				>
					{reportOnly ? 'Report only — nothing was changed' : 'Applied'}
				</span>
				<span class="text-xs text-parchment-500">{runDate}</span>
			</div>
		</div>

		<p class="mt-2 text-sm text-parchment-300">
			This run re-checked {formatNumber(checked)} tag {plural(checked, 'pair', 'pairs')}.
			{formatNumber(confirmed)}
			{plural(confirmed, 'decision was', 'decisions were')} confirmed.
			{formatNumber(changed)}
			{plural(changed, 'decision', 'decisions')}
			{#if creates.length > 0}
				{reportOnly ? 'would change' : 'changed'}
				({formatNumber(creates.length)}
				{plural(creates.length, 'tag created back', 'tags created back')}),
				{reportOnly ? 'affecting' : 'moving'}
				{documentsLabel(documents)}.
			{:else}
				{reportOnly ? 'would change' : 'changed'}, affecting {documentsLabel(documents)}.
			{/if}
		</p>

		{#if why.length > 0}
			<p class="mt-1 text-xs text-parchment-500">
				Why these pairs were re-checked: {why.join('; ')}.
			</p>
		{/if}

		{#if disabled}
			<div
				class="mt-4 rounded-lg border border-amber-600 bg-amber-500/10 p-3 text-sm text-amber-400"
			>
				The tag adjudicator is disabled in Settings, so runs can only report — they never change
				tags or documents. Enable it to allow real changes.
			</div>
		{:else}
			<div class="mt-4 space-y-2">
				{#if cleared.length > 0}
					<details class="group rounded-lg border border-clay-800 bg-clay-950">
						<summary
							class="flex cursor-pointer list-none items-center justify-between gap-2 px-4 py-3 text-sm font-medium text-parchment-200 [&::-webkit-details-marker]:hidden"
						>
							<span>
								Discard cached decisions for tags that no longer exist ({cleared.length})
							</span>
							<svg
								class="size-4 shrink-0 text-parchment-500 transition-transform group-open:rotate-90 motion-reduce:transition-none"
								viewBox="0 0 20 20"
								fill="currentColor"
								aria-hidden="true"
							>
								<path
									fill-rule="evenodd"
									d="M7.21 14.77a.75.75 0 0 1 .02-1.06L11.168 10 7.23 6.29a.75.75 0 1 1 1.04-1.08l4.5 4.25a.75.75 0 0 1 0 1.08l-4.5 4.25a.75.75 0 0 1-1.06-.02Z"
									clip-rule="evenodd"
								/>
							</svg>
						</summary>
						<ul class="space-y-1 border-t border-clay-800 px-4 py-3">
							{#each cleared as entry, i (i)}
								<li class="text-sm text-parchment-300">
									<span class="font-mono">{entry.query}</span>
									<span class="text-parchment-600" aria-hidden="true">→</span>
									<span class="font-mono">{entry.target}</span>
								</li>
							{/each}
						</ul>
					</details>
				{/if}

				{#if aliases.length > 0}
					<details class="group rounded-lg border border-clay-800 bg-clay-950">
						<summary
							class="flex cursor-pointer list-none items-center justify-between gap-2 px-4 py-3 text-sm font-medium text-parchment-200 [&::-webkit-details-marker]:hidden"
						>
							<span>
								{reportOnly ? 'Would alias' : 'Aliased tags'} ({aliases.length}
								{plural(aliases.length, 'pair', 'pairs')}, {documentsLabel(aliasDocs)})
							</span>
							<svg
								class="size-4 shrink-0 text-parchment-500 transition-transform group-open:rotate-90 motion-reduce:transition-none"
								viewBox="0 0 20 20"
								fill="currentColor"
								aria-hidden="true"
							>
								<path
									fill-rule="evenodd"
									d="M7.21 14.77a.75.75 0 0 1 .02-1.06L11.168 10 7.23 6.29a.75.75 0 1 1 1.04-1.08l4.5 4.25a.75.75 0 0 1 0 1.08l-4.5 4.25a.75.75 0 0 1-1.06-.02Z"
									clip-rule="evenodd"
								/>
							</svg>
						</summary>
						<ul class="space-y-1 border-t border-clay-800 px-4 py-3">
							{#each aliases as entry, i (i)}
								<li class="text-sm text-parchment-300">
									<span class="font-mono">{entry.query}</span>
									<span class="text-parchment-600" aria-hidden="true">→</span>
									<span class="font-mono">{entry.target}</span>
									<span class="text-parchment-500">({documentsLabel(entry.reassigned ?? 0)})</span>
								</li>
							{/each}
						</ul>
					</details>
				{/if}

				{#if reportOnly && creates.length > 0}
					<details class="group rounded-lg border border-clay-800 bg-clay-950">
						<summary
							class="flex cursor-pointer list-none items-center justify-between gap-2 px-4 py-3 text-sm font-medium text-parchment-200 [&::-webkit-details-marker]:hidden"
						>
							<span>
								Would create tags ({creates.length}
								{plural(creates.length, 'pair', 'pairs')}, {documentsLabel(createsDocs)})
							</span>
							<svg
								class="size-4 shrink-0 text-parchment-500 transition-transform group-open:rotate-90 motion-reduce:transition-none"
								viewBox="0 0 20 20"
								fill="currentColor"
								aria-hidden="true"
							>
								<path
									fill-rule="evenodd"
									d="M7.21 14.77a.75.75 0 0 1 .02-1.06L11.168 10 7.23 6.29a.75.75 0 1 1 1.04-1.08l4.5 4.25a.75.75 0 0 1 0 1.08l-4.5 4.25a.75.75 0 0 1-1.06-.02Z"
									clip-rule="evenodd"
								/>
							</svg>
						</summary>
						<ul class="space-y-1 border-t border-clay-800 px-4 py-3">
							{#each creates as entry, i (i)}
								<li class="text-sm text-parchment-300">
									create tag <span class="font-mono">{entry.query}</span>
									<span class="text-parchment-600" aria-hidden="true">—</span>
									<span class="text-parchment-500">previously replaced by</span>
									<span class="font-mono">{entry.target}</span>
									<span class="text-parchment-600" aria-hidden="true">—</span>
									<span class="text-parchment-500">
										{(entry.reassigned ?? 0) > 0
											? `${documentsLabel(entry.reassigned)} move back`
											: 'no documents to move'}
									</span>
								</li>
							{/each}
						</ul>
					</details>
				{/if}

				{#if retags.length > 0}
					<details class="group rounded-lg border border-clay-800 bg-clay-950">
						<summary
							class="flex cursor-pointer list-none items-center justify-between gap-2 px-4 py-3 text-sm font-medium text-parchment-200 [&::-webkit-details-marker]:hidden"
						>
							<span>
								{reportOnly ? 'Would re-tag documents' : 'Re-tagged documents'} ({retags.length}
								{plural(retags.length, 'pair', 'pairs')}, {documentsLabel(retagDocs)})
							</span>
							<svg
								class="size-4 shrink-0 text-parchment-500 transition-transform group-open:rotate-90 motion-reduce:transition-none"
								viewBox="0 0 20 20"
								fill="currentColor"
								aria-hidden="true"
							>
								<path
									fill-rule="evenodd"
									d="M7.21 14.77a.75.75 0 0 1 .02-1.06L11.168 10 7.23 6.29a.75.75 0 1 1 1.04-1.08l4.5 4.25a.75.75 0 0 1 0 1.08l-4.5 4.25a.75.75 0 0 1-1.06-.02Z"
									clip-rule="evenodd"
								/>
							</svg>
						</summary>
						<ul class="space-y-1 border-t border-clay-800 px-4 py-3">
							{#each retags as entry, i (i)}
								<li class="text-sm text-parchment-300">
									move {documentsLabel(entry.reassigned ?? 0)} from
									<span class="font-mono">{entry.target}</span>
									back to
									<span class="font-mono">{entry.query}</span>
								</li>
							{/each}
						</ul>
					</details>
				{/if}

				{#if notActedTotal > 0}
					<details class="group rounded-lg border border-clay-800 bg-clay-950">
						<summary
							class="flex cursor-pointer list-none items-center justify-between gap-2 px-4 py-3 text-sm font-medium text-parchment-200 [&::-webkit-details-marker]:hidden"
						>
							<span>Not acted on ({notActedTotal} pairs)</span>
							<svg
								class="size-4 shrink-0 text-parchment-500 transition-transform group-open:rotate-90 motion-reduce:transition-none"
								viewBox="0 0 20 20"
								fill="currentColor"
								aria-hidden="true"
							>
								<path
									fill-rule="evenodd"
									d="M7.21 14.77a.75.75 0 0 1 .02-1.06L11.168 10 7.23 6.29a.75.75 0 1 1 1.04-1.08l4.5 4.25a.75.75 0 0 1 0 1.08l-4.5 4.25a.75.75 0 0 1-1.06-.02Z"
									clip-rule="evenodd"
								/>
							</svg>
						</summary>
						<ul class="space-y-1 border-t border-clay-800 px-4 py-3">
							{#each notActed as group, i (i)}
								<li class="flex items-baseline justify-between gap-3 text-sm text-parchment-300">
									<span>{group.label}</span>
									<span class="shrink-0 text-parchment-500 tabular-nums">{group.count}</span>
								</li>
							{/each}
						</ul>
					</details>
				{/if}
			</div>
		{/if}
	</section>
{/if}
