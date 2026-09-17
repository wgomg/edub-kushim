<script>
	import { onMount } from 'svelte';
	import { resolve } from '$app/paths';
	import { api } from '$lib/api';
	import Icon from '$lib/components/Icon.svelte';
	import { confirmStore } from '$lib/stores/confirmStore.svelte.js';
	import { toastStore } from '$lib/stores/toastStore.svelte.js';
	import { documentsForTagHref, documentsLabel, formatNumber } from '$lib/utils/html.js';

	const PROVISIONAL_LIMIT = 50;

	const criteria = [
		{ key: 'subject_matter', label: 'subject matter' },
		{ key: 'standard_term', label: 'standard term' },
		{ key: 'not_person_place_work', label: 'not person/place/work' },
		{ key: 'appropriately_broad', label: 'appropriately broad' }
	];

	const cardClass = 'rounded-lg border border-clay-800 bg-clay-900 p-4';
	const criteriaPill = 'rounded-full px-2 py-0.5 font-mono text-[0.6875rem] whitespace-nowrap';
	const pillBase =
		'rounded-full px-2 py-0.5 font-mono text-[0.6875rem] font-medium whitespace-nowrap';
	const pillPlain = `${pillBase} bg-clay-800 text-parchment-400`;
	const pillOk = `${pillBase} bg-emerald-600/20 text-emerald-500`;
	const pillWarn = `${pillBase} bg-amber-600/20 text-amber-400`;
	const btnPrimary =
		'rounded-lg bg-gold-500 px-3 py-1.5 text-xs font-medium text-clay-950 hover:bg-gold-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-50';
	const btnGhost =
		'rounded-lg px-3 py-1.5 text-xs text-parchment-400 hover:bg-clay-800 hover:text-terracotta-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-50';
	const countLink =
		'flex items-center gap-1.5 text-xs text-parchment-400 tabular-nums underline-offset-[3px] hover:text-lapis-300 hover:underline hover:decoration-lapis-300/40 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none';

	let { onchanged = null } = $props();

	let tags = $state([]);
	let total = $state(0);
	let loading = $state(true);
	let error = $state('');
	let acting = $state(null);

	onMount(load);

	async function load() {
		loading = true;
		error = '';
		const result = await api.tags.provisionalList(PROVISIONAL_LIMIT, 0);
		if (result.ok) {
			tags = result.data?.results ?? [];
			total = result.data?.total ?? 0;
		} else {
			tags = [];
			total = 0;
			error = 'Failed to load new tags.';
		}
		loading = false;
	}

	async function approve(tag) {
		acting = tag.id;
		const result = await api.tags.promote(tag.id);
		acting = null;
		if (result.ok) {
			toastStore.success(`Approved "${tag.name}"`);
			if (onchanged) onchanged();
		} else if (result.status !== 409) {
			toastStore.error('Failed to approve tag');
		}
		await load();
	}

	async function reject(tag) {
		const ok = await confirmStore.confirm({
			title: 'Reject tag',
			message: `Reject tag "${tag.name}"? It is deleted and blocked from enrichment.`,
			danger: true
		});
		if (!ok) return;
		acting = tag.id;
		const result = await api.tags.delete(tag.id);
		acting = null;
		if (result.ok) {
			toastStore.success(`Rejected "${tag.name}"`);
			if (onchanged) onchanged();
		} else {
			toastStore.error('Failed to reject tag');
		}
		await load();
	}

	function criterionMark(value) {
		if (value === true) return '✓';
		if (value === false) return '✗';
		return '—';
	}

	function criterionClass(value) {
		if (value === true) return 'bg-emerald-600/20 text-emerald-500';
		if (value === false) return 'bg-clay-800 text-terracotta-500';
		return 'bg-clay-800 text-parchment-600';
	}

	function verdictPillClass(tag) {
		if (!tag.verdict) return pillPlain;
		return tag.verdict.pass ? pillOk : pillWarn;
	}

	function verdictLabel(tag) {
		if (!tag.verdict) return 'no verdict';
		if (!tag.verdict.pass) return 'judge rejected';
		return tag.document_count >= 2 ? 'eligible' : 'awaiting second document';
	}

	function verdictTitle(tag) {
		if (!tag.verdict) return 'Never judged — no verdict was recorded when this tag was born';
		if (tag.verdict.pass) return 'The quality judge passed this tag';
		return 'The quality judge rejected this tag';
	}

	function verdictMeta(tag) {
		return [tag.verdict.model, new Date(tag.verdict.verdict_at).toLocaleString()]
			.filter(Boolean)
			.join(' · ');
	}
