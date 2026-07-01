<script>
	import Modal from './Modal.svelte';
	import { api } from '$lib/api.js';
	import { goto } from '$app/navigation';

	let { open, onClose } = $props();

	let files = $state([]);
	let uploading = $state(false);
	let result = $state(null);
	let error = $state(null);

	let fileInput = $state(null);

	function reset() {
		files = [];
		uploading = false;
		result = null;
		error = null;
	}

	function handleClose() {
		reset();
		onClose();
	}

	function handleFileSelect(e) {
		files = Array.from(e.currentTarget.files ?? []);
		result = null;
		error = null;
	}

	function removeFile(index) {
		files = files.filter((_, i) => i !== index);
	}

	async function handleUpload() {
		if (files.length === 0) return;
		uploading = true;
		error = null;
		result = null;

		const res = await api.consume.upload(files);
		if (res.ok && res.status === 202) {
			result = res.data;
		} else if (res.status === 413) {
			error = res.data?.error ?? 'Upload too large';
		} else if (res.status === 422) {
			error = res.data?.error ?? 'Upload rejected';
			if (res.data?.missing_tools?.length) {
				error += ` — ${res.data.missing_tools.length} missing tool(s). Review your settings.`;
			}
		} else if (res.status === 0) {
			error = 'Network error — check that the server is running.';
		} else {
			error = `Unexpected error (${res.status})`;
		}
		uploading = false;
	}
</script>

<Modal {open} title="Upload documents" onClose={handleClose}>
	{#if result}
		<div class="space-y-4">
			<p class="text-parchment-200">{result.accepted} file(s) queued</p>
			{#if result.rejected?.length}
				<div
					class="rounded-lg border border-terracotta-600 bg-terracotta-500/10 p-3 text-sm text-terracotta-500"
				>
					<p class="font-medium">Rejected files:</p>
					<ul class="mt-1 list-inside list-disc">
						{#each result.rejected as r}
							<li>{r.name} — {r.reason}</li>
						{/each}
					</ul>
				</div>
			{/if}
			<div class="flex gap-2">
				<a
					href="/tasks?batch={result.batch_id}"
					class="inline-block rounded-lg bg-gold-500 px-4 py-2 text-sm font-medium text-clay-950 hover:bg-gold-600"
					>View tasks</a
				>
				<button
					onclick={handleClose}
					class="rounded-lg border border-clay-800 px-4 py-2 text-sm text-parchment-400 hover:bg-clay-800 hover:text-parchment-200"
					>Close</button
				>
			</div>
		</div>
	{:else if error}
		<div class="space-y-4">
			<div
				class="rounded-lg border border-terracotta-600 bg-terracotta-500/10 p-3 text-sm text-terracotta-500"
			>
				{error}
			</div>
			{#if error.includes('missing tool')}
				<a
					href="/settings"
					class="inline-block rounded-lg bg-gold-500 px-4 py-2 text-sm font-medium text-clay-950 hover:bg-gold-600"
					>Review settings</a
				>
			{/if}
			<button
				onclick={handleClose}
				class="rounded-lg border border-clay-800 px-4 py-2 text-sm text-parchment-400 hover:bg-clay-800 hover:text-parchment-200"
				>Close</button
			>
		</div>
	{:else}
		<div class="space-y-4">
			<input
				type="file"
				multiple
				accept=".pdf"
				onchange={handleFileSelect}
				bind:this={fileInput}
				class="hidden"
			/>
			{#if files.length === 0}
				<button
					onclick={() => fileInput?.click()}
					class="w-full rounded-lg border-2 border-dashed border-clay-700 px-4 py-8 text-sm text-parchment-400 hover:border-clay-600 hover:text-parchment-200"
					>Choose files</button
				>
			{:else}
				<ul class="max-h-40 space-y-1 overflow-y-auto">
					{#each files as file, i (file.name + i)}
						<li
							class="flex items-center justify-between rounded bg-clay-900 px-3 py-2 text-sm text-parchment-300"
						>
							<span class="truncate">{file.name}</span>
							<button
								onclick={() => removeFile(i)}
								class="ml-2 shrink-0 text-parchment-500 hover:text-parchment-200">&times;</button
							>
						</li>
					{/each}
				</ul>
				<div class="flex gap-2">
					<button
						onclick={() => fileInput?.click()}
						class="rounded-lg border border-clay-800 px-4 py-2 text-sm text-parchment-400 hover:bg-clay-800 hover:text-parchment-200"
						>Add more</button
					>
					<button
						onclick={() => {
							files = [];
							fileInput.value = '';
						}}
						class="rounded-lg border border-clay-800 px-4 py-2 text-sm text-parchment-400 hover:bg-clay-800 hover:text-parchment-200"
						>Clear</button
					>
				</div>
			{/if}
			<button
				onclick={handleUpload}
				disabled={files.length === 0 || uploading}
				class="w-full rounded-lg bg-gold-500 px-4 py-2 text-sm font-medium text-clay-950 hover:bg-gold-600 disabled:opacity-50"
				>{uploading ? 'Uploading...' : 'Upload'}</button
			>
		</div>
	{/if}
</Modal>
