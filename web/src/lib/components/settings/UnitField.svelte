<script>
	let {
		id,
		label,
		value = $bindable(),
		unit = '',
		hint = '',
		type = 'text',
		name = '',
		placeholder = '',
		min = undefined,
		max = undefined,
		step = undefined,
		inputmode = undefined,
		pattern = undefined,
		spellcheck = undefined,
		mono = false,
		wide = false,
		advancedOnly = false,
		autocomplete = 'off'
	} = $props();
</script>

<div class="field {advancedOnly ? 'advanced-only' : ''} {wide ? 'sm:col-span-2' : ''}">
	<label for={id} class="text-[0.8125rem] font-medium text-parchment-200">{label}</label>
	<div class="relative">
		<input
			{id}
			name={name || id}
			{type}
			{min}
			{max}
			{step}
			{inputmode}
			{pattern}
			{placeholder}
			{spellcheck}
			{autocomplete}
			bind:value
			class="w-full rounded-lg border border-clay-800 bg-clay-950 px-2.5 py-[0.45rem] text-sm text-parchment-200 placeholder-parchment-500 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none {unit
				? 'pr-12'
				: ''} {mono ? 'font-mono text-[0.8125rem]' : ''}"
		/>
		{#if unit}
			<span
				class="pointer-events-none absolute top-1/2 right-[0.7rem] -translate-y-1/2 font-mono text-[0.6875rem] font-medium text-parchment-500"
				>{unit}</span
			>
		{/if}
	</div>
	{#if hint}
		<p class="text-xs text-parchment-500">{hint}</p>
	{/if}
</div>
