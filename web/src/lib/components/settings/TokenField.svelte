<script>
	let { id, value = $bindable(), placeholder = 'sk-…', autocomplete = 'off' } = $props();

	let visible = $state(false);
</script>

<div class="flex gap-2">
	<input
		{id}
		type={visible ? 'text' : 'password'}
		bind:value
		{placeholder}
		{autocomplete}
		class="min-w-0 flex-1 rounded-lg border border-clay-800 bg-clay-950 px-2.5 py-[0.45rem] text-sm text-parchment-200 placeholder-parchment-500 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
	/>
	<button
		type="button"
		onclick={() => (visible = !visible)}
		class="rounded-lg border border-clay-800 px-2.5 py-[0.45rem] text-[0.8125rem] font-medium text-parchment-400 hover:bg-clay-800 hover:text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
	>
		{visible ? 'Hide' : 'Show'}
	</button>
</div>
