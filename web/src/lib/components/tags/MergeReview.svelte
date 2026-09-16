<script>
	import { onMount, untrack } from 'svelte';
	import { api } from '$lib/api';
	import { confirmStore } from '$lib/stores/confirmStore.svelte.js';
	import { toastStore } from '$lib/stores/toastStore.svelte.js';
	import { documentsLabel, formatNumber } from '$lib/utils/html.js';

	const MERGE_LIMIT = 50;

	const cardClass = 'rounded-lg border border-clay-800 bg-clay-900 p-4';
	const chipGold =
		'rounded-full border border-gold-800 bg-clay-950 px-2.5 py-0.5 font-mono text-[0.6875rem] font-semibold whitespace-nowrap text-gold-500';
	const pillBase =
		'rounded-full px-2 py-0.5 font-mono text-[0.6875rem] font-medium whitespace-nowrap';
	const pillPlain = `${pillBase} bg-clay-800 text-parchment-400`;
	const pillOk = `${pillBase} bg-emerald-600/20 text-emerald-500`;
	const btnPrimary =
		'rounded-lg bg-gold-500 px-3 py-1.5 text-xs font-medium text-clay-950 hover:bg-gold-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-50';
	const btnGhost =
		'rounded-lg px-3 py-1.5 text-xs text-parchment-400 hover:bg-clay-800 hover:text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-50';
	const selectClass =
		'rounded-md border border-clay-800 bg-clay-950 px-2 py-1 text-[0.8125rem] text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none';

	let { initialSuggestions = null, onchanged = null } = $props();

	let suggestions = $state(untrack(() => (initialSuggestions ? [...initialSuggestions] : [])));
	let loading = $state(false);
	let error = $state('');
	let survivorByPair = $state({});
	let mergingPair = $state(null);

	onMount(() => {
		if (!initialSuggestions) load();
	});

	async function load() {
		loading = true;
		error = '';
		const result = await api.tags.mergeSuggestions(MERGE_LIMIT);
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

	function pairKey(s) {
		return `${s.tags[0].id}-${s.tags[1].id}`;
	}

	function survivorId(s) {
		return survivorByPair[pairKey(s)] ?? s.tags[0].id;
	}

	async function handleMerge(s) {
		const key = pairKey(s);
		const survivor = s.tags.find((t) => t.id === survivorId(s));
		const removed = s.tags.find((t) => t.id !== survivor.id);
		const ok = await confirmStore.confirm({
			title: 'Merge tags',
			message: `Delete tag "${removed.name}" and move its documents into "${survivor.name}"?`,
			danger: true
		});
		if (!ok) return;
		mergingPair = key;
		const result = await api.tags.merge(removed.id, survivor.id);
		mergingPair = null;
		if (result.ok) {
			suggestions = suggestions.filter((x) => pairKey(x) !== key);
			toastStore.success(`Merged "${removed.name}" into "${survivor.name}"`);
			if (onchanged) onchanged();
		} else {
			toastStore.error('Failed to merge tags');
		}
	}

	function dismiss(s) {
		const key = pairKey(s);
		suggestions = suggestions.filter((x) => pairKey(x) !== key);
		toastStore.info('Suggestion dismissed');
		if (onchanged) onchanged();
	}
</script>

<div class="mb-4 flex flex-wrap items-baseline gap-x-3 gap-y-1">
	<h2 class="text-base font-semibold text-parchment-200">Merge queue</h2>
	<span class="text-[0.8125rem] text-parchment-500">
		{formatNumber(suggestions.length)} pending{#if suggestions.length >= MERGE_LIMIT}
			· showing first {MERGE_LIMIT}{/if}
	</span>
</div>

{#if loading}
	<p class="text-[0.8125rem] text-parchment-500">Loading merge suggestions…</p>
{:else if error}
	<p class="text-[0.8125rem] text-terracotta-500" role="alert">{error}</p>
{:else if suggestions.length === 0}
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
		{#each suggestions as s (pairKey(s))}
			<li class={cardClass}>
				<div class="flex flex-wrap items-center gap-2">
					<span class="text-[0.9375rem] font-semibold text-parchment-200">{s.tags[0].name}</span>
					<span class="text-xs text-parchment-500 tabular-nums">
						{documentsLabel(s.tags[0].document_count)}
					</span>
					<span class="text-[0.8125rem] text-parchment-600" aria-hidden="true">↔</span>
					<span class="text-[0.9375rem] font-semibold text-parchment-200">{s.tags[1].name}</span>
					<span class="text-xs text-parchment-500 tabular-nums">
						{documentsLabel(s.tags[1].document_count)}
					</span>
					<span class={chipGold} title="Embedding similarity">
						{Math.round(s.similarity * 100)}%
					</span>
					{#if s.confidence === 'population'}
						<span class={pillOk} title="This pair has usage history from past enrichment runs">
							backed by usage data
						</span>
					{:else}
						<span class={pillPlain} title="No usage data yet — suggested by name similarity alone">
							similarity only
						</span>
					{/if}
				</div>
				<p class="mt-2 max-w-3xl text-[0.8125rem] text-parchment-500">
					{#if s.confidence === 'population'}
						{documentsLabel(s.document_overlap)} carry both tags;
						{formatNumber(s.variant_count + s.related_count)} automatic checks judged them related rather
						than the same concept; {formatNumber(s.same_count)} judged them the same
					{:else}
						No usage data yet — suggested by name similarity alone.
					{/if}
				</p>
				<div
					class="mt-3 flex flex-wrap items-center gap-2 border-t border-dashed border-clay-800 pt-3"
				>
					<label class="flex items-center gap-2 text-[0.8125rem] text-parchment-500">
						Keep:
						<select
							value={survivorId(s)}
							onchange={(e) => (survivorByPair[pairKey(s)] = parseInt(e.currentTarget.value, 10))}
							aria-label="Surviving tag"
							class={selectClass}
						>
							{#each s.tags as t (t.id)}
								<option value={t.id}>{t.name}</option>
							{/each}
						</select>
					</label>
					<button
						onclick={() => handleMerge(s)}
						disabled={mergingPair === pairKey(s)}
						class={btnPrimary}
					>
						{mergingPair === pairKey(s) ? 'Merging…' : 'Merge'}
					</button>
					<button onclick={() => dismiss(s)} class={btnGhost}>Dismiss</button>
				</div>
			</li>
		{/each}
	</ul>
{/if}
