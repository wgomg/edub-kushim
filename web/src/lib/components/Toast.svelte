<script>
	import { toastStore } from '$lib/stores/toastStore.svelte.js';

	let toasts = $derived(toastStore.toasts);

	const variantClasses = {
		error: 'border-terracotta-600 bg-terracotta-500/10 text-terracotta-500',
		success: 'border-gold-500/30 bg-gold-500/10 text-gold-500',
		warning: 'border-amber-500/30 bg-amber-500/10 text-amber-400',
		info: 'border-parchment-500/20 bg-clay-900 text-parchment-200'
	};
</script>

<div class="fixed top-20 right-4 z-50 flex flex-col gap-2">
	{#each toasts as toast (toast.id)}
		<div
			role="alert"
			class="flex items-center gap-2 rounded-lg border px-4 py-3 text-sm shadow-lg transition-colors {variantClasses[
				toast.variant
			] || variantClasses.info}"
		>
			<span class="flex-1">{toast.message}</span>
			<button
				type="button"
				onclick={() => toastStore.dismiss(toast.id)}
				class="shrink-0 text-lg leading-none opacity-60 hover:opacity-100"
				aria-label="Dismiss">&times;</button
			>
		</div>
	{/each}
</div>
