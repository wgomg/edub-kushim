<script>
	let { count = 0, saving = false, onSave, onRevert } = $props();
</script>

<div
	class="fixed bottom-5 left-1/2 z-40 flex w-[min(28rem,calc(100vw-2rem))] -translate-x-1/2 items-center gap-3.5 rounded-xl border border-gold-800 bg-clay-900 px-3.5 py-2.5 shadow-[0_12px_32px_rgba(0,0,0,0.5)] transition-transform duration-250 ease-out motion-reduce:transition-none {count >
	0
		? 'translate-y-0'
		: 'translate-y-[150%]'}"
	role="status"
	aria-live="polite"
>
	<span class="size-2 shrink-0 rounded-full bg-gold-500"></span>
	<span class="flex-1 text-sm text-parchment-200"
		>{count === 1 ? '1 unsaved change' : `${count} unsaved changes`}</span
	>
	<button
		type="button"
		onclick={onRevert}
		disabled={saving}
		tabindex={count > 0 ? undefined : -1}
		class="rounded-lg border border-clay-800 px-3 py-1.5 text-[0.8125rem] font-medium text-parchment-400 hover:bg-clay-800 hover:text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-50"
	>
		Revert
	</button>
	<button
		type="button"
		onclick={onSave}
		disabled={saving}
		tabindex={count > 0 ? undefined : -1}
		class="rounded-lg bg-gold-500 px-4 py-1.5 text-sm font-semibold text-clay-950 hover:bg-gold-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-50"
	>
		{saving ? 'Saving…' : 'Save changes'}
	</button>
</div>
