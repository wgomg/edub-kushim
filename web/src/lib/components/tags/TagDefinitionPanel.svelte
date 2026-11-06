<!--
  Looks a tag name up across Wikipedia, Wiktionary, and the Free Dictionary API so the operator can judge
  a tag about a topic they do not know. Rendered inside SlideOver; every source shows its own failure
  state and the panel never depends on the network succeeding.
-->
<script>
	import Icon from '#lib/components/Icon.svelte';
	import { forgetTerm, lookupTerm } from '#lib/utils/termLookup.js';

	let { term = '' } = $props();

	let activeTerm = $state('');
	let loading = $state(false);
	let result = $state(null);
	let seq = 0;

	let wikipedia = $derived(result?.wikipedia ?? null);
	let wiktionary = $derived(result?.wiktionary ?? null);
	let dictionary = $derived(result?.dictionary ?? null);
	let suggestions = $derived(result?.suggestions ?? []);
	let allMissing = $derived(
		!loading &&
			result !== null &&
			wikipedia?.status === 'not-found' &&
			wiktionary?.status === 'not-found' &&
			dictionary?.status === 'not-found'
	);
	let allErrored = $derived(
		!loading &&
			result !== null &&
			wikipedia?.status === 'error' &&
			wiktionary?.status === 'error' &&
			dictionary?.status === 'error'
	);

	const sectionTitle = 'mb-1.5 text-sm font-semibold text-parchment-200';
	const text = 'text-[0.8125rem] leading-relaxed text-parchment-400';
	const lead = 'text-[0.8125rem] leading-relaxed text-parchment-400';
	const meta = 'text-[0.75rem] text-parchment-500';
	const sourceLink =
		'mt-1.5 inline-flex items-center gap-1 text-[0.6875rem] text-lapis-400 hover:text-lapis-300 hover:underline focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none';
	const btnRetry =
		'shrink-0 rounded-md border border-clay-700 px-2.5 py-1 text-xs text-parchment-400 hover:border-gold-500 hover:text-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-40';
	const suggestion =
		'rounded-full border border-clay-700 px-2 py-0.5 text-[0.6875rem] text-parchment-300 hover:border-gold-500 hover:text-gold-400 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none';
	const senseList = 'flex flex-col gap-1.5';
	const sensePos = 'font-mono text-[0.625rem] tracking-wide text-parchment-600 uppercase';

	$effect(() => {
		const name = term;
		if (name) {
			activeTerm = name;
			load(name);
		}
	});

	async function load(name) {
		const id = ++seq;
		loading = true;
		result = null;
		const next = await lookupTerm(name);
		if (id !== seq) return;
		result = next;
		loading = false;
	}

	function openSuggestion(title) {
		activeTerm = title;
		load(title);
	}

	function retry() {
		forgetTerm(activeTerm);
		load(activeTerm);
	}

	function sourceMessage(source) {
		if (!source) return '';
		return source.status === 'not-found' ? 'No entry found.' : 'The source could not be reached.';
	}
</script>

{#snippet senseItems(senses)}
	<ul class={senseList}>
		{#each senses as sense, index (index)}
			<li class={text}>
				{#if sense.partOfSpeech}<span class="{sensePos} me-1.5">{sense.partOfSpeech}</span
					>{/if}{sense.definition}
				{#each sense.examples as example, exampleIndex (exampleIndex)}
					<span class="block text-[0.75rem] text-parchment-500 italic">{example}</span>
				{/each}
			</li>
		{/each}
	</ul>
{/snippet}

<div class="flex flex-col gap-5">
	<div class="flex items-start justify-between gap-3">
		<p class={lead}>
			Definitions for <span class="font-semibold text-parchment-200">{activeTerm}</span> from free public
			sources.
		</p>
		<button type="button" class={btnRetry} onclick={retry} disabled={loading}>Retry</button>
	</div>

	{#if loading}
		<p class={text}>Looking up “{activeTerm}”…</p>
	{:else if result}
		{#if allMissing}
			<p class={text}>
				No definition found in any source for “{activeTerm}”. The term may be jargon, a proper name,
				or a non-English value.
			</p>
		{:else if allErrored}
			<p class={text}>
				The sources could not be reached for “{activeTerm}”. Check the network connection and retry.
			</p>
		{/if}

		<section>
			<h3 class={sectionTitle}>Wikipedia</h3>
			{#if wikipedia?.status === 'ok'}
				{#if wikipedia.disambiguation}
					<p class={text}>
						“{wikipedia.title}” is a disambiguation page — the name can refer to more than one
						topic.
					</p>
				{:else}
					{#if wikipedia.description}
						<p class={meta}>{wikipedia.description}</p>
					{/if}
					<p class={text}>{wikipedia.extract}</p>
				{/if}
				{#if wikipedia.url}
					<a
						class={sourceLink}
						href={wikipedia.url}
						target="_blank"
						rel="external noopener noreferrer"
					>
						Read on Wikipedia
						<Icon name="external-link" size={11} />
					</a>
				{/if}
			{:else}
				<p class={text}>{sourceMessage(wikipedia)}</p>
			{/if}
			{#if suggestions.length > 0}
				<p class="{meta} mt-2">Possibly:</p>
				<ul class="mt-1 flex flex-wrap gap-1.5">
					{#each suggestions as title (title)}
						<li>
							<button type="button" class={suggestion} onclick={() => openSuggestion(title)}>
								{title}
							</button>
						</li>
					{/each}
				</ul>
			{/if}
		</section>

		<section>
			<h3 class={sectionTitle}>Wiktionary</h3>
			{#if wiktionary?.status === 'ok'}
				{@render senseItems(wiktionary.senses)}
				<a
					class={sourceLink}
					href={wiktionary.url}
					target="_blank"
					rel="external noopener noreferrer"
				>
					Read on Wiktionary
					<Icon name="external-link" size={11} />
				</a>
			{:else}
				<p class={text}>{sourceMessage(wiktionary)}</p>
			{/if}
		</section>

		<section>
			<h3 class={sectionTitle}>Free Dictionary</h3>
			{#if dictionary?.status === 'ok'}
				{#if dictionary.phonetic}
					<p class={meta}>{dictionary.phonetic}</p>
				{/if}
				{@render senseItems(dictionary.senses)}
				<a
					class={sourceLink}
					href={dictionary.url}
					target="_blank"
					rel="external noopener noreferrer"
				>
					Free Dictionary API
					<Icon name="external-link" size={11} />
				</a>
			{:else}
				<p class={text}>{sourceMessage(dictionary)}</p>
			{/if}
		</section>
	{:else}
		<p class={text}>No term selected.</p>
	{/if}
</div>
