<script>
	import { resolve } from '$app/paths';
	import EnrichmentSection from '#lib/components/settings/EnrichmentSection.svelte';
	import { settingsStore } from '#lib/stores/settingsStore.svelte.js';
</script>

<div class="mb-5 max-w-3xl">
	<p class="font-mono text-[0.625rem] font-semibold tracking-[0.14em] text-gold-500 uppercase">
		Pipeline · Stage 3
	</p>
	<h1 class="mt-1 text-[1.625rem] font-semibold tracking-[-0.01em] text-parchment-200">
		Enrichment
	</h1>
	<p class="mt-1.5 text-parchment-500">
		What the machine understands — analysis, summaries and tags. Advanced request tuning, prompts
		and fallbacks stay out of the way until you ask for them.
	</p>
	<p class="mt-2 text-xs text-parchment-500">
		Model: <span class="font-mono text-parchment-300"
			>{settingsStore.cfg.enricher.contentanalyzer.llm.provider || 'none'} / {settingsStore.cfg
				.enricher.contentanalyzer.llm.model || 'none'}</span
		>
		·
		<a
			href={resolve('settings/models')}
			class="text-lapis-300 hover:text-lapis-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
			>Manage Catalog <span aria-hidden="true">→</span></a
		>
	</p>
</div>

<EnrichmentSection
	cfg={settingsStore.cfg}
	llmModels={settingsStore.llmModels}
	toolStatus={settingsStore.toolStatus}
	statusKnown={settingsStore.statusLoaded}
	showHead={false}
/>
