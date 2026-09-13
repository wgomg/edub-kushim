<script>
	let { variant = 'info', title = '', children, class: klass = '' } = $props();

	const VARIANT_CLASSES = {
		info: 'border-l-lapis-700 bg-lapis-500/10',
		warn: 'border-l-gold-500 bg-gold-500/10',
		danger: 'border-l-terracotta-500 bg-terracotta-500/10'
	};
</script>

<div
	class="rounded-lg border-l-[3px] px-3.5 py-2.5 text-[0.8125rem] text-parchment-300 {VARIANT_CLASSES[
		variant
	] ?? VARIANT_CLASSES.info} {klass}"
>
	{#if title}
		<strong class="me-1 font-medium text-parchment-200">{title}</strong>
	{/if}
	{@render children()}
</div>
