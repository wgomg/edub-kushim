<script>
	import { onMount, onDestroy } from 'svelte';
	import { page } from '$app/stores';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { api } from '$lib/api';

	const tabs = [
		{ id: 'kushim', label: 'Kushim' },
		{ id: 'edub', label: 'Edub' },
		{ id: 'hugot', label: 'Hugot' },
		{ id: 'queue', label: 'Queue' }
	];

	let activeTab = $state('kushim');
	let lines = $state(500);
	let autoRefresh = $state(false);
	let logs = $state([]);
	let loading = $state(false);
	let error = $state('');
	let atBottom = $state(true);
	let scrollContainer = $state(null);
	let expandedLines = $state(new Set());

	let refreshInterval = null;
	let controller = $state(null);

	function syncFromURL() {
		const params = $page.url.searchParams;
		const tab = params.get('tab');
		if (tab && tabs.some((t) => t.id === tab)) activeTab = tab;
		const linesParam = params.get('lines');
		if (linesParam) {
			const n = parseInt(linesParam);
			if (n >= 100 && n <= 5000) lines = n;
		}
	}

	syncFromURL();

	$effect(() => {
		const search = $page.url.search;
		// transient helper for replaceState; SvelteURLSearchParams not needed here
		// eslint-disable-next-line svelte/prefer-svelte-reactivity
		const params = new URLSearchParams(search);
		params.set('tab', activeTab);
		params.set('lines', String(lines));
		const qs = params.toString();
		const current = $page.url.search;
		if ('?' + qs !== current && current !== qs) {
			goto(resolve(`/logs?${qs}`), { replaceState: true, noScroll: true });
		}
	});

	async function fetchLogs() {
		if (controller) controller.abort();
		const myController = new AbortController();
		controller = myController;
		loading = true;
		error = '';
		const data = await api.logs.get(activeTab, lines, myController.signal);
		if (controller !== myController) return;
		if (data && data.lines) {
			logs = data.lines;
		} else {
			logs = [];
			error = 'No log file found — the process may not be running';
		}
		loading = false;
		controller = null;
	}

	function handleTabClick(tabId) {
		if (tabId === activeTab) return;
		activeTab = tabId;
		expandedLines = new Set();
		atBottom = true;
		fetchLogs();
	}

	function handleLinesChange(e) {
		const val = parseInt(e.target.value);
		if (!isNaN(val)) {
			lines = Math.max(100, Math.min(5000, val));
			fetchLogs();
		}
	}

	function toggleAutoRefresh() {
		autoRefresh = !autoRefresh;
	}

	$effect(() => {
		if (refreshInterval) clearInterval(refreshInterval);
		refreshInterval = null;
		if (autoRefresh) {
			refreshInterval = setInterval(() => {
				fetchLogs();
			}, 10000);
		}
		return () => {
			if (refreshInterval) clearInterval(refreshInterval);
		};
	});

	function handleScroll() {
		if (!scrollContainer) return;
		const el = scrollContainer;
		const threshold = 50;
		atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < threshold;
	}

	function scrollToBottom() {
		if (scrollContainer) {
			scrollContainer.scrollTop = scrollContainer.scrollHeight;
			atBottom = true;
		}
	}

	$effect(() => {
		if (atBottom && logs.length > 0 && scrollContainer) {
			scrollContainer.scrollTop = scrollContainer.scrollHeight;
		}
	});

	function toggleExpand(idx) {
		// set is rebuilt on change, not mutated in place
		// eslint-disable-next-line svelte/prefer-svelte-reactivity
		const next = new Set(expandedLines);
		if (next.has(idx)) next.delete(idx);
		else next.add(idx);
		expandedLines = next;
	}

	function lineClass(level) {
		switch (level) {
			case 'INFO':
				return 'text-parchment-300';
			case 'ERROR':
			case 'FATAL':
				return 'text-terracotta-500';
			case 'DEBUG':
				return 'text-clay-500';
			case 'WARN':
				return 'text-gold-500';
			default:
				return 'text-parchment-300';
		}
	}

	function parseLine(line) {
		const m = line.match(/^(.*?\d{2}:\d{2}:\d{2})\s+(ERROR|FATAL|DEBUG|WARN|INFO)\s*:/);
		if (m) return { timestamp: m[1], level: m[2], rest: line.slice(m[0].length) };
		return { timestamp: '', level: '', rest: line };
	}

	function displayText(line) {
		const maxLen = 500;
		if (line.length <= maxLen) return { text: line, truncated: false };
		return { text: line.slice(0, maxLen) + '…', truncated: true };
	}

	onMount(() => {
		fetchLogs();
	});

	onDestroy(() => {
		if (refreshInterval) clearInterval(refreshInterval);
		if (controller) controller.abort();
	});
