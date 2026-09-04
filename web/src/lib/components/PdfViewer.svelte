<script>
	import { onDestroy } from 'svelte';
	import { SvelteSet } from 'svelte/reactivity';
	import { resolve } from '$app/paths';
	import { getDocument } from '$lib/pdf/pdfjs';
	import Icon from './Icon.svelte';
	import PdfPage from './PdfPage.svelte';
	import './pdf-viewer.css';

	let { url, title, actions, rootClass = '', scrollClass = 'h-[75vh] overflow-y-auto' } = $props();

	let pdfDoc = $state(null);
	let pages = $state({});
	let numPages = $state(0);
	let loading = $state(true);
	let error = $state(null);

	let scale = $state(1);
	let fitScale = $state(1);
	let zoomMode = $state('fit');
	let currentPage = $state(1);
	let pageInput = $state('1');
	let pageInputFocused = $state(false);
	let page1Width = $state(1);
	let placeholderRatio = $state({ width: 1, height: 1 });

	let findOpen = $state(false);
	let findQuery = $state('');
	let findMatches = $state([]);
	let currentMatch = $state(-1);

	let renderZone = new SvelteSet();
	let keepRendered = new SvelteSet();
	let inRenderZone = new SvelteSet();
	let inDestroyZone = new SvelteSet();
	let wasRendered = new SvelteSet();
	let pendingZone = new SvelteSet();
	let zoneFlushScheduled = false;
	let scrollEl = $state(null);
	let pageEls = $state([]);
	let pageOffsets = [];
	let offsetsDirty = true;
	let pageApis = {};
	let textCache = {};
	let pendingScrollTo = null;
	let loadingTask = null;
	let findTimer = null;
	let findSeq = 0;
	let scrollRaf = 0;

	let pageNumbers = $derived(Array.from({ length: numPages }, (_, i) => i + 1));

	$effect(() => {
		if (!pageInputFocused) pageInput = String(currentPage);
	});

	$effect(() => {
		const u = url;
		loadDoc(u);
		return () => {
			loadingTask?.destroy();
			loadingTask = null;
		};
	});

	async function loadDoc(u) {
		loading = true;
		error = null;
		pdfDoc = null;
		pages = {};
		numPages = 0;
		pageEls = [];
		pageApis = {};
		textCache = {};
		renderZone.clear();
		keepRendered.clear();
		inRenderZone.clear();
		inDestroyZone.clear();
		wasRendered.clear();
		pendingZone.clear();
		findMatches = [];
		currentMatch = -1;
		pendingScrollTo = null;
		findSeq++;
		loadingTask?.destroy();
		const task = getDocument({ url: u });
		loadingTask = task;
		try {
			const doc = await task.promise;
			if (task.destroyed) return;
			pdfDoc = doc;
			numPages = doc.numPages;
			const page1 = await doc.getPage(1);
			if (task.destroyed) return;
			pages[1] = page1;
			const vp1 = page1.getViewport({ scale: 1 });
			page1Width = vp1.width;
			placeholderRatio = { width: vp1.width, height: vp1.height };
			computeFitScale();
			loading = false;
		} catch (err) {
			if (task.destroyed) return;
			error = err?.message || String(err);
			loading = false;
		}
	}

	async function fetchPage(n) {
		const doc = pdfDoc;
		if (!doc || pages[n]) return pages[n] ?? null;
		const page = await doc.getPage(n);
		if (doc !== pdfDoc) return null;
		pages[n] = page;
		offsetsDirty = true;
		return page;
	}

	async function getPageText(n) {
		if (!textCache[n]) {
			const page = pages[n] ?? (await fetchPage(n));
			if (!page) return null;
			textCache[n] = await page.getTextContent();
		}
		return textCache[n];
	}

	function computeFitScale() {
		if (!scrollEl || !page1Width) return;
		fitScale = Math.min(4, Math.max(0.5, scrollEl.clientWidth / page1Width));
		if (zoomMode === 'fit') scale = fitScale;
		offsetsDirty = true;
	}

	function setScale(s) {
		scale = Math.min(4, Math.max(0.5, s));
		zoomMode = 'manual';
		offsetsDirty = true;
	}

	function zoomIn() {
		setScale(scale + 0.25);
	}

	function zoomOut() {
		setScale(scale - 0.25);
	}

	function fitWidth() {
		scale = fitScale;
		zoomMode = 'fit';
		offsetsDirty = true;
	}

	function goToPage(n) {
		if (n < 1 || n > numPages) return;
		scrollToPage(n);
	}

	function jumpToPageInput() {
		const n = parseInt(pageInput, 10);
		if (Number.isNaN(n) || n < 1) {
			pageInput = String(currentPage);
			return;
		}
		const clamped = Math.min(numPages, n);
		pageInput = String(clamped);
		goToPage(clamped);
	}

	function onPageInputKeydown(e) {
		if (e.key === 'Enter') {
			e.preventDefault();
			jumpToPageInput();
		}
	}

	function scrollToPage(n) {
		if (offsetsDirty) measurePageOffsets();
		const top = pageOffsets[n - 1];
		if (top === undefined) return;
		scrollEl?.scrollTo({ top });
	}

	function scrollSpanIntoView(pageNumber, itemIndex) {
		const span = pageApis[pageNumber]?.textDivs[itemIndex];
		const el = scrollEl;
		if (!span || !el) return;
		const spanTop =
			span.getBoundingClientRect().top - el.getBoundingClientRect().top + el.scrollTop;
		el.scrollTo({ top: spanTop - el.clientHeight / 2 });
	}

	function updateCurrentPage() {
		const el = scrollEl;
		if (!el) return;
		if (offsetsDirty) measurePageOffsets();
		if (el.scrollTop + el.clientHeight >= el.scrollHeight - 2) {
			currentPage = numPages;
			return;
		}
		const threshold = el.scrollTop + el.clientHeight * 0.3;
		let n = 1;
		for (let i = 0; i < pageOffsets.length; i++) {
			if (pageOffsets[i] > threshold) break;
			n = i + 1;
		}
		currentPage = n;
	}

	function measurePageOffsets() {
		pageOffsets = pageEls.map((el) => el.offsetTop);
		offsetsDirty = false;
	}

	function onScroll() {
		if (scrollRaf) return;
		scrollRaf = requestAnimationFrame(() => {
			scrollRaf = 0;
			updateCurrentPage();
		});
	}

	$effect(() => {
		const el = scrollEl;
		if (!el) return;
		const ro = new ResizeObserver(() => {
			computeFitScale();
			offsetsDirty = true;
		});
		ro.observe(el);
		return () => ro.disconnect();
	});

	$effect(() => {
		if (!pdfDoc || pageEls.length === 0) return;
		offsetsDirty = true;
	});

	$effect(() => {
		if (!pdfDoc || !scrollEl || pageEls.length === 0) return;
		const renderObs = new IntersectionObserver(onRenderObs, {
			root: scrollEl,
			rootMargin: '200% 0px'
		});
		const destroyObs = new IntersectionObserver(onDestroyObs, {
			root: scrollEl,
			rootMargin: '600% 0px'
		});
		for (const el of pageEls) {
			renderObs.observe(el);
			destroyObs.observe(el);
		}
		return () => {
			renderObs.disconnect();
			destroyObs.disconnect();
		};
	});

	function onRenderObs(entries) {
		for (const entry of entries) {
			const n = Number(entry.target.dataset.page);
			if (entry.isIntersecting) inRenderZone.add(n);
			else inRenderZone.delete(n);
			pendingZone.add(n);
		}
		scheduleZoneFlush();
	}

	function onDestroyObs(entries) {
		for (const entry of entries) {
			const n = Number(entry.target.dataset.page);
			if (entry.isIntersecting) inDestroyZone.add(n);
			else inDestroyZone.delete(n);
			pendingZone.add(n);
		}
		scheduleZoneFlush();
	}

	function scheduleZoneFlush() {
		if (zoneFlushScheduled) return;
		zoneFlushScheduled = true;
		queueMicrotask(() => {
			zoneFlushScheduled = false;
			flushZone();
		});
	}

	function flushZone() {
		if (pendingZone.size === 0) return;
		for (const n of pendingZone) {
			reconcilePage(n, inRenderZone.has(n), inDestroyZone.has(n));
		}
		pendingZone.clear();
	}

	function reconcilePage(n, render, destroy) {
		if (render) {
			renderZone.add(n);
			if (!pages[n]) fetchPage(n);
		} else {
			renderZone.delete(n);
		}
		if (destroy) {
			if (render || wasRendered.has(n)) keepRendered.add(n);
		} else {
			keepRendered.delete(n);
		}
		if (renderZone.has(n) || keepRendered.has(n)) wasRendered.add(n);
		else wasRendered.delete(n);
	}

	$effect(() => {
		const el = scrollEl;
		if (!el) return;
		const handler = (e) => {
			if (!e.ctrlKey) return;
			e.preventDefault();
			setScale(scale + (e.deltaY < 0 ? 0.25 : -0.25));
		};
		el.addEventListener('wheel', handler, { passive: false });
		return () => el.removeEventListener('wheel', handler);
	});

	function onFindInput() {
		clearTimeout(findTimer);
		findTimer = setTimeout(runFind, 250);
	}

	async function runFind() {
		const seq = ++findSeq;
		const q = findQuery.trim().toLowerCase();
		if (!q) {
			findMatches = [];
			currentMatch = -1;
			pendingScrollTo = null;
			clearAllHighlights();
			return;
		}
		const matches = [];
		for (let n = 1; n <= numPages; n++) {
			const tc = await getPageText(n);
			if (seq !== findSeq) return;
			if (!tc) continue;
			tc.items.forEach((item, i) => {
				if (item.str && item.str.toLowerCase().includes(q)) {
					matches.push({ pageNumber: n, itemIndex: i });
				}
			});
		}
		if (seq !== findSeq) return;
		findMatches = matches;
		applyAllHighlights();
		currentMatch = matches.length > 0 ? 0 : -1;
		if (matches.length > 0) goToMatch(0);
	}

	function applyAllHighlights() {
		for (let n = 1; n <= numPages; n++) {
			const indexes = findMatches.filter((m) => m.pageNumber === n).map((m) => m.itemIndex);
			pageApis[n]?.applyHighlights(indexes);
		}
		markCurrentSpan();
	}

	function markCurrentSpan() {
		for (let n = 1; n <= numPages; n++) pageApis[n]?.markCurrent(-1);
		const m = findMatches[currentMatch];
		if (!m) return;
		pageApis[m.pageNumber]?.markCurrent(m.itemIndex);
	}

	function clearAllHighlights() {
		for (let n = 1; n <= numPages; n++) pageApis[n]?.applyHighlights([]);
	}

	function goToMatch(i) {
		const len = findMatches.length;
		if (len === 0) return;
		currentMatch = ((i % len) + len) % len;
		const m = findMatches[currentMatch];
		scrollToPage(m.pageNumber);
		markCurrentSpan();
		const api = pageApis[m.pageNumber];
		if (api && api.textDivs.length > 0) {
			scrollSpanIntoView(m.pageNumber, m.itemIndex);
		} else {
			pendingScrollTo = m;
		}
	}

	function onTextLayerReady(pageNumber) {
		const indexes = findMatches.filter((m) => m.pageNumber === pageNumber).map((m) => m.itemIndex);
		pageApis[pageNumber]?.applyHighlights(indexes);
		markCurrentSpan();
		const p = pendingScrollTo;
		if (p && p.pageNumber === pageNumber) {
			pendingScrollTo = null;
			scrollSpanIntoView(p.pageNumber, p.itemIndex);
		}
	}

	onDestroy(() => {
		clearTimeout(findTimer);
		cancelAnimationFrame(scrollRaf);
	});
