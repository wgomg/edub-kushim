<script>
	import { onMount } from 'svelte';
	import Icon from '#lib/components/Icon.svelte';

	let { open = $bindable(false), label, badge = 0, onopen = null, children } = $props();

	let root = $state(null);
	let trigger = $state(null);

	const triggerClass =
		'inline-flex items-center gap-1.5 rounded-md border border-clay-700 px-2.5 py-1 text-xs font-medium text-parchment-300 hover:border-gold-500 hover:text-gold-400 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none';

	function setOpen(next) {
		if (open === next) return;
		open = next;
		if (next && onopen) onopen();
	}

	function close() {
		if (!open) return;
		setOpen(false);
		if (trigger) trigger.focus();
	}

	onMount(() => {
		function onDocumentClick(event) {
			if (open && root && !root.contains(event.target)) setOpen(false);
		}
		function onKeydown(event) {
			if (event.key === 'Escape') close();
		}
		document.addEventListener('click', onDocumentClick);
		document.addEventListener('keydown', onKeydown);
		return () => {
			document.removeEventListener('click', onDocumentClick);
			document.removeEventListener('keydown', onKeydown);
		};
	});
</script>

<div class="relative" bind:this={root}>
	<button
		type="button"
		bind:this={trigger}
		class={triggerClass}
		aria-haspopup="true"
		aria-expanded={open}
		onclick={() => setOpen(!open)}
	>
		{label}
		{#if badge > 0}
			<span
				class="inline-flex h-4.5 min-w-4.5 items-center justify-center rounded-full bg-gold-500 px-1 font-mono text-[0.625rem] font-bold text-clay-950 tabular-nums"
				>{badge}</span
			>
		{/if}
		<Icon name="chevron-down" size={12} class="text-parchment-600" />
	</button>
	{#if open}
		<div
			class="absolute top-full left-0 z-30 mt-1.5 max-h-[30rem] w-[23rem] max-w-[calc(100vw-3rem)] overflow-y-auto rounded-lg border border-clay-700 bg-clay-900 p-2 shadow-xl"
			role="group"
			aria-label={label}
		>
			{@render children()}
		</div>
	{/if}
</div>
