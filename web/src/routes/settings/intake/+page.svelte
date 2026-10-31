<script>
	import IntakeSection from '#lib/components/settings/IntakeSection.svelte';
	import { settingsStore } from '#lib/stores/settingsStore.svelte.js';
</script>

<div class="mb-5 max-w-3xl">
	<p class="font-mono text-[0.625rem] font-semibold tracking-[0.14em] text-gold-500 uppercase">
		Pipeline · Stage 1
	</p>
	<h1 class="mt-1 text-[1.625rem] font-semibold tracking-[-0.01em] text-parchment-200">Intake</h1>
	<p class="mt-1.5 text-parchment-500">
		How documents get in — the server, the watched inbox, and what the inbox accepts.
	</p>
</div>

<h2 class="sr-only">Intake Settings</h2>

<IntakeSection
	cfg={settingsStore.cfg}
	mimeTypeOptions={settingsStore.mimeTypeOptions}
	showHead={false}
/>
