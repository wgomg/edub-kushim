<script>
	import ModelsSection from '#lib/components/settings/ModelsSection.svelte';
	import { settingsStore } from '#lib/stores/settingsStore.svelte.js';

	let usedBy = $derived.by(() => {
		const map = {};
		const add = (label, llm) => {
			if (!llm?.provider) return;
			const key = llm.model ? `${llm.provider}/${llm.model}` : `${llm.provider}/*`;
			(map[key] = map[key] ?? []).push(label);
		};
		const cfg = settingsStore.cfg;
		add('Analyzer', cfg.enricher.contentanalyzer.llm);
		add('Adjudicator', cfg.enricher.tag_adjudicator.llm);
		add('Quality judge', cfg.enricher.tag_quality.llm);
		(cfg.enricher.contentanalyzer.fallbacks ?? []).forEach((fallback, index) => {
			add(`Fallback ${index + 1}`, fallback.llm);
		});
		return map;
	});
</script>

<div class="mb-5 max-w-3xl">
	<p class="font-mono text-[0.625rem] font-semibold tracking-[0.14em] text-gold-500 uppercase">
		Catalog
	</p>
	<h1 class="mt-1 text-[1.625rem] font-semibold tracking-[-0.01em] text-balance text-parchment-200">
		AI providers &amp; models
	</h1>
	<p class="mt-1.5 text-parchment-500">
		The single source of truth for every model the pipeline can use. Edits stay staged until you
		apply them with the rest of your changes.
	</p>
</div>

<ModelsSection
	catalog={settingsStore.catalog}
	loaded={settingsStore.catalogLoaded}
	error={settingsStore.catalogError}
	{usedBy}
	pendingCount={settingsStore.catalogOps.length}
	busy={settingsStore.busy}
	onAddProvider={settingsStore.addProvider}
	onUpdateProvider={settingsStore.updateProvider}
	onRemoveProvider={settingsStore.removeProvider}
	onAddModel={settingsStore.addModel}
	onUpdateModel={settingsStore.updateModel}
	onRemoveModel={settingsStore.removeModel}
	onReset={settingsStore.resetCatalog}
/>
