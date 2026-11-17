<!--
  Lists the live documents currently carrying one tag — title plus their assigned tags — so the operator can
  judge a pending value in context without leaving the queue. Rendered inside SlideOver; the search endpoint
  caps a page at 100 results, so further pages load while more remain.
-->
<script>
	import { resolve } from '$app/paths';
	import { api } from '#lib/api.js';
	import { documentsLabel, formatNumber } from '#lib/utils/html.js';

	let { tag = '' } = $props();

	const PAGE_SIZE = 100;

	let rows = $state([]);
	let total = $state(0);
	let loading = $state(false);
	let failed = $state(false);
	let seq = 0;

	const text = 'text-[0.8125rem] leading-relaxed text-parchment-400';
	const meta = 'text-[0.75rem] text-parchment-500';
	const rowClass =
		'block rounded-md border border-clay-800 px-3 py-2.5 hover:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none';
	const titleClass = 'block truncate text-[0.8125rem] font-medium text-parchment-200';
	const tagPill = 'rounded-full bg-clay-800 px-2 py-0.5 text-[0.6875rem] text-parchment-300';
	const btnRetry =
		'shrink-0 rounded-md border border-clay-700 px-2.5 py-1 text-xs text-parchment-400 hover:border-gold-500 hover:text-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-40';
	const btnMore =
		'self-center rounded-md border border-clay-700 px-3 py-1.5 text-xs text-parchment-300 hover:border-gold-500 hover:text-gold-400 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-40';

	$effect(() => {
		if (tag) {
			rows = [];
			total = 0;
			load(tag, 0);
		}
	});

	async function load(name, offset) {
		const id = ++seq;
		loading = true;
		failed = false;
		const result = await api.documents.searchStructuredRaw({
			tags: [name],
			limit: PAGE_SIZE,
			offset
		});
		if (id !== seq) return;
		loading = false;
		if (!result.ok) {
			failed = true;
			return;
		}
		total = result.data?.total ?? 0;
		const next = result.data?.results ?? [];
		rows = offset === 0 ? next : [...rows, ...next];
	}

	function retry() {
		load(tag, 0);
	}

	function loadMore() {
		load(tag, rows.length);
	}
</script>

<div class="flex flex-col gap-4">
	<div class="flex items-start justify-between gap-3">
		<div>
			<p class={text}>
				Documents currently carrying
				<span class="font-semibold text-parchment-200">“{tag}”</span>.
			</p>
			{#if !failed && (total > 0 || rows.length > 0)}
				<p class="{meta} mt-0.5">{documentsLabel(total)}</p>
			{/if}
		</div>
		{#if failed}
			<button type="button" class={btnRetry} onclick={retry} disabled={loading}>Retry</button>
		{/if}
	</div>

	{#if failed}
		<p class={text}>The document list could not be loaded. Check the connection and retry.</p>
	{:else if loading && rows.length === 0}
		<p class={text}>Loading documents…</p>
	{:else if rows.length === 0}
		<p class={text}>No live document carries this tag right now.</p>
	{:else}
		<ul class="flex flex-col gap-1.5">
			{#each rows as row (row.id)}
				<li>
					<a class={rowClass} href={resolve(`documents/${row.id}`)}>
						<span class={titleClass}>{row.title || 'Untitled document'}</span>
						{#if row.tags?.length > 0}
							<span class="mt-1.5 flex flex-wrap gap-1">
								{#each row.tags as assigned (assigned.id)}
									<span class={tagPill}>{assigned.name}</span>
								{/each}
							</span>
						{/if}
					</a>
				</li>
			{/each}
		</ul>
		{#if rows.length < total}
			<button type="button" class={btnMore} onclick={loadMore} disabled={loading}>
				{loading ? 'Loading…' : `Load more (${formatNumber(total - rows.length)} remaining)`}
			</button>
		{/if}
	{/if}
</div>
