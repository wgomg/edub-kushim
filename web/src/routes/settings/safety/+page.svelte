<script>
	import SafetySection from '$lib/components/settings/SafetySection.svelte';
	import { settingsStore } from '$lib/stores/settingsStore.svelte.js';
</script>

<div class="mb-5 max-w-3xl">
	<p class="font-mono text-[0.625rem] font-semibold tracking-[0.14em] text-gold-500 uppercase">
		Operations
	</p>
	<h1 class="mt-1 text-[1.625rem] font-semibold tracking-[-0.01em] text-parchment-200">Safety</h1>
	<p class="mt-1.5 text-parchment-500">
		Backups and the optional mirror. These run on their own schedules, independent of the document
		pipeline.
	</p>
</div>

<h2 class="sr-only">Safety Settings</h2>

<SafetySection cfg={settingsStore.cfg} toolStatus={settingsStore.toolStatus} showHead={false} />
