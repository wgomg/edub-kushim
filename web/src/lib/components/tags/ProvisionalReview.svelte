<script>
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { replaceState } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { api, encodeCriteria } from '$lib/api';
	import Icon from '$lib/components/Icon.svelte';
	import { confirmStore } from '$lib/stores/confirmStore.svelte.js';
	import { toastStore } from '$lib/stores/toastStore.svelte.js';
	import { documentsForTagHref, documentsLabel, formatNumber } from '$lib/utils/html.js';

	const PAGE_SIZE = 50;

	const criteria = [
		{ key: 'subject_matter', label: 'subject matter' },
		{ key: 'dimension', label: 'dimension' },
		{ key: 'not_person_place_work', label: 'not person/place/work' },
		{ key: 'atomic', label: 'atomic' },
		{ key: 'not_form_genre', label: 'not publishing format' },
		{ key: 'defer', label: 'defer' }
	];

	const FILTER_STATES = [
		{ value: '', label: 'Any' },
		{ value: 'pass', label: '✓ pass' },
		{ value: 'fail', label: '✗ fail' },
		{ value: 'absent', label: '— absent' }
	];

	const VERDICT_FILTERS = [
		{ value: '', label: 'Any verdict' },
		{ value: 'none', label: 'no verdict' },
		{ value: 'admit', label: 'awaiting promotion floor' },
		{ value: 'defer', label: 'deferred to review' },
		{ value: 'reject', label: 'judge rejected' }
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
	const selectClass =
		'rounded-md border border-clay-700 bg-clay-900 px-2 py-1 text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none';
	const pagerButton =
		'rounded-md border border-clay-700 px-2.5 py-1 text-parchment-400 hover:border-gold-500 hover:text-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-40';

	let { onchanged = null } = $props();

	let tags = $state([]);
	let total = $state(0);
	let loading = $state(true);
	let error = $state('');
	let acting = $state(null);
	let identityActing = $state(false);
	let offset = $state(0);
	let pendingTotal = $state(0);
	let filterStates = $state({});
	let verdictFilter = $state('');
	let loadSeq = 0;

	let hasFilters = $derived(criteria.some((c) => filterStates[c.key]) || verdictFilter !== '');

	onMount(() => {
		const sp = page.url.searchParams;

		const rawPage = sp.get('prov_page') ?? '';
		if (/^\d+$/.test(rawPage)) offset = parseInt(rawPage, 10) * PAGE_SIZE;

		const rawCriteria = sp.get('prov_criteria') ?? '';
		for (const entry of rawCriteria ? rawCriteria.split(',') : []) {
			const [key, state] = entry.split(':');
			if (criteria.some((c) => c.key === key) && FILTER_STATES.some((s) => s.value === state)) {
				filterStates[key] = state;
			}
		}

		const rawVerdict = sp.get('prov_verdict') ?? '';
		if (VERDICT_FILTERS.some((v) => v.value === rawVerdict)) verdictFilter = rawVerdict;

		load();
	});

	function activeCriteria() {
		return criteria
			.filter((c) => filterStates[c.key])
			.map((c) => ({ key: c.key, state: filterStates[c.key] }));
	}

	function syncUrl() {
		// transient helper for replaceState; SvelteURLSearchParams not needed here
		// eslint-disable-next-line svelte/prefer-svelte-reactivity
		const sp = new URLSearchParams(page.url.searchParams);
		if (offset > 0) sp.set('prov_page', String(offset / PAGE_SIZE));
		else sp.delete('prov_page');

		const active = encodeCriteria(activeCriteria());
		if (active) sp.set('prov_criteria', active);
		else sp.delete('prov_criteria');

		if (verdictFilter) sp.set('prov_verdict', verdictFilter);
		else sp.delete('prov_verdict');

		replaceState(resolve(`${page.url.pathname}?${sp.toString()}${page.url.hash}`));
	}

	async function load(clamp = true) {
		const seq = ++loadSeq;
		loading = true;
		error = '';
		const result = await api.tags.provisionalList(PAGE_SIZE, offset, {
			criteria: activeCriteria(),
			verdict: verdictFilter
		});
		if (seq !== loadSeq) return;
		if (!result.ok) {
			tags = [];
			total = 0;
			pendingTotal = 0;
			error = 'Failed to load new tags.';
			loading = false;
			return;
		}
		tags = result.data?.results ?? [];
		total = result.data?.total ?? 0;
		pendingTotal = result.data?.pending_identity_total ?? 0;
		// an emptied page and a deep-link offset the API clamped away both
		// surface as offset >= total
		if (clamp && offset > 0 && total <= offset) {
			offset = Math.max(0, (Math.ceil(total / PAGE_SIZE) - 1) * PAGE_SIZE);
			syncUrl();
			return load(false);
		}
		loading = false;
	}

	function onFilterChange() {
		offset = 0;
		load();
		syncUrl();
	}

	function clearFilters() {
		for (const c of criteria) filterStates[c.key] = '';
		verdictFilter = '';
		onFilterChange();
	}

	function prevPage() {
		if (offset === 0) return;
		offset = Math.max(0, offset - PAGE_SIZE);
		load();
		syncUrl();
	}

	function nextPage() {
		if (offset + tags.length >= total) return;
		offset += PAGE_SIZE;
		load();
		syncUrl();
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

	async function reevaluateIdentity() {
		identityActing = true;
		const result = await api.tags.reevaluateIdentity();
		identityActing = false;
		if (result.ok) {
			toastStore.success('Identity adjudication queued');
		} else {
			toastStore.error('Failed to queue identity adjudication');
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

	function verdictOutcome(tag) {
		if (tag.verdict.outcome) return tag.verdict.outcome;
		if (tag.verdict.pass) return 'admit';
		return tag.verdict.criteria?.defer === true ? 'defer' : 'reject';
	}

	function verdictLabel(tag) {
		if (!tag.verdict) return 'no verdict';
		if (tag.verdict.pass) return 'awaiting promotion floor';
		return verdictOutcome(tag) === 'defer' ? 'deferred to review' : 'judge rejected';
	}

	function verdictTitle(tag) {
		if (!tag.verdict) return 'Never judged — no verdict was recorded when this tag was born';
		if (tag.verdict.pass) return 'The quality judge admitted this tag';
		if (verdictOutcome(tag) === 'defer') return 'The quality judge deferred this tag to review';
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
			{formatNumber(total)} awaiting review{#if hasFilters}
				· filtered{/if}
		</span>
	{/if}
	{#if pendingTotal > 0}
		<button
			onclick={reevaluateIdentity}
			disabled={identityActing}
			class="{btnGhost} ml-auto"
			title="Adjudicate the pending identity evidence now instead of waiting for the scheduled sweep"
		>
			{identityActing ? 'Queueing…' : 'Reevaluate identity'}
		</button>
	{/if}
</div>

<div class="mb-4 flex flex-wrap items-center gap-x-3 gap-y-2">
	{#each criteria as c (c.key)}
		<label class="flex items-center gap-1.5 text-[0.6875rem] text-parchment-500">
			{c.label}
			<select
				value={filterStates[c.key] ?? ''}
				onchange={(e) => {
					filterStates[c.key] = e.target.value;
					onFilterChange();
				}}
				class={selectClass}
			>
				{#each FILTER_STATES as s (s.value)}
					<option value={s.value}>{s.label}</option>
				{/each}
			</select>
		</label>
	{/each}
	<label class="flex items-center gap-1.5 text-[0.6875rem] text-parchment-500">
		verdict
		<select
			value={verdictFilter}
			onchange={(e) => {
				verdictFilter = e.target.value;
				onFilterChange();
			}}
			class={selectClass}
		>
			{#each VERDICT_FILTERS as v (v.value)}
				<option value={v.value}>{v.label}</option>
			{/each}
		</select>
	</label>
	{#if hasFilters}
		<button onclick={clearFilters} class={btnGhost}>Clear filters</button>
	{/if}
</div>

<p class="mb-4 max-w-3xl text-[0.8125rem] text-parchment-500">
	New tags stay provisional until the admission decision admits them and enough live documents carry
	them to clear the promotion floor; uncertain ones wait here for review. Approve promotes a tag
	into the shared vocabulary; reject deletes it and blocks it from enrichment.
</p>

{#if loading}
	<p class="text-[0.8125rem] text-parchment-500">Loading new tags…</p>
{:else if error}
	<p class="text-[0.8125rem] text-terracotta-500" role="alert">{error}</p>
{:else if tags.length === 0}
	<div
		class="rounded-lg border border-dashed border-clay-800 px-4 py-12 text-center text-parchment-500"
	>
		{#if hasFilters}
			<p>
				No tags match the current filters.
				<button
					onclick={clearFilters}
					class="underline underline-offset-[3px] hover:text-parchment-300"
				>
					Clear filters
				</button>
			</p>
		{:else}
			<p>
				<span
					class="mr-1.5 inline-block h-[0.45rem] w-[0.45rem] rounded-full bg-emerald-400"
					aria-hidden="true"
				></span>
				<b class="font-semibold text-parchment-300">Queue is clear.</b>
				No provisional tags. New tags appear here once enrichment assigns them to a document.
			</p>
		{/if}
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
					{#if tag.pending_identity > 0}
						<span
							class={pillWarn}
							title="This tag is waiting for a same-intent decision; the scheduled sweep resolves it, or trigger the reevaluation"
						>
							identity pending
						</span>
					{/if}
					{#if tag.identity_hold}
						<span
							class={pillWarn}
							title="The identity judge refused a fold of this tag onto that store value; promotion waits for review"
						>
							identity hold → {tag.identity_hold}
						</span>
					{/if}
					{#if tag.composition_hold}
						<span
							class={pillWarn}
							title="The split decision could not settle this compound; promotion waits for review"
						>
							split hold → {tag.composition_hold}
						</span>
					{/if}
					{#if tag.composition_reason === 'judge_unavailable'}
						<span
							class={pillPlain}
							title="No split judge was available; the compound was kept whole and is re-judged when emitted again"
						>
							composition → judge_unavailable
						</span>
					{/if}
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
	{#if total > 0}
		<div class="flex flex-wrap items-center justify-between gap-3 pt-1 text-sm">
			<p class="text-parchment-500">
				Showing {offset + 1}–{offset + tags.length} of {total}
			</p>
			<div class="flex items-center gap-3">
				<button disabled={offset === 0} onclick={prevPage} class={pagerButton}>Prev</button>
				<button disabled={offset + tags.length >= total} onclick={nextPage} class={pagerButton}>
					Next
				</button>
			</div>
		</div>
	{/if}
{/if}
