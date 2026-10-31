<script>
	import ProcessingSection from '#lib/components/settings/ProcessingSection.svelte';
	import { settingsStore } from '#lib/stores/settingsStore.svelte.js';
</script>

<div class="mb-5 max-w-3xl">
	<p class="font-mono text-[0.625rem] font-semibold tracking-[0.14em] text-gold-500 uppercase">
		Pipeline · Stage 2
	</p>
	<h1 class="mt-1 text-[1.625rem] font-semibold tracking-[-0.01em] text-parchment-200">
		Processing
	</h1>
	<p class="mt-1.5 text-parchment-500">
		Turn files into text and images. Only the controls most installations touch are shown; switch to
		Advanced for engine internals.
	</p>
</div>

<h2 class="sr-only">Processing Settings</h2>

<ProcessingSection
	cfg={settingsStore.cfg}
	toolStatus={settingsStore.toolStatus}
	statusKnown={settingsStore.statusLoaded}
	showHead={false}
/>