</script>

<div
	class="flex flex-col overflow-hidden rounded-lg border border-clay-800 bg-clay-950 {rootClass}"
>
	<div class="flex flex-wrap items-center gap-2 border-b border-clay-800 bg-clay-900 px-3 py-2">
		<div class="flex items-center gap-1">
			<button
				type="button"
				onclick={zoomOut}
				aria-label="Zoom out"
				title="Zoom out"
				class="rounded-md p-1.5 text-parchment-300 hover:bg-clay-800 hover:text-parchment-100 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
				><Icon name="minus" /></button
			>
			<button
				type="button"
				onclick={fitWidth}
				aria-label="Fit width"
				title="Fit width"
				class="rounded-md px-2 py-1 text-sm text-parchment-300 hover:bg-clay-800 hover:text-parchment-100 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
				>Fit</button
			>
			<button
				type="button"
				onclick={zoomIn}
				aria-label="Zoom in"
				title="Zoom in"
				class="rounded-md p-1.5 text-parchment-300 hover:bg-clay-800 hover:text-parchment-100 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
				><Icon name="plus" /></button
			>
			<span class="min-w-10 text-center text-xs text-parchment-500 tabular-nums"
				>{Math.round(scale * 100)}%</span
			>
		</div>
		<span class="mx-1 h-5 w-px bg-clay-700" aria-hidden="true"></span>
		<div class="flex items-center gap-1 text-sm text-parchment-400">
			<button
				type="button"
				onclick={() => goToPage(currentPage - 1)}
				disabled={currentPage <= 1}
				aria-label="Previous page"
				title="Previous page"
				class="rounded-md p-1.5 text-parchment-300 hover:bg-clay-800 hover:text-parchment-100 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-40"
				><Icon name="chevron-left" /></button
			>
			<input
				type="text"
				inputmode="numeric"
				pattern="[0-9]*"
				autocomplete="off"
				aria-label="Page number"
				title="Jump to page"
				bind:value={pageInput}
				onfocus={(e) => {
					pageInputFocused = true;
					e.currentTarget.select();
				}}
				onblur={() => (pageInputFocused = false)}
				onkeydown={onPageInputKeydown}
				class="w-20 rounded-md border border-clay-700 bg-clay-950 px-1 py-0.5 text-center text-sm text-parchment-200 tabular-nums focus:border-gold-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-gold-500"
			/>
			<span class="tabular-nums"> / {numPages}</span>
			<button
				type="button"
				onclick={() => goToPage(currentPage + 1)}
				disabled={currentPage >= numPages}
				aria-label="Next page"
				title="Next page"
				class="rounded-md p-1.5 text-parchment-300 hover:bg-clay-800 hover:text-parchment-100 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-40"
				><Icon name="chevron-right" /></button
			>
		</div>
		<div class="flex-1"></div>
		{@render actions?.()}
		<span class="mx-1 h-5 w-px bg-clay-700" aria-hidden="true"></span>
		<button
			type="button"
			onclick={() => (findOpen = !findOpen)}
			aria-label="Find in document"
			title="Find in document"
			aria-expanded={findOpen}
			aria-pressed={findOpen}
			class="rounded-md p-1.5 text-parchment-300 hover:bg-clay-800 hover:text-parchment-100 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
			><Icon name="search" /></button
		>
	</div>
	{#if findOpen}
		<div class="flex items-center gap-2 bg-clay-900 px-3 py-2">
			<input
				type="text"
				bind:value={findQuery}
				oninput={onFindInput}
				onkeydown={(e) => {
					if (e.key === 'Escape') {
						e.preventDefault();
						findOpen = false;
						findQuery = '';
						runFind();
					}
				}}
				aria-label="Find text"
				placeholder="Find in document…"
				autocomplete="off"
				class="w-48 rounded-md border border-clay-700 bg-clay-950 px-2 py-1 text-sm text-parchment-200 placeholder-parchment-600 focus:border-gold-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-gold-500"
			/>
			<span
				class="text-xs text-parchment-400 tabular-nums"
				aria-label={findMatches.length > 0
					? `Match ${currentMatch + 1} of ${findMatches.length}`
					: findQuery.trim()
						? 'No matches'
						: undefined}
			>
				{findMatches.length > 0
					? `${currentMatch + 1} / ${findMatches.length}`
					: findQuery.trim()
						? 'No matches'
						: ''}
			</span>
			<button
				type="button"
				onclick={() => goToMatch(currentMatch - 1)}
				disabled={findMatches.length === 0}
				aria-label="Previous match"
				title="Previous match"
				class="rounded-md p-1.5 text-parchment-300 hover:bg-clay-800 hover:text-parchment-100 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-40"
				><Icon name="chevron-left" /></button
			>
			<button
				type="button"
				onclick={() => goToMatch(currentMatch + 1)}
				disabled={findMatches.length === 0}
				aria-label="Next match"
				title="Next match"
				class="rounded-md p-1.5 text-parchment-300 hover:bg-clay-800 hover:text-parchment-100 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-40"
				><Icon name="chevron-right" /></button
			>
		</div>
	{/if}
	<div
		bind:this={scrollEl}
		onscroll={onScroll}
		class="relative {scrollClass} bg-clay-950 p-4"
		role="region"
		aria-label={title ? `PDF viewer: ${title}` : 'PDF viewer'}
	>
		{#if loading}
			<div class="flex h-full items-center justify-center">
				<div
					aria-hidden="true"
					class="h-8 w-8 animate-spin rounded-full border-2 border-clay-800 border-t-gold-500 motion-reduce:animate-none"
				></div>
			</div>
		{:else if error}
			<div class="flex h-full items-center justify-center">
				<div
					class="max-w-md rounded-lg border border-terracotta-600 bg-terracotta-800/30 p-4 text-center"
				>
					<p class="text-sm font-medium text-terracotta-400">Failed to load PDF</p>
					<p class="mt-1 text-xs break-all text-parchment-500">{error}</p>
					<a
						href={resolve(`${url}?download=true`)}
						class="mt-3 inline-block rounded-md bg-gold-600 px-3 py-1.5 text-xs font-medium text-clay-950 hover:bg-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
						>Download PDF</a
					>
				</div>
			</div>
		{:else if pdfDoc}
			{#each pageNumbers as n (n)}
				<div bind:this={pageEls[n - 1]} data-page={n}>
					<PdfPage
						pdfPage={pages[n] ?? null}
						{placeholderRatio}
						{scale}
						active={renderZone.has(n) || keepRendered.has(n)}
						register={(api) => (pageApis[n] = api)}
						{onTextLayerReady}
					/>
				</div>
			{/each}
		{/if}
	</div>
</div>
