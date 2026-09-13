<script>
	let { value = $bindable('') } = $props();

	let inputEl = $state(null);

	function handleKeydown(e) {
		if (e.key === 'Escape') {
			value = '';
			inputEl?.blur();
		}
	}

	$effect(() => {
		if (typeof document === 'undefined') return;
		function onDocumentKeydown(e) {
			if (e.key !== '/' || e.ctrlKey || e.metaKey || e.altKey) return;
			const tag = document.activeElement?.tagName ?? '';
			if (/^(INPUT|TEXTAREA|SELECT)$/.test(tag)) return;
			e.preventDefault();
			inputEl?.focus();
		}
		document.addEventListener('keydown', onDocumentKeydown);
		return () => document.removeEventListener('keydown', onDocumentKeydown);
	});
</script>

<div class="relative max-w-96">
	<input
		id="settings-search"
		type="text"
		bind:this={inputEl}
		bind:value
		onkeydown={handleKeydown}
		placeholder="Search settings — engines, paths, limits…"
		autocomplete="off"
		aria-label="Search settings"
		class="w-full rounded-[0.625rem] border border-clay-800 bg-clay-900 py-2 pr-11 pl-3 text-sm text-parchment-200 placeholder-parchment-500 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
	/>
	<kbd
		class="pointer-events-none absolute top-1/2 right-2.5 -translate-y-1/2 rounded-[0.3rem] border border-b-2 border-clay-700 px-1.5 py-px font-mono text-[0.6875rem] font-semibold text-parchment-500"
		>/</kbd
	>
</div>
