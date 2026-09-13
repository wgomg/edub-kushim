<script>
	import { hintsForEngine } from '$lib/tools.js';
	import Note from './Note.svelte';
	import StatusBadge from './StatusBadge.svelte';

	let {
		id,
		label = 'Engine',
		value = $bindable(),
		engines = [],
		toolEntry = undefined,
		statusKnown = false
	} = $props();

	let missing = $derived(statusKnown && toolEntry !== undefined && toolEntry.available === false);

	let hints = $derived.by(() => {
		if (!missing) return [];
		const frontend = hintsForEngine(value);
		const source = Object.keys(frontend).length > 0 ? frontend : (toolEntry?.install_hints ?? {});
		return Object.entries(source);
	});

	let badgeLabel = $derived(toolEntry === undefined ? 'built-in' : 'available');
</script>

<div class="field">
	<label for={id} class="text-[0.8125rem] font-medium text-parchment-200">{label}</label>
	<div class="flex items-center gap-2.5">
		<select
			{id}
			bind:value
			class="min-w-0 flex-1 rounded-lg border border-clay-800 bg-clay-950 px-2.5 py-[0.45rem] text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
		>
			{#each engines as opt (opt.value)}
				<option value={opt.value}>{opt.label}</option>
			{:else}
				<option value="" disabled>No engines available</option>
			{/each}
		</select>
		{#if statusKnown}
			<StatusBadge
				variant={missing ? 'miss' : 'ok'}
				label={missing ? 'not installed' : badgeLabel}
			/>
		{/if}
	</div>
	{#if missing}
		<Note variant="danger" class="mt-3" title={`“${value}” is not installed.`}>
			Documents won't process until it is available. Install it, e.g.:
			{#each hints as [system, cmd] (system)}
				<pre class="mt-1 overflow-x-auto text-xs text-parchment-300">{system}: {cmd}</pre>
			{/each}
		</Note>
	{/if}
</div>
