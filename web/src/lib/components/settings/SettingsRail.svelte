<script>
	let {
		active = 'overview',
		dirtyGroups = new Set(),
		counts = {},
		tools = { known: false, available: 0, total: 0 }
	} = $props();

	const OVERVIEW = { id: 'overview', label: 'Overview', sub: 'health', node: '' };
	const STAGES = [
		{ id: 'intake', label: 'Intake', sub: 'uploads & inbox', node: '1' },
		{ id: 'processing', label: 'Processing', sub: 'OCR & rendering', node: '2' },
		{ id: 'enrichment', label: 'Enrichment', sub: 'LLM & tags', node: '3' },
		{ id: 'models', label: 'Models', sub: 'providers & catalog', node: '' },
		{ id: 'storage', label: 'Storage', sub: 'files & database', node: '4' }
	];
	const OPERATIONS = [
		{ id: 'safety', label: 'Safety', sub: 'backups & mirror', node: '' },
		{ id: 'access', label: 'Access', sub: 'sign-in & users', node: '' },
		{ id: 'system', label: 'System', sub: 'logging', node: '' }
	];
</script>

{#snippet railItem(group)}
	<a
		href={'#' + group.id}
		data-g={group.id}
		aria-current={active === group.id ? 'true' : undefined}
		class="flex flex-none items-center gap-2 rounded-full border border-clay-800 px-3 py-1 text-sm whitespace-nowrap transition-colors focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none lg:relative lg:flex-col lg:items-stretch lg:gap-0 lg:rounded-lg lg:border-0 lg:py-1.5 lg:pr-2.5 lg:pl-9 {active ===
		group.id
			? 'bg-clay-900 text-parchment-200'
			: 'text-parchment-400 hover:bg-clay-900 hover:text-parchment-200'}"
	>
		{#if group.node}
			<span
				class="hidden lg:absolute lg:top-[0.55rem] lg:left-2.5 lg:z-[1] lg:flex lg:size-5 lg:items-center lg:justify-center lg:rounded-full lg:border lg:bg-clay-950 lg:font-mono lg:text-[0.6875rem] lg:font-semibold {active ===
				group.id
					? 'lg:border-gold-500 lg:text-gold-500'
					: 'lg:border-clay-700 lg:text-parchment-500'}">{group.node}</span
			>
		{/if}
		<span class="flex items-center gap-1.5 lg:gap-0">
			{group.label}
			<span
				class="size-1.5 rounded-full bg-gold-500 lg:ml-auto {dirtyGroups.has(group.id)
					? 'opacity-100'
					: 'opacity-0'}"
			></span>
			{#if counts[group.id]}
				<span
					class="rounded-full border border-gold-800 px-1.5 font-mono text-[0.6875rem] font-semibold text-gold-500"
					>{counts[group.id]}</span
				>
			{/if}
		</span>
		<span class="hidden text-[0.6875rem] text-parchment-500 lg:block">{group.sub}</span>
	</a>
{/snippet}

<nav
	aria-label="Settings sections"
	class="mb-5 flex items-center gap-1.5 overflow-x-auto border-b border-clay-800 pb-2 lg:sticky lg:top-6 lg:mb-0 lg:flex-col lg:items-stretch lg:gap-0.5 lg:overflow-visible lg:border-0 lg:pb-0"
>
	{@render railItem(OVERVIEW)}
	<p
		class="mt-4 mb-1.5 ml-2.5 hidden font-mono text-[0.625rem] font-semibold tracking-[0.14em] text-parchment-500 uppercase lg:block"
	>
		The pipeline
	</p>
	<div
		class="contents lg:relative lg:block lg:before:absolute lg:before:top-5 lg:before:bottom-5 lg:before:left-5 lg:before:w-px lg:before:bg-clay-700 lg:before:content-['']"
	>
		{#each STAGES as group (group.id)}
			{@render railItem(group)}
		{/each}
	</div>
	<p
		class="mt-4 mb-1.5 ml-2.5 hidden font-mono text-[0.625rem] font-semibold tracking-[0.14em] text-parchment-500 uppercase lg:block"
	>
		Operations
	</p>
	{#each OPERATIONS as group (group.id)}
		{@render railItem(group)}
	{/each}
	<div class="mt-5 hidden flex-col gap-1 border-t border-dashed border-clay-700 pt-3.5 lg:flex">
		<a
			href="#overview"
			class="flex items-center gap-2 font-mono text-[0.6875rem] text-parchment-500 hover:text-parchment-300 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
		>
			<span
				class="size-[0.45rem] shrink-0 rounded-full {tools.known && tools.available === tools.total
					? 'bg-lapis-300'
					: 'bg-gold-500'}"
			></span>
			{tools.known ? `tools ${tools.available}/${tools.total}` : 'tools unknown'}
		</a>
	</div>
</nav>
