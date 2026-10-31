<script>
	import { onMount } from 'svelte';
	import '@scalar/api-reference/style.css';
	import { api } from '#lib/api.js';

	let container = $state();
	let error = $state('');

	onMount(() => {
		let disposed = false;
		let reference;

		(async () => {
			try {
				const [content, { createApiReference }] = await Promise.all([
					api.openapiSpec(),
					import('@scalar/api-reference')
				]);
				if (disposed) return;
				if (!content) {
					error = 'Could not load the API specification.';
					return;
				}

				reference = createApiReference(container, {
					content,
					darkMode: true,
					hideDarkModeToggle: true,
					withDefaultFonts: false,
					showDeveloperTools: 'never',
					agent: { disabled: true },
					urlRouting: false,
					modelsSectionLabel: 'Schemas',
					metaData: { title: 'edub API reference' }
				});
			} catch (err) {
				console.error('API reference:', err);
				error = 'Could not render the API reference.';
			}
		})();

		return () => {
			disposed = true;
			reference?.destroy();
		};
	});
</script>

<svelte:head><title>API Docs · edub-kushim</title></svelte:head>

<div class="h-full">
	{#if error}
		<p class="text-sm text-terracotta-500">{error}</p>
	{/if}
	<div bind:this={container} class="h-full"></div>
</div>

<style>
	:global(body.dark-mode) {
		--scalar-background-1: var(--color-clay-950);
		--scalar-background-2: var(--color-clay-900);
		--scalar-background-3: var(--color-clay-800);
		--scalar-background-accent: color-mix(in srgb, var(--color-gold-500) 14%, transparent);
		--scalar-color-1: var(--color-parchment-100);
		--scalar-color-2: var(--color-parchment-400);
		--scalar-color-3: var(--color-parchment-500);
		--scalar-color-accent: var(--color-gold-500);
		--scalar-color-red: var(--color-terracotta-400);
		--scalar-border-color: var(--color-clay-800);
		--scalar-button-1: var(--color-gold-500);
		--scalar-button-1-hover: var(--color-gold-400);
		--scalar-button-1-color: var(--color-clay-950);
		--scalar-sidebar-background-1: var(--color-clay-900);
		--scalar-sidebar-item-hover-background: var(--color-clay-800);
		--scalar-sidebar-item-active-background: var(--color-clay-800);
		--scalar-sidebar-color-active: var(--color-gold-400);
	}
</style>
