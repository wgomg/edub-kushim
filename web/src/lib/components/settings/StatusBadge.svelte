<script>
	let { variant = 'ok', label = '' } = $props();

	const VARIANT_CLASSES = {
		ok: 'border-lapis-700 bg-lapis-500/10 text-lapis-300',
		miss: 'border-terracotta-700 bg-terracotta-500/10 text-terracotta-300'
	};
</script>

<span
	class="inline-flex shrink-0 items-center rounded-full border px-2.5 py-0.5 font-mono text-[0.6875rem] font-semibold {VARIANT_CLASSES[
		variant
	] ?? VARIANT_CLASSES.ok}"
>
	{label}
</span>
