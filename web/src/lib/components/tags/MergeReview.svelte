<script>
	import { onMount, untrack } from 'svelte';
	import { api } from '$lib/api';
	import { formatNumber } from '$lib/utils/html.js';
	import MergePairCard from './MergePairCard.svelte';

	const MERGE_LIMIT = 50;
	const HIDE_DISMISSED_KEY = 'edub:tags:merge-hide-dismissed';

	let { initialSuggestions = null, onchanged = null } = $props();

	let suggestions = $state(untrack(() => (initialSuggestions ? [...initialSuggestions] : [])));
	let loading = $state(false);
	let error = $state('');
	let hideDismissed = $state(true);

	const visible = $derived(hideDismissed ? suggestions.filter((s) => !s.dismissed) : suggestions);
	const pending = $derived(suggestions.filter((s) => !s.dismissed).length);

	onMount(() => {
		hideDismissed = localStorage.getItem(HIDE_DISMISSED_KEY) !== '0';
		if (!initialSuggestions) load();
	});

	async function load() {
		loading = true;
		error = '';
		const result = await api.tags.mergeSuggestions(MERGE_LIMIT, { includeDismissed: true });
		if (result.ok) {
			suggestions = result.data?.results ?? [];
		} else {
			suggestions = [];
			error =
				result.status === 503
					? 'Matcher unavailable — merge review is offline.'
					: 'Failed to load merge suggestions.';
		}
		loading = false;
	}

	function toggleHideDismissed() {
		hideDismissed = !hideDismissed;
		localStorage.setItem(HIDE_DISMISSED_KEY, hideDismissed ? '1' : '0');
	}

	function pairKey(s) {
		return `${s.tags[0].id}-${s.tags[1].id}`;
	}

	function removeSuggestion(s) {
		const key = pairKey(s);
		suggestions = suggestions.filter((x) => pairKey(x) !== key);
	}
</script>

<div class="mb-4 flex flex-wrap items-center gap-x-3 gap-y-2">
	<h2 class="text-base font-semibold text-parchment-200">Merge queue</h2>
	<span class="text-[0.8125rem] text-parchment-500">
		{formatNumber(pending)} pending{#if pending >= MERGE_LIMIT}
			· showing first {MERGE_LIMIT}{/if}
	</span>
	<label class="ms-auto flex items-center gap-2 text-[0.8125rem] text-parchment-500">
		<input
			type="checkbox"
			checked={hideDismissed}
			onchange={toggleHideDismissed}
			class="h-4 w-4 cursor-pointer accent-gold-500"
		/>
		Hide dismissed
	</label>
</div>

{#if loading}
	<p class="text-[0.8125rem] text-parchment-500">Loading merge suggestions…</p>
{:else if error}
	<p class="text-[0.8125rem] text-terracotta-500" role="alert">{error}</p>
{:else if visible.length === 0}
	<div
		class="rounded-lg border border-dashed border-clay-800 px-4 py-12 text-center text-parchment-500"
	>
		<p>
			<span
				class="mr-1.5 inline-block h-[0.45rem] w-[0.45rem] rounded-full bg-emerald-400"
				aria-hidden="true"
			></span>
			<b class="font-semibold text-parchment-300">Queue is clear.</b>
			No merge suggestions right now — the matcher will add pairs here when names drift apart.
		</p>
	</div>
{:else}
	<ul class="flex flex-col gap-3">
		{#each visible as s (pairKey(s))}
			<li>
				<MergePairCard
					suggestion={s}
					onremoved={removeSuggestion}
					ondismisschange={(dismissed) => (s.dismissed = dismissed)}
					{onchanged}
				/>
			</li>
		{/each}
	</ul>
{/if}
