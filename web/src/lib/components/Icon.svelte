<script>
	let { name, size = 16 } = $props();
</script>

<svg
	xmlns="http://www.w3.org/2000/svg"
	width={size}
	height={size}
	viewBox="0 0 24 24"
	aria-hidden="true"
	stroke="currentColor"
	stroke-width="2"
	fill="none"
	stroke-linecap="round"
	stroke-linejoin="round"
>
	{#if name === 'minus'}
		<line x1="5" x2="19" y1="12" y2="12" />
	{:else if name === 'plus'}
		<path d="M5 12h14" />
		<path d="M12 5v14" />
	{:else if name === 'chevron-left'}
		<path d="m15 18-6-6 6-6" />
	{:else if name === 'chevron-right'}
		<path d="m9 18 6-6-6-6" />
	{:else if name === 'search'}
		<circle cx="11" cy="11" r="8" />
		<path d="m21 21-4.3-4.3" />
	{/if}
</svg>
