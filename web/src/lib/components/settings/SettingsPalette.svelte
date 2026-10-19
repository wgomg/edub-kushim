<script>
	import { onMount, tick } from 'svelte';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { fade, fly } from 'svelte/transition';
	import { SETTINGS_SECTION_BY_ID, SETTINGS_SECTIONS } from '$lib/settingsSections.js';

	let { open = $bindable(false) } = $props();

	let query = $state('');
	let activeIndex = $state(0);
	let inputEl = $state(null);
	let reducedMotion = $state(false);

	onMount(() => {
		const match = window.matchMedia('(prefers-reduced-motion: reduce)');
		reducedMotion = match.matches;
		const onChange = (event) => (reducedMotion = event.matches);
		match.addEventListener('change', onChange);
		return () => match.removeEventListener('change', onChange);
	});

	const INDEX = [
		{
			title: 'Settings overview',
			section: 'Settings',
			desc: 'Health, pipeline map and every destination',
			path: '/settings',
			keys: 'home index overview health pipeline'
		},
		...SETTINGS_SECTIONS.map((section) => ({
			title: section.title,
			section: section.group,
			desc: section.description,
			path: section.path,
			keys: section.keywords
		})),
		{
			title: 'Content analyzer',
			section: 'Enrichment',
			desc: 'Model connection, tags per document, prompt and fallbacks',
			path: SETTINGS_SECTION_BY_ID.enrichment.path,
			keys: 'llm analysis prompt temperature timeout fallback tags'
		},
		{
			title: 'Tag matcher',
			section: 'Enrichment',
			desc: 'Hugot backend, chunk size and embeddings',
			path: SETTINGS_SECTION_BY_ID.enrichment.path,
			keys: 'hugot embeddings ort model chunk'
		},
		{
			title: 'Scheduled maintenance',
			section: 'Enrichment',
			desc: 'Reconciliation, measurement and coverage audit',
			path: SETTINGS_SECTION_BY_ID.enrichment.path,
			keys: 'schedule reconciliation measurement coverage audit grace interval'
		},
		{
			title: 'OCR engine',
			section: 'Processing',
			desc: 'Engine, languages, timeout and workers',
			path: SETTINGS_SECTION_BY_ID.processing.path,
			keys: 'ocr tesseract languages timeout workers dpi'
		},
		{
			title: 'Thumbnails',
			section: 'Processing',
			desc: 'Engine, DPI, width and quality',
			path: SETTINGS_SECTION_BY_ID.processing.path,
			keys: 'thumbnail dpi width quality'
		},
		{
			title: 'Database',
			section: 'Storage',
			desc: 'PostgreSQL host, port, user and SSL mode',
			path: SETTINGS_SECTION_BY_ID.storage.path,
			keys: 'postgres database host port ssl user'
		},
		{
			title: 'Backups',
			section: 'Safety',
			desc: 'Destination and schedules',
			path: SETTINGS_SECTION_BY_ID.safety.path,
			keys: 'backup daily weekly destination'
		},
		{
			title: 'Users',
			section: 'Access',
			desc: 'Accounts and roles',
			path: SETTINGS_SECTION_BY_ID.access.path,
			keys: 'users accounts roles'
		},
		{
			title: 'Logging',
			section: 'System',
			desc: 'Level, size, backups and retention',
			path: SETTINGS_SECTION_BY_ID.system.path,
			keys: 'logs logging rotate level'
		}
	];

	let results = $derived.by(() => {
		const needle = query.trim().toLowerCase();
		if (!needle) return INDEX.slice(0, 12);
		return INDEX.filter((item) =>
			`${item.title} ${item.section} ${item.desc} ${item.keys}`.toLowerCase().includes(needle)
		).slice(0, 16);
	});

	$effect(() => {
		if (open) {
			query = '';
			activeIndex = 0;
			tick().then(() => inputEl?.focus());
		}
	});

	function choose(index) {
		const item = results[index];
		if (!item) return;
		open = false;
		goto(resolve(item.path));
	}

	function handleInputKeydown(event) {
		if (event.key === 'ArrowDown') {
			event.preventDefault();
			activeIndex = Math.min(activeIndex + 1, results.length - 1);
		} else if (event.key === 'ArrowUp') {
			event.preventDefault();
			activeIndex = Math.max(activeIndex - 1, 0);
		} else if (event.key === 'Enter') {
			event.preventDefault();
			choose(activeIndex);
		}
	}

	function handleWindowKeydown(event) {
		const typing = /^(INPUT|TEXTAREA|SELECT)$/.test(event.target?.tagName ?? '');
		if (event.key === 'Escape' && open) {
			open = false;
			return;
		}
		if (
			(event.key === '/' && !typing && !open) ||
			((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k')
		) {
			event.preventDefault();
			open = true;
		}
	}
</script>

<svelte:window onkeydown={handleWindowKeydown} />

{#if open}
	<div
		class="fixed inset-0 z-[110] bg-[rgba(10,8,7,0.62)]"
		transition:fade={{ duration: reducedMotion ? 0 : 120 }}
		onclick={() => (open = false)}
		role="presentation"
	></div>
	<div
		class="fixed inset-0 z-[111] flex items-start justify-center px-4 pt-[12vh]"
		onclick={(event) => {
			if (event.target === event.currentTarget) open = false;
		}}
		role="presentation"
	>
		<div
			class="w-full max-w-2xl overflow-hidden rounded-xl border border-clay-700 bg-clay-900 shadow-[0_24px_70px_rgba(0,0,0,0.5)]"
			transition:fly={{ y: reducedMotion ? 0 : -10, duration: reducedMotion ? 0 : 140 }}
			role="dialog"
			aria-modal="true"
			aria-label="Search settings"
		>
			<div class="flex items-center gap-3 border-b border-clay-800 px-4 py-3">
				<span class="text-parchment-500" aria-hidden="true">⌕</span>
				<input
					bind:this={inputEl}
					bind:value={query}
					oninput={() => (activeIndex = 0)}
					onkeydown={handleInputKeydown}
					type="text"
					placeholder="Search settings, providers, models…"
					autocomplete="off"
					aria-label="Search settings"
					class="flex-1 rounded-sm border-0 bg-transparent p-0 text-[0.9375rem] text-parchment-200 placeholder-parchment-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
				/>
				<kbd
					class="rounded-[0.25rem] border border-b-2 border-clay-700 px-1.5 py-px font-mono text-[0.625rem] font-semibold text-parchment-500"
					>esc</kbd
				>
			</div>

			<div class="max-h-[22rem] overflow-y-auto overscroll-contain p-1.5">
				{#if results.length === 0}
					<div class="px-4 py-8 text-center text-sm text-parchment-500">
						No settings match “{query.trim()}”.
					</div>
				{:else}
					{#each results as item, index (item.title)}
						<a
							href={resolve(item.path)}
							onclick={() => (open = false)}
							onmouseenter={() => (activeIndex = index)}
							class="flex w-full items-center gap-3 rounded-lg px-3 py-2 text-left focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none {index ===
							activeIndex
								? 'bg-clay-800 text-parchment-200'
								: 'text-parchment-300 hover:bg-clay-800'}"
						>
							<span class="min-w-0 flex-1">
								<span class="block text-[0.8125rem] font-medium">{item.title}</span>
								<span class="block truncate text-[0.6875rem] text-parchment-500">
									{item.section} · {item.desc}
								</span>
							</span>
							<span class="text-clay-600" aria-hidden="true">→</span>
						</a>
					{/each}
				{/if}
			</div>

			<div class="flex gap-4 border-t border-clay-800 px-4 py-2 text-[0.625rem] text-parchment-500">
				<span>↑↓ navigate</span>
				<span>enter open</span>
				<span>/ to search</span>
			</div>
		</div>
	</div>
{/if}
