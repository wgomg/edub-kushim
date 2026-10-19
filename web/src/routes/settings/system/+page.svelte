<script>
	import SystemSection from '$lib/components/settings/SystemSection.svelte';
	import { settingsStore } from '$lib/stores/settingsStore.svelte.js';
</script>

<div class="mb-5 max-w-3xl">
	<p class="font-mono text-[0.625rem] font-semibold tracking-[0.14em] text-gold-500 uppercase">
		Operations
	</p>
	<h1 class="mt-1 text-[1.625rem] font-semibold tracking-[-0.01em] text-parchment-200">System</h1>
	<p class="mt-1.5 text-parchment-500">
		Logging behavior and the version information for this installation.
	</p>
</div>

<h2 class="sr-only">System Settings</h2>

<SystemSection cfg={settingsStore.cfg} showHead={false} />
