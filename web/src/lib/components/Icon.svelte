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
	{:else if name === 'download'}
		<path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
		<polyline points="7 10 12 15 17 10" />
		<line x1="12" y1="15" x2="12" y2="3" />
	{:else if name === 'rotate-cw'}
		<path d="M23 4v6h-6" />
		<path d="M20.49 15a9 9 0 1 1 2.12-9.36L23 10" />
	{:else if name === 'trash-2'}
		<polyline points="3 6 5 6 21 6" />
		<path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
		<line x1="10" y1="11" x2="10" y2="17" />
		<line x1="14" y1="11" x2="14" y2="17" />
	{:else if name === 'panel-right-close'}
		<rect x="3" y="3" width="18" height="18" rx="2" />
		<line x1="15" y1="3" x2="15" y2="18" />
		<path d="m10 9 3 3-3 3" />
	{:else if name === 'panel-right-open'}
		<rect x="3" y="3" width="18" height="18" rx="2" />
		<line x1="15" y1="3" x2="15" y2="18" />
		<path d="m13 9-3 3 3 3" />
	{/if}
</svg>
