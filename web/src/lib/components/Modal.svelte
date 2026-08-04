<script>
	let { open, title, onClose, children } = $props();
</script>

{#if open}
	<div
		role="presentation"
		class="fixed inset-0 z-50 flex items-center justify-center bg-black/60"
		onclick={onClose}
	>
		<div
			class="mx-4 w-full max-w-md rounded-lg border border-clay-800 bg-clay-950 p-6 shadow-xl"
			onclick={(e) => e.stopPropagation()}
			onkeydown={(e) => {
				if (e.key === 'Escape') onClose();
			}}
			role="dialog"
			aria-modal="true"
			aria-label={title}
			tabindex="-1"
			style="overscroll-behavior: contain"
		>
			<div class="mb-4 flex items-center justify-between">
				<h2 class="text-lg font-semibold text-parchment-200">{title}</h2>
				<button
					onclick={onClose}
					aria-label="Close dialog"
					class="rounded-md p-1 text-parchment-500 hover:bg-clay-800 hover:text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					>&times;</button
				>
			</div>
			{@render children()}
		</div>
	</div>
{/if}
