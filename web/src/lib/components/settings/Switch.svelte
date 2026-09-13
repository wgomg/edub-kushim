<script>
	let {
		checked = $bindable(false),
		label = '',
		description = '',
		ariaLabel = '',
		disabled = false,
		id = undefined
	} = $props();
</script>

<label class="relative flex cursor-pointer items-start gap-3">
	<input
		type="checkbox"
		{id}
		{disabled}
		class="peer sr-only"
		bind:checked
		aria-label={ariaLabel || label}
	/>
	<div
		class="relative mt-0.5 h-5.5 w-10 shrink-0 rounded-full border border-clay-700 bg-clay-800 transition-colors peer-checked:border-gold-600 peer-checked:bg-gold-600 peer-focus-visible:ring-2 peer-focus-visible:ring-gold-500 peer-focus-visible:ring-offset-2 peer-focus-visible:ring-offset-clay-900 peer-disabled:opacity-50 after:absolute after:top-0.5 after:left-0.5 after:size-4 after:rounded-full after:bg-parchment-400 after:transition-transform peer-checked:after:translate-x-4.5 peer-checked:after:bg-white"
	></div>
	<span class="min-w-0">
		<strong class="block text-sm font-medium text-parchment-200">{label}</strong>
		{#if description}
			<em class="block text-xs font-normal text-parchment-500 not-italic">{description}</em>
		{/if}
	</span>
</label>
