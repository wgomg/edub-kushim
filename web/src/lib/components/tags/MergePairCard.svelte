<script>
	import { resolve } from '$app/paths';
	import { api } from '$lib/api';
	import Icon from '$lib/components/Icon.svelte';
	import { confirmStore } from '$lib/stores/confirmStore.svelte.js';
	import { toastStore } from '$lib/stores/toastStore.svelte.js';
	import { documentsForTagHref, documentsLabel, formatNumber } from '$lib/utils/html.js';

	const cardClass = 'rounded-lg border border-clay-800 bg-clay-900 p-4';
	const chipGold =
		'rounded-full border border-gold-800 bg-clay-950 px-2.5 py-0.5 font-mono text-[0.6875rem] font-semibold whitespace-nowrap text-gold-500';
	const pillBase =
		'rounded-full px-2 py-0.5 font-mono text-[0.6875rem] font-medium whitespace-nowrap';
	const pillPlain = `${pillBase} bg-clay-800 text-parchment-400`;
	const pillOk = `${pillBase} bg-emerald-600/20 text-emerald-500`;
	const pillMuted = `${pillBase} bg-clay-800 text-parchment-500`;
	const btnPrimary =
		'rounded-lg bg-gold-500 px-3 py-1.5 text-xs font-medium text-clay-950 hover:bg-gold-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-50';
	const btnGhost =
		'rounded-lg px-3 py-1.5 text-xs text-parchment-400 hover:bg-clay-800 hover:text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-50';
	const selectClass =
		'rounded-md border border-clay-800 bg-clay-950 px-2 py-1 text-[0.8125rem] text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none';
	const countLink =
		'flex items-center gap-1.5 text-xs text-parchment-400 tabular-nums underline-offset-[3px] hover:text-lapis-300 hover:underline hover:decoration-lapis-300/40 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none';

	let { suggestion, onchanged = null, onremoved = null, ondismisschange = null } = $props();

	let survivorOverride = $state(null);
	const survivor = $derived(
		suggestion.tags.some((t) => t.id === survivorOverride)
			? survivorOverride
			: suggestion.tags[0].id
	);
	let merging = $state(false);
	let dismissing = $state(false);
	let restoring = $state(false);

	async function handleMerge() {
		const kept = suggestion.tags.find((t) => t.id === survivor);
		const removed = suggestion.tags.find((t) => t.id !== kept.id);
		const ok = await confirmStore.confirm({
			title: 'Merge tags',
			message: `Delete tag "${removed.name}" and move its documents into "${kept.name}"?`,
			danger: true
		});
		if (!ok) return;
		merging = true;
		const result = await api.tags.merge(removed.id, kept.id);
		merging = false;
		if (result.ok) {
			toastStore.success(`Merged "${removed.name}" into "${kept.name}"`);
			if (onremoved) onremoved(suggestion);
			if (onchanged) onchanged();
		} else {
			toastStore.error('Failed to merge tags');
		}
	}

	async function dismiss() {
		dismissing = true;
		const result = await api.tags.dismissMergePair(
			suggestion.tags[0].name,
			suggestion.tags[1].name
		);
		dismissing = false;
		if (result.ok) {
			if (ondismisschange) ondismisschange(true);
			toastStore.info('Suggestion dismissed');
			if (onchanged) onchanged();
		} else {
			toastStore.error('Failed to dismiss suggestion');
		}
	}

	async function restore() {
		restoring = true;
		const result = await api.tags.restoreMergePair(
			suggestion.tags[0].name,
			suggestion.tags[1].name
		);
		restoring = false;
		if (result.ok) {
			if (ondismisschange) ondismisschange(false);
			toastStore.info('Suggestion restored');
			if (onchanged) onchanged();
		} else {
			toastStore.error('Failed to restore suggestion');
		}
	}
</script>

<div class={cardClass}>
	<div class="flex flex-wrap items-center gap-2">
		<span class="text-[0.9375rem] font-semibold text-parchment-200">{suggestion.tags[0].name}</span>
		<a href={resolve(documentsForTagHref(suggestion.tags[0].name))} class={countLink}>
			<Icon name="external-link" size={12} class="text-lapis-400" />
			{documentsLabel(suggestion.tags[0].document_count)}
		</a>
		<span class="text-[0.8125rem] text-parchment-600" aria-hidden="true">↔</span>
		<span class="text-[0.9375rem] font-semibold text-parchment-200">{suggestion.tags[1].name}</span>
		<a href={resolve(documentsForTagHref(suggestion.tags[1].name))} class={countLink}>
			<Icon name="external-link" size={12} class="text-lapis-400" />
			{documentsLabel(suggestion.tags[1].document_count)}
		</a>
		<span class={chipGold} title="Embedding similarity">
			{Math.round(suggestion.similarity * 100)}%
		</span>
		{#if suggestion.confidence === 'population'}
			<span class={pillOk} title="This pair has usage history from past enrichment runs">
				backed by usage data
			</span>
		{:else}
			<span class={pillPlain} title="No usage data yet — suggested by name similarity alone">
				similarity only
			</span>
		{/if}
		{#if suggestion.dismissed}
			<span class={pillMuted} title="This pair is hidden from the merge queue">dismissed</span>
		{/if}
	</div>
	<p class="mt-2 max-w-3xl text-[0.8125rem] text-parchment-500">
		{#if suggestion.confidence === 'population'}
			{documentsLabel(suggestion.document_overlap)} carry both tags;
			{formatNumber(suggestion.variant_count + suggestion.related_count)} automatic checks judged them
			related rather than the same concept; {formatNumber(suggestion.same_count)} judged them the same
		{:else}
			No usage data yet — suggested by name similarity alone.
		{/if}
	</p>
	<div class="mt-3 flex flex-wrap items-center gap-2 border-t border-dashed border-clay-800 pt-3">
		{#if suggestion.dismissed}
			<button onclick={restore} disabled={restoring} class={btnGhost}>Restore</button>
		{:else}
			<label class="flex items-center gap-2 text-[0.8125rem] text-parchment-500">
				Keep:
				<select
					value={survivor}
					onchange={(e) => (survivorOverride = parseInt(e.currentTarget.value, 10))}
					aria-label="Surviving tag"
					class={selectClass}
				>
					{#each suggestion.tags as t (t.id)}
						<option value={t.id}>{t.name}</option>
					{/each}
				</select>
			</label>
			<button onclick={handleMerge} disabled={merging} class={btnPrimary}>
				{merging ? 'Merging…' : 'Merge'}
			</button>
			<button onclick={dismiss} disabled={dismissing} class={btnGhost}>Dismiss</button>
		{/if}
	</div>
</div>
