<script>
	import { onMount } from 'svelte';
	import { api } from '$lib/api';

	let { params } = $props();

	let doc = $state();
	let error = $state('');

	onMount(async () => {
		doc = await api.documents.get(params.id);
		if (!doc) error = 'Failed to load document';
	});
</script>

<div class="space-y-6">
	<a
		href="/documents"
		class="inline-flex items-center gap-1 text-sm text-parchment-500 hover:text-parchment-200"
	>
		&larr; Back to documents
	</a>

	{#if error}
		<p class="text-sm text-terracotta-500">{error}</p>
	{:else if !doc}
		<p class="text-parchment-500">Loading…</p>
	{:else}
		<div class="flex items-start gap-6">
			<div class="min-w-0 flex-1">
				<h1 class="text-2xl font-semibold text-parchment-200">{doc.title}</h1>

				{#if doc.mime_type === 'application/pdf'}
					<div class="mt-4 overflow-hidden rounded-lg border border-clay-800">
						<iframe
							src={`/api/v1/documents/${doc.id}/file`}
							class="h-[75vh] w-full"
							title={doc.title}
						></iframe>
					</div>
				{:else}
					<p class="mt-4 text-parchment-500">Preview not available for this file type.</p>
				{/if}
			</div>

			<div class="w-80 shrink-0 space-y-4">
				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<p class="text-xs font-medium tracking-wider text-parchment-500 uppercase">MIME Type</p>
					<p class="mt-1 text-parchment-200">{doc.mime_type}</p>
				</div>
				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<p class="text-xs font-medium tracking-wider text-parchment-500 uppercase">File Size</p>
					<p class="mt-1 text-parchment-200">{(doc.file_size / 1024).toFixed(0)} KB</p>
				</div>
				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<p class="text-xs font-medium tracking-wider text-parchment-500 uppercase">
						Content Stats
					</p>
					<div class="mt-2 space-y-1 text-sm">
						<div class="flex justify-between">
							<span class="text-parchment-500">Pages</span>
							<span class="text-parchment-200">{doc.page_count ?? '—'}</span>
						</div>
						<div class="flex justify-between">
							<span class="text-parchment-500">Words</span>
							<span class="text-parchment-200">{doc.word_count ?? '—'}</span>
						</div>
						<div class="flex justify-between">
							<span class="text-parchment-500">Characters</span>
							<span class="text-parchment-200">{doc.char_count ?? '—'}</span>
						</div>
					</div>
				</div>
				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<p class="text-xs font-medium tracking-wider text-parchment-500 uppercase">Created</p>
					<p class="mt-1 text-parchment-200">{new Date(doc.created_at).toLocaleString()}</p>
				</div>
				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<p class="text-xs font-medium tracking-wider text-parchment-500 uppercase">Modified</p>
					<p class="mt-1 text-parchment-200">{new Date(doc.modified_at).toLocaleString()}</p>
				</div>
				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<p class="text-xs font-medium tracking-wider text-parchment-500 uppercase">
						MD5 Checksum
					</p>
					<p class="mt-1 font-mono text-xs break-all text-parchment-400">{doc.md5_checksum}</p>
				</div>
				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<p class="text-xs font-medium tracking-wider text-parchment-500 uppercase">
						SHA‑512 Checksum
					</p>
					<p class="mt-1 font-mono text-xs break-all text-parchment-400">{doc.sha512_checksum}</p>
				</div>
			</div>
		</div>
	{/if}
</div>
