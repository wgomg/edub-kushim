<script>
	import { formatNumber } from '#lib/utils/html.js';

	/**
	 * Paged suggestion combobox: one control owning the query input, the debounced paged fetch, the
	 * stale-response invalidation, the dropdown listbox with its load-more footer, and the pointer and
	 * keyboard selection. Callers supply the endpoint through their own `fetch` — which receives the
	 * trimmed query, the number of rows already loaded, and an abort signal, and returns an envelope
	 * like `{ items, total }` or throws — plus the styling, a pick callback, and optional failure and
	 * free-text hooks, so no consumer ever re-implements the fetching, debounce, or keyboard handling. A
	 * multi-source `fetch` may ignore `offset` and page its own cursors; the component appends only what
	 * it gets back, and the offset it passes counts rows already loaded, never a page number of its own.
	 *
	 * With the `input` snippet the caller renders its own input (a tokenized search bar, for instance) and
	 * forwards the provided `oninput`/`onkeydown` callbacks, so a richer composition still gets the same
	 * shared listbox. `onselect` runs on a pick; the component then closes the dropdown but leaves the
	 * query alone, so each caller decides whether to clear it or keep the picked text.
	 */
	let {
		fetch,
		debounceMs = 200,
		id = 'suggest',
		label = '',
		placeholder = '',
		inputClass = '',
		listClass = '',
		rowClass = 'px-3 py-1.5 text-sm',
		filter = null,
		combobox = true,
		disabled = false,
		onselect = () => {},
		onenter = null,
		onerror = null,
		query = $bindable(''),
		results = $bindable([]),
		input = null,
		row = null
	} = $props();

	const rowStyle = 'content-visibility: auto; contain-intrinsic-size: auto 32px';

	let total = $state(0);
	let loading = $state(false);
	let closed = $state(true);
	let highlight = $state(-1);

	let sequence = 0;
	let inflight = null;
	let timer = null;

	const displayed = $derived(filter ? results.filter(filter) : results);
	const listboxId = $derived(`${id}-listbox`);
	const panelOpen = $derived(!closed && displayed.length > 0);
	const hasMore = $derived(results.length < total);
	const activeDescendant = $derived(
		combobox && highlight >= 0 && highlight < displayed.length
			? `${id}-option-${highlight}`
			: undefined
	);

	$effect(() => {
		const q = query.trim();
		const mySequence = ++sequence;

		results = [];
		total = 0;
		highlight = -1;
		loading = false;

		if (!q) return;

		loading = true;
		timer = setTimeout(() => {
			timer = null;
			runPage(q, 0, mySequence);
		}, debounceMs);

		return () => {
			if (timer) {
				clearTimeout(timer);
				timer = null;
			}
			if (inflight) {
				inflight.abort();
				inflight = null;
			}
		};
	});

	async function runPage(q, offset, mySequence) {
		const controller = new AbortController();
		inflight = controller;

		try {
			const page = await fetch(q, offset, controller.signal);
			if (mySequence !== sequence) return;

			const items = page?.items ?? [];
			results = offset === 0 ? items : [...results, ...items];
			total = page?.total ?? 0;
		} catch (err) {
			if (mySequence !== sequence || err?.name === 'AbortError') return;
			if (offset === 0) {
				results = [];
				total = 0;
			}
			if (onerror) onerror(err instanceof Error ? err.message : 'Failed to load suggestions');
		} finally {
			if (mySequence === sequence) {
				inflight = null;
				loading = false;
			}
		}
	}

	function loadMore() {
		if (loading || !hasMore) return;
		loading = true;
		runPage(query.trim(), results.length, sequence);
	}

	function handleInput() {
		closed = false;
	}

	function pick(item) {
		closed = true;
		highlight = -1;
		onselect(item);
	}

	function close() {
		closed = true;
		highlight = -1;
	}

	function moveHighlight(next) {
		highlight = next;
		document.getElementById(`${id}-option-${next}`)?.scrollIntoView?.({ block: 'nearest' });
	}

	function handleKeydown(e) {
		if (e.key === 'Escape') {
			if (!panelOpen) return false;
			e.preventDefault();
			close();
			return true;
		}

		if (e.key === 'Enter') {
			if (combobox && panelOpen && highlight >= 0 && displayed[highlight]) {
				e.preventDefault();
				pick(displayed[highlight]);
				return true;
			}
			if (onenter && onenter(query, panelOpen ? displayed[0] : undefined)) {
				e.preventDefault();
				return true;
			}
			return false;
		}

		if (!combobox || !panelOpen) return false;

		if (e.key === 'ArrowDown') {
			e.preventDefault();
			moveHighlight(Math.min(highlight + 1, displayed.length - 1));
			return true;
		}

		if (e.key === 'ArrowUp') {
			e.preventDefault();
			moveHighlight(Math.max(highlight - 1, -1));
			return true;
		}

		return false;
	}
</script>

{#if input}
	{@render input({
		oninput: handleInput,
		onkeydown: handleKeydown,
		close,
		panelOpen,
		listboxId,
		activeDescendant
	})}
{:else}
	<input
		{id}
		name={id}
		type="text"
		bind:value={query}
		oninput={handleInput}
		onkeydown={handleKeydown}
		role={combobox ? 'combobox' : undefined}
		aria-label={label || undefined}
		aria-expanded={combobox ? panelOpen : undefined}
		aria-controls={combobox ? listboxId : undefined}
		aria-autocomplete={combobox ? 'list' : undefined}
		aria-activedescendant={combobox ? activeDescendant : undefined}
		{placeholder}
		{disabled}
		autocomplete="off"
		spellcheck="false"
		class={inputClass}
	/>
{/if}

{#if panelOpen}
	<div class="overscroll-contain {listClass}">
		<ul
			role={combobox ? 'listbox' : undefined}
			id={combobox ? listboxId : undefined}
			aria-labelledby={combobox && label ? id : undefined}
		>
			{#each displayed as item, i (i)}
				{#if row}
					<li style={rowStyle}>
						{@render row({ item, index: i, highlighted: i === highlight })}
					</li>
				{:else}
					<li
						id={`${id}-option-${i}`}
						role="option"
						aria-selected={i === highlight}
						style={rowStyle}
						class="cursor-pointer text-parchment-200 hover:bg-clay-800 {rowClass} {i === highlight
							? 'bg-clay-800'
							: ''}"
						onmousedown={() => pick(item)}
						onmouseenter={() => (highlight = i)}
					>
						{item.name}
					</li>
				{/if}
			{/each}
		</ul>
		{#if hasMore}
			<button
				type="button"
				class="block w-full cursor-pointer px-3 py-1.5 text-center text-xs font-medium text-gold-500 hover:bg-clay-800 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:cursor-default disabled:opacity-60"
				disabled={loading}
				onmousedown={(e) => {
					e.preventDefault();
					loadMore();
				}}
			>
				{loading
					? 'Loading…'
					: `Load more (${formatNumber(displayed.length)} of ${formatNumber(total)})`}
			</button>
		{/if}
	</div>
{/if}
