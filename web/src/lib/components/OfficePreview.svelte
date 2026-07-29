<script>
	import { onMount } from 'svelte';

	let { docId, mimeType, officeFormat } = $props();

	let state = $state('loading');
	let htmlContent = $state('');
	let errorMessage = $state('');

	onMount(async () => {
		try {
			const resp = await fetch(`/api/v1/documents/${docId}/file`);
			if (!resp.ok) {
				errorMessage = `Failed to fetch file (${resp.status} ${resp.statusText})`;
				state = 'error';
				return;
			}

			const contentLength = parseInt(resp.headers.get('Content-Length') ?? '0', 10);
			if (contentLength > 20 * 1024 * 1024) {
				errorMessage = `File is ${new Intl.NumberFormat('en-US', { maximumFractionDigits: 1 }).format(contentLength / 1024 / 1024)} MB, which may be too large for preview. Download the file instead.`;
				state = 'error';
				return;
			}

			const buffer = await resp.arrayBuffer();
			const fileType = officeFormat;
			if (!fileType) {
				errorMessage = `Unsupported MIME type: ${mimeType}`;
				state = 'error';
				return;
			}

			const { OfficeParser } = await import('officeparser');
			const ast = await OfficeParser.parseOffice(buffer, {
				fileType,
				extractAttachments: false
			});

			const { value } = await ast.to('html', {
				htmlConfig: {
					standalone: { document: false, styles: 'scoped' }
				}
			});

			const html = String(value ?? '');
			if (!html.trim()) {
				state = 'empty';
				return;
			}

			htmlContent = html;
			state = 'success';
		} catch (err) {
			errorMessage = err instanceof Error ? err.message : 'An unexpected error occurred';
			state = 'error';
		}
	});
</script>

<div class="overflow-auto rounded-lg border border-clay-800 bg-white">
	{#if state === 'loading'}
		<div aria-live="polite" class="flex items-center justify-center gap-2 p-8 text-parchment-500">
			<span>Rendering preview…</span>
		</div>
	{:else if state === 'error'}
		<div class="p-6">
			<div
				class="rounded-lg border border-terracotta-600 bg-terracotta-500/10 p-4 text-sm text-terracotta-500"
			>
				<p class="font-medium">Preview failed</p>
				<p class="mt-1">{errorMessage}</p>
				<a
					href={`/api/v1/documents/${docId}/file?download=true`}
					class="mt-3 inline-block rounded-md bg-gold-600 px-3 py-1.5 text-xs font-medium text-clay-950 hover:bg-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
				>
					Download file instead
				</a>
			</div>
		</div>
	{:else if state === 'empty'}
		<div class="p-8 text-center text-parchment-500">This document appears to be empty.</div>
	{:else if state === 'success'}
		<div class="office-preview">
			{@html htmlContent}
		</div>
	{/if}
</div>
