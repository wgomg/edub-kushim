<script>
	import { resolve } from '$app/paths';
	import { defaultFilter, serializeFilter } from '$lib/stores/searchFilter.js';
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

	function filterUrl(partial) {
		const q = serializeFilter({ ...defaultFilter, ...partial });
		return `/documents?q=${encodeURIComponent(q)}`;
	}
</script>

<div class="space-y-6">
	<div class="grid grid-cols-1 gap-4 sm:grid-cols-3">
		<a
			href={resolve(filterUrl({ missingLanguage: true }))}
			class="block cursor-pointer rounded-lg border border-clay-800 bg-clay-900 p-4 hover:bg-clay-800 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
		>
			<p class="text-sm text-parchment-500">Missing Language</p>
			<p class="mt-1 text-lg font-semibold text-parchment-200">
				{formatNumber(analytics?.missing_language_count ?? 0)}
			</p>
		</a>
		<a
			href={resolve(filterUrl({ missingType: true }))}
			class="block cursor-pointer rounded-lg border border-clay-800 bg-clay-900 p-4 hover:bg-clay-800 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
		>
			<p class="text-sm text-parchment-500">Missing Type</p>
			<p class="mt-1 text-lg font-semibold text-parchment-200">
				{formatNumber(analytics?.missing_type_count ?? 0)}
			</p>
		</a>
		<a
			href={resolve(filterUrl({ untagged: true }))}
			class="block cursor-pointer rounded-lg border border-clay-800 bg-clay-900 p-4 hover:bg-clay-800 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
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
				<h3 class="mb-3 text-base font-semibold text-balance text-parchment-200">Top Tags</h3>
				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<div class="space-y-2">
						{#each analytics.tag_frequency as item, i (i)}
							<a
								href={resolve(filterUrl({ tags: [item.label] }))}
								class="flex cursor-pointer items-center gap-3 hover:bg-clay-800 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							>
								<div
									class="h-4 w-4 shrink-0 rounded"
									style="background-color: {chartColors[i % chartColors.length]}"
									aria-hidden="true"
								></div>
								<div class="min-w-0 flex-1">
									<div class="flex justify-between text-sm">
										<span class="truncate text-parchment-200">{item.label}</span>
										<span class="shrink-0 text-parchment-500 tabular-nums"
											>{formatNumber(item.count)}</span
										>
									</div>
									<div class="mt-1 h-2 w-full overflow-hidden rounded-full bg-clay-800">
										<div
											class="h-2 w-full origin-left rounded-full motion-safe:transition-transform"
											style="transform: scaleX({(item.count / maxTagCount()) *
												100}%); background-color: {chartColors[i % chartColors.length]}"
										></div>
									</div>
								</div>
							</a>
						{/each}
					</div>
				</div>
			</section>
		{/if}

		{#if analytics?.language_distribution?.length}
			<section class="min-w-0 flex-1">
				<h3 class="mb-3 text-base font-semibold text-balance text-parchment-200">
					Language Distribution
				</h3>
				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<div class="space-y-2">
						{#each analytics.language_distribution as item, i (i)}
							<a
								href={resolve(filterUrl({ language: item.label }))}
								class="flex cursor-pointer items-center gap-3 hover:bg-clay-800 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							>
								<div
									class="h-4 w-4 shrink-0 rounded"
									style="background-color: {chartColors[i % chartColors.length]}"
									aria-hidden="true"
								></div>
								<div class="min-w-0 flex-1">
									<div class="flex justify-between text-sm">
										<span class="truncate text-parchment-200">{langLabel(item.label)}</span>
										<span class="shrink-0 text-parchment-500 tabular-nums"
											>{formatNumber(item.count)}</span
										>
									</div>
									<div class="mt-1 h-2 w-full overflow-hidden rounded-full bg-clay-800">
										<div
											class="h-2 w-full origin-left rounded-full motion-safe:transition-transform"
											style="transform: scaleX({(item.count / maxLangCount()) *
												100}%); background-color: {chartColors[i % chartColors.length]}"
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
				<h3 class="mb-3 text-base font-semibold text-balance text-parchment-200">
					Document Type Distribution
				</h3>
				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<div class="space-y-2">
						{#each analytics.document_type_distribution as item, i (i)}
							<a
								href={resolve(filterUrl({ documentType: item.label }))}
								class="flex cursor-pointer items-center gap-3 hover:bg-clay-800 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							>
								<div
									class="h-4 w-4 shrink-0 rounded"
									style="background-color: {chartColors[i % chartColors.length]}"
									aria-hidden="true"
								></div>
								<div class="min-w-0 flex-1">
									<div class="flex justify-between text-sm">
										<span class="truncate text-parchment-200">{item.label}</span>
										<span class="shrink-0 text-parchment-500 tabular-nums"
											>{formatNumber(item.count)}</span
										>
									</div>
									<div class="mt-1 h-2 w-full overflow-hidden rounded-full bg-clay-800">
										<div
											class="h-2 w-full origin-left rounded-full motion-safe:transition-transform"
											style="transform: scaleX({(item.count / maxDocTypeCount()) *
												100}%); background-color: {chartColors[i % chartColors.length]}"
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
