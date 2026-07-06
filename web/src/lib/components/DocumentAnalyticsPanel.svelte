<script>
	import { goto } from '$app/navigation';
	import { filterStore } from '$lib/stores/filterStore.js';
	import { defaultFilter } from '$lib/stores/searchFilter.js';
	import { formatNumber } from '$lib/utils/html.js';

	let { analytics = null } = $props();

	const chartColors = ['#c9953a', '#3b5e8a', '#b84a3a', '#9a8c78', '#b07d2e', '#2e4a6e', '#5a3a2a'];

	const maxLangCount = $derived(() => {
		if (!analytics?.language_distribution?.length) return 1;
		return Math.max(...analytics.language_distribution.map((d) => d.count));
	});

	const maxDocTypeCount = $derived(() => {
		if (!analytics?.document_type_distribution?.length) return 1;
		return Math.max(...analytics.document_type_distribution.map((d) => d.count));
	});

	const maxTagCount = $derived(() => {
		if (!analytics?.tag_frequency?.length) return 1;
		return Math.max(...analytics.tag_frequency.map((d) => d.count));
	});

	function langLabel(lang) {
		if (!lang || lang === 'und') return 'Undetermined';
		return lang.toUpperCase();
	}
</script>

<div class="space-y-6">
	<div class="grid grid-cols-1 gap-4 sm:grid-cols-3">
		<a
			href="/documents"
			class="block cursor-pointer rounded-lg border border-clay-800 bg-clay-900 p-4 hover:bg-clay-800 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
			onclick={(e) => {
				e.preventDefault();
				filterStore.set({ ...defaultFilter, missingLanguage: true });
				goto('/documents');
			}}
		>
			<p class="text-sm text-parchment-500">Missing Language</p>
			<p class="mt-1 text-lg font-semibold text-parchment-200">
				{formatNumber(analytics?.missing_language_count ?? 0)}
			</p>
		</a>
		<a
			href="/documents"
			class="block cursor-pointer rounded-lg border border-clay-800 bg-clay-900 p-4 hover:bg-clay-800 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
			onclick={(e) => {
				e.preventDefault();
				filterStore.set({ ...defaultFilter, missingType: true });
				goto('/documents');
			}}
		>
			<p class="text-sm text-parchment-500">Missing Type</p>
			<p class="mt-1 text-lg font-semibold text-parchment-200">
				{formatNumber(analytics?.missing_type_count ?? 0)}
			</p>
		</a>
		<a
			href="/documents"
			class="block cursor-pointer rounded-lg border border-clay-800 bg-clay-900 p-4 hover:bg-clay-800 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
			onclick={(e) => {
				e.preventDefault();
				filterStore.set({ ...defaultFilter, untagged: true });
				goto('/documents');
			}}
		>
			<p class="text-sm text-parchment-500">Untagged Documents</p>
			<p class="mt-1 text-lg font-semibold text-parchment-200">
				{formatNumber(analytics?.missing_tags_count ?? 0)}
			</p>
		</a>
	</div>

	<div class="flex flex-col gap-4 sm:flex-row">
		{#if analytics?.tag_frequency?.length}
			<section class="min-w-0 flex-1">
				<h3 class="mb-3 text-base font-semibold text-parchment-200">Top Tags</h3>
				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<div class="overflow-x-auto">
						<table class="w-full text-sm">
							<thead>
								<tr class="border-b border-clay-800 text-left text-parchment-500">
									<th class="pr-4 pb-2 font-medium">Tag</th>
									<th class="pb-2 font-medium">Documents</th>
								</tr>
							</thead>
							<tbody>
								{#each analytics.tag_frequency as item, i}
									<tr
										class="cursor-pointer border-b border-clay-800/50 hover:bg-clay-800 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
										tabindex="0"
										role="link"
										onclick={() => {
											filterStore.set({ ...defaultFilter, tags: [item.label] });
											goto('/documents');
										}}
										onkeydown={(e) => {
											if (e.key === 'Enter') {
												filterStore.set({ ...defaultFilter, tags: [item.label] });
												goto('/documents');
											}
										}}
									>
										<td class="py-2 pr-4 text-parchment-200">
											<span
												class="inline-block h-2.5 w-2.5 rounded-full"
												style="background-color: {chartColors[i % chartColors.length]}"
											></span>
											<span class="ml-2">{item.label}</span>
										</td>
										<td class="py-2 text-parchment-400">
											<div class="flex items-center gap-2">
												<div class="h-2 w-24 overflow-hidden rounded-full bg-clay-800 sm:w-32">
													<div
														class="h-2 rounded-full transition-[width,background-color]"
														style="width: {(item.count / maxTagCount()) *
															100}%; background-color: {chartColors[i % chartColors.length]}"
													></div>
												</div>
												<span class="tabular-nums">{formatNumber(item.count)}</span>
											</div>
										</td>
									</tr>
								{/each}
							</tbody>
						</table>
					</div>
				</div>
			</section>
		{/if}

		{#if analytics?.language_distribution?.length}
			<section class="min-w-0 flex-1">
				<h3 class="mb-3 text-base font-semibold text-parchment-200">Language Distribution</h3>
				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<div class="space-y-2">
						{#each analytics.language_distribution as item, i}
							<a
								href="/documents"
								class="flex cursor-pointer items-center gap-3 hover:bg-clay-800 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
								onclick={(e) => {
									e.preventDefault();
									filterStore.set({ ...defaultFilter, language: item.label });
									goto('/documents');
								}}
							>
								<div
									class="h-4 w-4 shrink-0 rounded"
									style="background-color: {chartColors[i % chartColors.length]}"
								></div>
								<div class="flex-1">
									<div class="flex justify-between text-sm">
										<span class="truncate text-parchment-200">{langLabel(item.label)}</span>
										<span class="shrink-0 text-parchment-500 tabular-nums"
											>{formatNumber(item.count)}</span
										>
									</div>
									<div class="mt-1 h-2 w-full rounded-full bg-clay-800">
										<div
											class="h-2 rounded-full transition-[width,background-color]"
											style="width: {(item.count / maxLangCount()) *
												100}%; background-color: {chartColors[i % chartColors.length]}"
										></div>
									</div>
								</div>
							</a>
						{/each}
					</div>
				</div>
			</section>
		{/if}

		{#if analytics?.document_type_distribution?.length}
			<section class="min-w-0 flex-1">
				<h3 class="mb-3 text-base font-semibold text-parchment-200">Document Type Distribution</h3>
				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<div class="space-y-2">
						{#each analytics.document_type_distribution as item, i}
							<a
								href="/documents"
								class="flex cursor-pointer items-center gap-3 hover:bg-clay-800 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
								onclick={(e) => {
									e.preventDefault();
									filterStore.set({ ...defaultFilter, documentType: item.label });
									goto('/documents');
								}}
							>
								<div
									class="h-4 w-4 shrink-0 rounded"
									style="background-color: {chartColors[i % chartColors.length]}"
								></div>
								<div class="flex-1">
									<div class="flex justify-between text-sm">
										<span class="truncate text-parchment-200">{item.label}</span>
										<span class="shrink-0 text-parchment-500 tabular-nums"
											>{formatNumber(item.count)}</span
										>
									</div>
									<div class="mt-1 h-2 w-full rounded-full bg-clay-800">
										<div
											class="h-2 rounded-full transition-[width,background-color]"
											style="width: {(item.count / maxDocTypeCount()) *
												100}%; background-color: {chartColors[i % chartColors.length]}"
										></div>
									</div>
								</div>
							</a>
						{/each}
					</div>
				</div>
			</section>
		{/if}
	</div>
</div>
