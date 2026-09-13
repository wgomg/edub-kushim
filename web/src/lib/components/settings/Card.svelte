<script>
	let { title, description = '', name = undefined, children, actions } = $props();
</script>

<section class="card rounded-xl border border-clay-800 bg-clay-900 p-5" data-name={name}>
	<div class="mb-4 flex items-start justify-between gap-4">
		<div>
			<h3 class="text-base font-semibold text-parchment-200">{title}</h3>
			{#if description}
				<p class="mt-0.5 text-[0.8125rem] text-parchment-500">{description}</p>
			{/if}
		</div>
		{#if actions}
			<div class="shrink-0">{@render actions()}</div>
		{/if}
	</div>
	{@render children()}
</section>
