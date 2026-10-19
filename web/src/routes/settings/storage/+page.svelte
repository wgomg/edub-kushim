<script>
	import StorageSection from '$lib/components/settings/StorageSection.svelte';
	import { settingsStore } from '$lib/stores/settingsStore.svelte.js';
</script>

<div class="mb-5 max-w-3xl">
	<p class="font-mono text-[0.625rem] font-semibold tracking-[0.14em] text-gold-500 uppercase">
		Pipeline · Stage 4
	</p>
	<h1 class="mt-1 text-[1.625rem] font-semibold tracking-[-0.01em] text-parchment-200">Storage</h1>
	<p class="mt-1.5 text-parchment-500">
		Where originals and processed files live, how long trash is kept, and which database backs the
		library.
	</p>
</div>

<h2 class="sr-only">Storage Settings</h2>

<StorageSection cfg={settingsStore.cfg} showHead={false} />
