<script>
	import { TextLayer } from '$lib/pdf/pdfjs';

	let { pdfPage, placeholderRatio, scale, active, register, onTextLayerReady } = $props();

	let viewport = $derived(
		pdfPage
			? pdfPage.getViewport({ scale })
			: { width: placeholderRatio.width * scale, height: placeholderRatio.height * scale }
	);
	let dpr = $derived(Math.min(window.devicePixelRatio || 1, 2));

	let canvasEl = $state(null);
	let textLayerEl = $state(null);
	let docKey = $state(0);
	let lastPage = null;
	let textLayer = null;
	let textLayerReady = false;

	$effect(() => {
		if (pdfPage !== lastPage) {
			lastPage = pdfPage;
			docKey++;
		}
	});

	$effect(() => {
		if (!active || !pdfPage || !canvasEl || !textLayerEl) return;
		const vp = viewport;
		const ctx = canvasEl.getContext('2d', { alpha: false });
		const task = pdfPage.render({
			canvasContext: ctx,
			viewport: vp,
			transform: dpr !== 1 ? [dpr, 0, 0, dpr, 0, 0] : null
		});
		let cancelled = false;
		task.promise
			.then(() => {
				if (cancelled) return;
				textLayer = new TextLayer({
					textContentSource: pdfPage.streamTextContent({
						includeMarkedContent: true,
						disableNormalization: true
					}),
					container: textLayerEl,
					viewport: vp
				});
				return textLayer.render();
			})
			.then(() => {
				if (cancelled) return;
				textLayerReady = true;
				onTextLayerReady?.(pdfPage.pageNumber);
			})
			.catch((err) => {
				if (cancelled) return;
				if (err?.name !== 'RenderingCancelledException') {
					console.error(`PDF page ${pdfPage.pageNumber} render failed:`, err);
				}
			});
		return () => {
			cancelled = true;
			task.cancel();
			textLayer?.cancel();
			textLayer = null;
			textLayerReady = false;
		};
	});

	$effect(() => {
		if (!pdfPage) return;
		register({
			get textDivs() {
				return textLayerReady ? textLayer.textDivs : [];
			},
			applyHighlights: (indexes) => {
				if (!textLayerReady) return;
				for (const div of textLayer.textDivs) {
					div.classList.remove('pdf-find-highlight', 'pdf-find-current');
				}
				for (const i of indexes) {
					textLayer.textDivs[i]?.classList.add('pdf-find-highlight');
				}
			},
			markCurrent: (itemIndex) => {
				if (!textLayerReady) return;
				for (const div of textLayer.textDivs) {
					div.classList.remove('pdf-find-current');
				}
				textLayer.textDivs[itemIndex]?.classList.add('pdf-find-current');
			}
		});
	});
</script>

<div
	class="pdf-page"
	style="width: {Math.floor(viewport.width)}px; height: {Math.floor(
		viewport.height
	)}px; --scale-factor: {scale}; --user-unit: {viewport.userUnit ?? 1}"
>
	<canvas
		bind:this={canvasEl}
		class="block"
		role={pdfPage ? 'img' : undefined}
		aria-label={pdfPage ? `Page ${pdfPage.pageNumber}` : undefined}
		aria-hidden={pdfPage && active ? undefined : true}
		width={active ? Math.floor(viewport.width * dpr) : 0}
		height={active ? Math.floor(viewport.height * dpr) : 0}
		style="width: {active ? Math.floor(viewport.width) : 0}px; height: {active
			? Math.floor(viewport.height)
			: 0}px"
	></canvas>
	{#if active}
		{#key `${scale}-${docKey}`}
			<div class="textLayer" bind:this={textLayerEl}></div>
		{/key}
	{/if}
</div>
