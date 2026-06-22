<script>
	import { confirmStore } from '$lib/stores/confirmStore.svelte.js';
</script>

{#if confirmStore.pending}
	{@const p = confirmStore.pending}
	<div
		role="presentation"
		class="fixed inset-0 z-50 flex items-center justify-center bg-black/60"
		onclick={() => confirmStore.resolve(false)}
		onkeydown={(e) => {
			if (e.key === 'Escape') confirmStore.resolve(false);
		}}
	>
		<div
			role="dialog"
			aria-modal="true"
			tabindex="-1"
			class="mx-4 w-full max-w-sm rounded-lg border border-clay-800 bg-clay-950 p-6 shadow-xl"
			onclick={(e) => e.stopPropagation()}
			onkeydown={(e) => {
				if (e.key === 'Escape') e.stopPropagation();
			}}
		>
			<h2 class="mb-2 text-lg font-semibold text-parchment-200">{p.title}</h2>
			<p class="text-sm text-parchment-200">{p.message}</p>
			<div class="mt-4 flex justify-end gap-2">
				<button
					onclick={() => confirmStore.resolve(false)}
					class="rounded-md px-3 py-1.5 text-xs font-medium text-parchment-400 hover:bg-clay-800"
				>
					Cancel
				</button>
				<button
					onclick={() => confirmStore.resolve(true)}
					class="rounded-md px-3 py-1.5 text-xs font-medium text-parchment-200 {p.danger
						? 'bg-terracotta-700 hover:bg-terracotta-600'
						: 'bg-gold-500 hover:bg-gold-600'}"
				>
					{p.danger ? 'Delete' : 'OK'}
				</button>
			</div>
		</div>
	</div>
{/if}