</script>

<div class="mx-auto flex h-full max-w-6xl flex-col space-y-4 p-6">
	<div class="flex items-center justify-between">
		<h1 class="text-2xl font-semibold text-parchment-200">Logs</h1>
		<div class="flex items-center gap-4">
			<div class="flex items-center gap-2">
				<label for="lines" class="text-sm text-parchment-400">Lines:</label>
				<input
					id="lines"
					type="number"
					name="lines"
					autocomplete="off"
					min="100"
					max="5000"
					value={lines}
					onchange={handleLinesChange}
					class="w-20 rounded-md border border-clay-700 bg-clay-900 px-2 py-1 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-gold-500"
				/>
			</div>
			<button
				onclick={toggleAutoRefresh}
				aria-pressed={autoRefresh}
				class="rounded-md px-3 py-1.5 text-xs font-medium transition-colors focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none {autoRefresh
					? 'bg-gold-500 text-clay-950 hover:bg-gold-600'
					: 'bg-clay-800 text-parchment-400 hover:bg-clay-700'}"
			>
				{autoRefresh ? '⟳ On' : '⟳ Off'}
			</button>
		</div>
	</div>

	<div class="flex gap-1 border-b border-clay-800" role="tablist">
		{#each tabs as tab (tab.id)}
			<button
				role="tab"
				aria-selected={activeTab === tab.id}
				onclick={() => handleTabClick(tab.id)}
				class="rounded-t-md px-4 py-2 text-sm font-medium transition-colors focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none {activeTab ===
				tab.id
					? 'bg-clay-800 text-parchment-200'
					: 'text-parchment-500 hover:bg-clay-900 hover:text-parchment-300'}"
			>
				{tab.label}
			</button>
		{/each}
	</div>

	<div class="min-h-0 flex-1">
		{#if loading && logs.length === 0}
			<div class="flex items-center justify-center py-16">
				<p class="text-parchment-500">Loading…</p>
			</div>
		{:else if error && logs.length === 0}
			<div class="flex flex-col items-center justify-center gap-3 py-16">
				<p class="text-parchment-500">{error}</p>
				<button
					onclick={fetchLogs}
					class="rounded-md bg-clay-800 px-4 py-2 text-sm font-medium text-parchment-400 hover:bg-clay-700 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
				>
					Retry
				</button>
			</div>
		{:else if logs.length === 0}
			<div class="flex items-center justify-center py-16">
				<p class="text-parchment-500">No log entries yet</p>
			</div>
		{:else}
			<div class="relative h-full">
				<div
					bind:this={scrollContainer}
					onscroll={handleScroll}
					class="h-full overflow-y-auto rounded-lg border border-clay-800 bg-clay-950 p-4 font-mono text-sm leading-relaxed"
				>
					{#each logs as line, idx (idx)}
						{@const parsed = parseLine(line)}
						{@const expanded = expandedLines.has(idx)}
						{@const display = expanded ? { text: line, truncated: false } : displayText(line)}
						<div
							class="break-all whitespace-pre-wrap {lineClass(parsed.level)}"
							style="content-visibility: auto; contain-intrinsic-size: auto 21px"
						>
							{#if display.truncated}
								{display.text}
							{:else if parsed.timestamp}
								<span class="text-clay-500">{parsed.timestamp}</span>
								<span class="ml-1 {lineClass(parsed.level)}">{parsed.level}</span>
								<span>{parsed.rest}</span>
							{:else}
								{line}
							{/if}
							{#if display.truncated}
								<button
									onclick={() => toggleExpand(idx)}
									class="ml-1 text-gold-500 hover:text-gold-400"
									aria-label="Expand full line">…</button
								>
							{/if}
							{#if expanded}
								<button
									onclick={() => toggleExpand(idx)}
									class="ml-1 text-gold-500 hover:text-gold-400"
									aria-label="Collapse line">…</button
								>
							{/if}
						</div>
					{/each}
				</div>
				{#if !atBottom}
					<button
						onclick={scrollToBottom}
						class="absolute right-4 bottom-4 rounded-lg bg-clay-800 px-3 py-2 text-xs font-medium text-parchment-400 shadow-lg transition-colors hover:bg-clay-700"
						aria-label="Scroll to bottom"
					>
						↓ Btm
					</button>
				{/if}
			</div>
		{/if}
	</div>
</div>
