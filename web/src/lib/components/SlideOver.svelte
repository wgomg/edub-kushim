<script>
	let { open = false, title = '', onClose, children } = $props();

	let panel = $state(null);

	$effect(() => {
		if (open) panel?.focus();
	});
</script>

{#if open}
	<div
		role="presentation"
		class="fixed inset-0 z-50 flex justify-end bg-black/60"
		onclick={onClose}
	>
		<div
			bind:this={panel}
			role="dialog"
			aria-modal="true"
			aria-label={title}
			tabindex="-1"
			class="flex h-full w-full max-w-lg flex-col border-s border-clay-800 bg-clay-950 shadow-xl focus:outline-none"
			onclick={(e) => e.stopPropagation()}
			onkeydown={(e) => {
				if (e.key === 'Escape') onClose?.();
			}}
			style="overscroll-behavior: contain"
		>
			<header class="flex items-center justify-between border-b border-clay-800 px-5 py-3.5">
				<h2 class="text-base font-semibold text-parchment-200">{title}</h2>
				<button
					onclick={onClose}
					aria-label="Close help"
					class="rounded-md p-1 text-parchment-500 hover:bg-clay-800 hover:text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					>&times;</button
				>
			</header>
			<div class="flex-1 overflow-y-auto px-5 py-4">{@render children()}</div>
		</div>
	</div>
{/if}
