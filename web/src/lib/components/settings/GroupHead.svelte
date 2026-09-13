<script>
	let { eyebrow, variant = 'stage', title, description = '' } = $props();
</script>

<header class="mb-4">
	<span
		class="font-mono text-[0.6875rem] font-semibold tracking-[0.14em] uppercase {variant === 'op'
			? 'text-parchment-500'
			: 'text-gold-500'}">{eyebrow}</span
	>
	<h2 class="mt-1 text-xl font-semibold tracking-[-0.01em] text-parchment-200">{title}</h2>
	{#if description}
		<p class="mt-1 text-parchment-500">{description}</p>
	{/if}
</header>
