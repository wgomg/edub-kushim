<script>
	let { name, size = 16, class: klass = '' } = $props();
</script>

<svg
	class={klass}
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
	{:else if name === 'external-link'}
		<path d="M15 3h6v6" />
		<path d="M10 14 21 3" />
		<path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6" />
	{:else if name === 'panel-right-close'}
		<rect x="3" y="3" width="18" height="18" rx="2" />
		<line x1="15" y1="3" x2="15" y2="18" />
		<path d="m10 9 3 3-3 3" />
	{:else if name === 'panel-right-open'}
		<rect x="3" y="3" width="18" height="18" rx="2" />
		<line x1="15" y1="3" x2="15" y2="18" />
		<path d="m13 9-3 3 3 3" />
	{:else if name === 'check'}
		<path d="M20 6 9 17l-5-5" />
	{:else if name === 'x'}
		<path d="M18 6 6 18" />
		<path d="m6 6 12 12" />
	{:else if name === 'pencil'}
		<path
			d="M21.174 6.812a1 1 0 0 0-3.986-3.987L3.842 16.174a2 2 0 0 0-.5.83l-1.321 4.352a.5.5 0 0 0 .623.622l4.353-1.32a2 2 0 0 0 .83-.497z"
		/>
		<path d="m15 5 4 4" />
	{:else if name === 'fold-into'}
		<path d="M17 12H7" />
		<path d="m11 18 6-6-6-6" />
		<path d="M21 5v14" />
	{:else if name === 'make-canonical'}
		<path d="M3 5v14" />
		<path d="m13 6-6 6 6 6" />
		<path d="M7 12h14" />
	{:else if name === 'split'}
		<path d="M16 3h5v5" />
		<path d="M8 3H3v5" />
		<path d="M12 22v-8.3a4 4 0 0 0-1.172-2.872L3 3" />
		<path d="m15 9 6-6" />
	{:else if name === 'keep-whole'}
		<rect width="18" height="18" x="3" y="3" rx="2" />
		<path d="m9 12 2 2 4-4" />
	{:else if name === 'apply-parts'}
		<path
			d="m16.02 12 5.48 3.13a1 1 0 0 1 0 1.74L13 21.74a2 2 0 0 1-2 0l-8.5-4.87a1 1 0 0 1 0-1.74L7.98 12"
		/>
		<path
			d="M13 13.74a2 2 0 0 1-2 0L2.5 8.87a1 1 0 0 1 0-1.74L11 2.26a2 2 0 0 1 2 0l8.5 4.87a1 1 0 0 1 0 1.74Z"
		/>
	{:else if name === 'retire'}
		<rect width="20" height="5" x="2" y="3" rx="1" />
		<path d="M4 8v11a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8" />
		<path d="M10 12h4" />
	{:else if name === 'judge-now'}
		<path d="m16 16 3-8 3 8c-.87.65-1.92 1-3 1s-2.13-.35-3-1Z" />
		<path d="m2 16 3-8 3 8c-.87.65-1.92 1-3 1s-2.13-.35-3-1Z" />
		<path d="M7 21h10" />
		<path d="M12 3v18" />
		<path d="M3 7h2c2 0 5-1 7-2 2 1 5 2 7 2h2" />
	{:else if name === 'help'}
		<circle cx="12" cy="12" r="10" />
		<path d="M9.09 9a3 3 0 0 1 5.83 1c0 2-3 3-3 3" />
		<path d="M12 17h.01" />
	{/if}
</svg>
