<script>
	import { onMount } from 'svelte';
	import { api } from '$lib/api';
	import { confirmStore } from '$lib/stores/confirmStore.svelte.js';
	import { toastStore } from '$lib/stores/toastStore.svelte.js';
	import { page } from '$app/stores';
	import { goto } from '$app/navigation';
	import * as authStore from '$lib/stores/authStore.js';

	let activeTab = $state($page.url.searchParams.get('tab') || 'orphaned');

	let files = $state([]);
	let loading = $state(true);
	let scanning = $state(false);

	let erroredFiles = $state([]);
	let erroredLoading = $state(true);

	onMount(() => {
		loadFiles();
		loadErrored();
	});

	function switchTab(tab) {
		activeTab = tab;
		goto(`/documents/orphaned?tab=${tab}`, { replaceState: true, keepFocus: true });
	}

	async function loadFiles() {
		loading = true;
		files = await api.orphaned.list();
		loading = false;
	}

	async function loadErrored() {
		erroredLoading = true;
		erroredFiles = await api.errored.list();
		erroredLoading = false;
	}

	async function scan() {
		scanning = true;
		const res = await api.orphaned.scan();
		scanning = false;
		if (res?.ok) {
			toastStore.success(`Scan complete: ${res.data?.quarantined ?? 0} file(s) quarantined`);
			loadFiles();
		} else {
			toastStore.error('Scan failed — check server logs');
		}
	}

	async function handleDelete(id) {
		const ok = await confirmStore.confirm({
			title: 'Delete orphaned file',
			message: 'Permanently delete this orphaned file?',
			danger: true
		});
		if (!ok) return;
		const res = await api.orphaned.delete(id);
		if (res?.ok || res?.status === 204) {
			toastStore.success('File deleted');
			loadFiles();
		} else {
			toastStore.error('Delete failed — try again');
		}
	}

	async function handleRestore(id) {
		const ok = await confirmStore.confirm({
			title: 'Restore orphaned file',
			message: 'Restore this file by creating a consume task?'
		});
		if (!ok) return;
		const res = await api.orphaned.restore(id);
		if (res?.ok || res?.status === 202) {
			toastStore.success('Restore task created');
			loadFiles();
		} else {
			toastStore.error('Restore failed — try again');
		}
	}

	async function handleMoveToInbox(id) {
		const ok = await confirmStore.confirm({
			title: 'Move to inbox',
			message: 'Move this file back to the consumption inbox?'
		});
		if (!ok) return;
		const res = await api.orphaned.moveToInbox(id);
		if (res?.ok || res?.status === 202) {
			toastStore.success('File moved to inbox');
			loadFiles();
		} else {
			toastStore.error('Move failed — try again');
		}
	}

	async function handleDeleteAll() {
		const ok = await confirmStore.confirm({
			title: 'Delete all',
			message: 'Delete all pending orphaned files? This cannot be undone.',
			danger: true
		});
		if (!ok) return;
		const res = await api.orphaned.deleteAll();
		if (res?.ok) {
			toastStore.success(`${res.data?.deleted ?? 0} file(s) deleted`);
			loadFiles();
		} else {
			toastStore.error('Delete all failed — try again');
		}
	}

	async function handleMoveAllToInbox() {
		const ok = await confirmStore.confirm({
			title: 'Move all to inbox',
			message: 'Move all pending orphaned files to the consumption inbox?'
		});
		if (!ok) return;
		const res = await api.orphaned.moveAllToInbox();
		if (res?.ok) {
			toastStore.success(`${res.data?.moved ?? 0} file(s) moved to inbox`);
			loadFiles();
		} else {
			toastStore.error('Move all failed — try again');
		}
	}

	async function handleErroredDelete(subdir, file) {
		const ok = await confirmStore.confirm({
			title: 'Delete errored file',
			message: 'Permanently delete this errored file?',
			danger: true
		});
		if (!ok) return;
		const res = await api.errored.delete(subdir, file);
		if (res?.ok || res?.status === 204) {
			toastStore.success('File deleted');
			loadErrored();
		} else {
			toastStore.error('Delete failed — try again');
		}
	}

	async function handleErroredDeleteAll() {
		const ok = await confirmStore.confirm({
			title: 'Delete all errored files',
			message: 'Delete all errored files? This cannot be undone.',
			danger: true
		});
		if (!ok) return;
		const res = await api.errored.deleteAll();
		if (res?.ok) {
			toastStore.success(`${res.data?.deleted ?? 0} file(s) deleted`);
			loadErrored();
		} else {
			toastStore.error('Delete all failed — try again');
		}
	}

	function formatSize(bytes) {
		if (bytes < 1024) return `${bytes} B`;
		if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(0)} KB`;
		return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
	}

	function formatTime(t) {
		if (!t) return '—';
		return new Date(t).toLocaleString();
	}
</script>

{#if authStore.authEnabled() && !authStore.isEditor()}
	<p class="text-parchment-500">You do not have permission to view this page.</p>
{:else}
	<div class="space-y-4">
		<div class="flex items-center gap-4 border-b border-clay-800">
			<button
				class="px-1 pb-2 text-sm font-medium transition-colors focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:ring-offset-2 focus-visible:ring-offset-clay-950 {activeTab ===
				'orphaned'
					? 'border-b-2 border-gold-500 text-parchment-200'
					: 'text-parchment-500 hover:text-parchment-200'}"
				onclick={() => switchTab('orphaned')}
			>
				Orphaned
			</button>
			<button
				class="px-1 pb-2 text-sm font-medium transition-colors focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:ring-offset-2 focus-visible:ring-offset-clay-950 {activeTab ===
				'errored'
					? 'border-b-2 border-gold-500 text-parchment-200'
					: 'text-parchment-500 hover:text-parchment-200'}"
				onclick={() => switchTab('errored')}
			>
				Errored {#if erroredFiles.length > 0}({erroredFiles.length}){/if}
			</button>
		</div>

		{#if activeTab === 'orphaned'}
			<div class="space-y-4">
				<div class="flex items-center justify-between">
					<h1 class="text-2xl font-semibold text-parchment-200">Orphaned Files</h1>
					<div class="flex gap-2">
						<button
							onclick={scan}
							disabled={scanning}
							class="rounded-lg bg-gold-500 px-4 py-2 text-sm font-medium text-clay-950 hover:bg-gold-600 disabled:opacity-50"
						>
							{scanning ? 'Scanning…' : 'Scan Now'}
						</button>
					</div>
				</div>

				{#if files.length > 0}
					<div class="flex gap-2">
						<button
							onclick={handleDeleteAll}
							class="rounded-lg bg-terracotta-700 px-3 py-1.5 text-xs font-medium text-parchment-200 hover:bg-terracotta-600"
						>
							Delete All
						</button>
						<button
							onclick={handleMoveAllToInbox}
							class="rounded-lg border border-clay-800 px-3 py-1.5 text-xs font-medium text-parchment-400 hover:bg-clay-800 hover:text-parchment-200"
						>
							Move All to Inbox
						</button>
					</div>
				{/if}

				{#if loading}
					<p class="text-parchment-500">Loading…</p>
				{:else if files.length === 0}
					<div class="rounded-lg border border-clay-800 p-8 text-center">
						<p class="text-parchment-500">No orphaned files found.</p>
						<p class="mt-1 text-xs text-parchment-600">
							Run a scan to detect files in storage with no corresponding database entry.
						</p>
					</div>
				{:else}
					<div class="overflow-x-auto rounded-lg border border-clay-800">
						<table class="w-full text-left text-sm">
							<thead>
								<tr class="border-b border-clay-800 bg-clay-900">
									<th class="px-4 py-3 font-medium text-parchment-400">Key</th>
									<th class="px-4 py-3 font-medium text-parchment-400">Type</th>
									<th class="px-4 py-3 font-medium text-parchment-400">Source</th>
									<th class="px-4 py-3 font-medium text-parchment-400">Size</th>
									<th class="px-4 py-3 font-medium text-parchment-400">Detected</th>
									<th class="px-4 py-3 font-medium text-parchment-400">Actions</th>
								</tr>
							</thead>
							<tbody>
								{#each files as f (f.id)}
									<tr class="border-b border-clay-800 last:border-b-0 hover:bg-clay-900/50">
										<td class="px-4 py-3 font-mono text-xs text-parchment-200">{f.document_key}</td>
										<td class="px-4 py-3">
											<span
												class="inline-block rounded-full px-2 py-0.5 text-xs {f.document_key_type ===
												'uuid'
													? 'bg-blue-900/50 text-blue-400'
													: 'bg-amber-900/50 text-amber-400'}"
											>
												{f.document_key_type}
											</span>
										</td>
										<td class="px-4 py-3 text-parchment-300">{f.source_dir}</td>
										<td
											class="px-4 py-3 text-parchment-300"
											style="font-variant-numeric: tabular-nums">{formatSize(f.file_size)}</td
										>
										<td class="px-4 py-3 text-parchment-300">{formatTime(f.detected_at)}</td>
										<td class="px-4 py-3">
											<div class="flex gap-1">
												<button
													onclick={() => handleDelete(f.id)}
													class="rounded-md px-2 py-1 text-xs font-medium text-terracotta-400 hover:bg-terracotta-900/50 hover:text-terracotta-300"
												>
													Delete
												</button>
												{#if f.document_key_type === 'uuid'}
													<button
														onclick={() => handleRestore(f.id)}
														class="rounded-md px-2 py-1 text-xs font-medium text-emerald-400 hover:bg-emerald-900/50 hover:text-emerald-300"
													>
														Restore
													</button>
												{/if}
												<button
													onclick={() => handleMoveToInbox(f.id)}
													class="rounded-md px-2 py-1 text-xs font-medium text-parchment-400 hover:bg-clay-800 hover:text-parchment-200"
												>
													Move to Inbox
												</button>
											</div>
										</td>
									</tr>
								{/each}
							</tbody>
						</table>
					</div>
				{/if}
			</div>
		{:else if activeTab === 'errored'}
			<div class="space-y-4">
				<div class="flex items-center justify-between">
					<h1 class="text-2xl font-semibold text-parchment-200">Errored Files</h1>
					{#if erroredFiles.length > 0}
						<button
							onclick={handleErroredDeleteAll}
							class="rounded-lg bg-terracotta-700 px-3 py-1.5 text-xs font-medium text-parchment-200 hover:bg-terracotta-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:ring-offset-2 focus-visible:ring-offset-clay-950"
						>
							Delete All
						</button>
					{/if}
				</div>

				{#if erroredLoading}
					<p class="text-parchment-500">Loading…</p>
				{:else if erroredFiles.length === 0}
					<div class="rounded-lg border border-clay-800 p-8 text-center">
						<p class="text-parchment-500">No errored files found.</p>
					</div>
				{:else}
					<div class="overflow-x-auto rounded-lg border border-clay-800">
						<table class="w-full text-left text-sm">
							<thead>
								<tr class="border-b border-clay-800 bg-clay-900">
									<th class="px-4 py-3 font-medium text-parchment-400">Name</th>
									<th class="px-4 py-3 font-medium text-parchment-400">Subdir</th>
									<th class="px-4 py-3 font-medium text-parchment-400">Size</th>
									<th class="px-4 py-3 font-medium text-parchment-400">Modified</th>
									<th class="px-4 py-3 font-medium text-parchment-400">Actions</th>
								</tr>
							</thead>
							<tbody>
								{#each erroredFiles as f (f.name + f.subdir)}
									<tr class="border-b border-clay-800 last:border-b-0 hover:bg-clay-900/50">
										<td
											class="max-w-xs truncate px-4 py-3 font-mono text-xs text-parchment-200"
											title={f.name}>{f.name}</td
										>
										<td class="px-4 py-3">
											<span
												class="inline-block rounded-full px-2 py-0.5 text-xs {f.subdir ===
												'duplicated'
													? 'bg-amber-900/50 text-amber-400'
													: 'bg-terracotta-900/50 text-terracotta-400'}"
											>
												{f.subdir}
											</span>
										</td>
										<td
											class="px-4 py-3 text-parchment-300"
											style="font-variant-numeric: tabular-nums">{formatSize(f.size)}</td
										>
										<td class="px-4 py-3 text-parchment-300">{formatTime(f.modified_at)}</td>
										<td class="px-4 py-3">
											<div class="flex gap-1">
												<button
													onclick={() => api.errored.download(f.subdir, f.name)}
													class="rounded-md px-2 py-1 text-xs font-medium text-blue-400 hover:bg-blue-900/50 hover:text-blue-300 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:ring-offset-2 focus-visible:ring-offset-clay-950"
												>
													Download
												</button>
												<button
													onclick={() => handleErroredDelete(f.subdir, f.name)}
													class="rounded-md px-2 py-1 text-xs font-medium text-terracotta-400 hover:bg-terracotta-900/50 hover:text-terracotta-300 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:ring-offset-2 focus-visible:ring-offset-clay-950"
												>
													Delete
												</button>
											</div>
										</td>
									</tr>
								{/each}
							</tbody>
						</table>
					</div>
				{/if}
			</div>
		{/if}
	</div>
{/if}
