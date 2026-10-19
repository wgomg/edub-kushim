<script>
	import AccessSection from '$lib/components/settings/AccessSection.svelte';
	import { settingsStore } from '$lib/stores/settingsStore.svelte.js';
</script>

<div class="mb-5 max-w-3xl">
	<p class="font-mono text-[0.625rem] font-semibold tracking-[0.14em] text-gold-500 uppercase">
		Operations
	</p>
	<h1 class="mt-1 text-[1.625rem] font-semibold tracking-[-0.01em] text-parchment-200">Access</h1>
	<p class="mt-1.5 text-parchment-500">
		Who can sign in and what they can do. Account changes apply immediately, unlike the rest of this
		page.
	</p>
</div>

<h2 class="sr-only">Access Settings</h2>

<AccessSection cfg={settingsStore.cfg} showHead={false} />
