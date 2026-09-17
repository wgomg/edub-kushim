<script>
	import { onMount } from 'svelte';
	import { api } from '$lib/api';
	import MergePairCard from './MergePairCard.svelte';

	const MERGE_LIMIT = 50;

	let { tag, onchanged = null } = $props();

	let suggestions = $state([]);
	let loading = $state(true);
	let error = $state('');

	const visible = $derived(suggestions.filter((s) => !s.dismissed));

	onMount(load);

	async function load() {
		loading = true;
		error = '';
		const result = await api.tags.mergeSuggestions(MERGE_LIMIT, { forTag: tag.name });
		if (result.ok) {
			suggestions = result.data?.results ?? [];
		} else {
			suggestions = [];
			error =
				result.status === 503
					? 'Matcher unavailable — merge suggestions are offline.'
					: 'Failed to load merge suggestions.';
		}
		loading = false;
	}

	function pairKey(s) {
		return `${s.tags[0].id}-${s.tags[1].id}`;
	}

	function removeSuggestion(s) {
		const key = pairKey(s);
		suggestions = suggestions.filter((x) => pairKey(x) !== key);
	}
</script>

{#if loading}
	<p class="text-[0.8125rem] text-parchment-500">Loading merge suggestions…</p>
{:else if error}
	<p class="text-[0.8125rem] text-terracotta-500" role="alert">{error}</p>
{:else if visible.length === 0}
	<p class="text-[0.8125rem] text-parchment-500">No merge suggestions for this tag.</p>
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