</script>

<div class="mb-4 flex flex-wrap items-baseline gap-x-3 gap-y-1">
	<h2 class="text-base font-semibold text-parchment-200">Provisional queue</h2>
	{#if !loading && !error}
		<span class="text-[0.8125rem] text-parchment-500">
			{formatNumber(total)} awaiting review{#if total > tags.length}
				· showing first {formatNumber(tags.length)}{/if}
		</span>
	{/if}
</div>

<p class="mb-4 max-w-3xl text-[0.8125rem] text-parchment-500">
	New tags stay provisional until the quality judge passes them and a second document carries them.
	Approve promotes a tag into the shared vocabulary; reject deletes it and blocks it from
	enrichment.
</p>

{#if loading}
	<p class="text-[0.8125rem] text-parchment-500">Loading new tags…</p>
{:else if error}
	<p class="text-[0.8125rem] text-terracotta-500" role="alert">{error}</p>
{:else if tags.length === 0}
	<div
		class="rounded-lg border border-dashed border-clay-800 px-4 py-12 text-center text-parchment-500"
	>
		<p>
			<span
				class="mr-1.5 inline-block h-[0.45rem] w-[0.45rem] rounded-full bg-emerald-400"
				aria-hidden="true"
			></span>
			<b class="font-semibold text-parchment-300">Queue is clear.</b>
			No provisional tags. New tags appear here once enrichment assigns them to a document.
		</p>
	</div>
{:else}
	<ul class="flex flex-col gap-3">
		{#each tags as tag (tag.id)}
			<li class={cardClass}>
				<div class="flex flex-wrap items-center gap-2">
					<span class="text-[0.9375rem] font-semibold text-parchment-200">{tag.name}</span>
					<a href={resolve(documentsForTagHref(tag.name))} class={countLink}>
						<Icon name="external-link" size={12} class="text-lapis-400" />
						{documentsLabel(tag.document_count)}
					</a>
					<span class="text-xs text-parchment-500">
						· created {new Date(tag.created_at).toLocaleDateString()}
					</span>
					<span class={verdictPillClass(tag)} title={verdictTitle(tag)}>{verdictLabel(tag)}</span>
				</div>
				{#if tag.verdict}
					<div class="mt-2.5 flex flex-wrap items-center gap-1.5">
						{#each criteria as c (c.key)}
							<span class="{criteriaPill} {criterionClass(tag.verdict.criteria?.[c.key])}">
								{c.label}
								{criterionMark(tag.verdict.criteria?.[c.key])}
							</span>
						{/each}
						<span class="font-mono text-[0.6875rem] text-parchment-600">
							{verdictMeta(tag)}
						</span>
					</div>
				{/if}
				<div
					class="mt-3 flex flex-wrap items-center gap-2 border-t border-dashed border-clay-800 pt-3"
				>
					<span class="flex-1"></span>
					<button onclick={() => approve(tag)} disabled={acting === tag.id} class={btnPrimary}>
						Approve
					</button>
					<button onclick={() => reject(tag)} disabled={acting === tag.id} class={btnGhost}>
						Reject
					</button>
				</div>
			</li>
		{/each}
	</ul>
{/if}
