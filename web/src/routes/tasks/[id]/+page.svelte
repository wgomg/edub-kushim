<script>
	import { onMount } from 'svelte';
	import { api } from '$lib/api';
	import { toastStore } from '$lib/stores/toastStore.svelte.js';

	let { params } = $props();

	let task = $state();
	let loading = $state(true);
	let retrying = $state(false);

	function formatDate(v) {
		if (!v) return '—';
		return new Date(v).toLocaleString();
	}

	function statusBadgeClass(status) {
		const colors = {
			waiting: 'bg-amber-600/20 text-amber-400',
			pending: 'bg-parchment-500/20 text-parchment-400',
			processing: 'bg-lapis-600/20 text-lapis-600',
			completed: 'bg-emerald-600/20 text-emerald-500',
			failed: 'bg-terracotta-600/20 text-terracotta-500',
			cancelled: 'bg-parchment-500/10 text-parchment-500',
			discarded: 'bg-terracotta-600/10 text-terracotta-400'
		};
		return colors[status] ?? 'bg-parchment-500/10 text-parchment-500';
	}

	onMount(async () => {
		task = await api.tasks.get(params.id);
		loading = false;
	});

	async function handleRetry() {
		retrying = true;
		const res = await fetch(`/api/v1/tasks/${params.id}/retry`, { method: 'POST' });
		if (res.ok) {
			toastStore.success('Task retried');
			task = await api.tasks.get(params.id);
		} else {
			toastStore.error('Failed to retry task');
		}
		retrying = false;
	}
</script>

<div class="space-y-6">
	<a
		href={task ? `/tasks?batch=${task.batch_id}` : '/tasks'}
		class="inline-flex items-center gap-1 text-sm text-parchment-500 hover:text-parchment-200"
	>
		&larr; Back to batch
	</a>

	{#if loading}
		<p class="text-parchment-500">Loading…</p>
	{:else if !task}
		<div class="rounded-lg border border-clay-800 bg-clay-900 p-6 text-center">
			<p class="text-parchment-500">Task not found</p>
			<a
				href="/tasks"
				class="mt-2 inline-flex items-center gap-1 text-sm text-parchment-500 hover:text-parchment-200"
			>
				&larr; Back to batches
			</a>
		</div>
	{:else}
		<div class="flex items-center gap-3">
			<h1 class="text-2xl font-semibold text-parchment-200">
				Task <span class="font-mono text-sm text-parchment-400">{task.task_id}</span>
			</h1>
			<span
				class="inline-block rounded-full px-2.5 py-0.5 text-xs font-medium {statusBadgeClass(
					task.status
				)}"
			>
				{task.status}
			</span>
		</div>

		<div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
			<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
				<p class="text-xs font-medium tracking-wider text-parchment-500 uppercase">Task Type</p>
				<p class="mt-1 text-parchment-200">{task.task_type}</p>
			</div>

			<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
				<p class="text-xs font-medium tracking-wider text-parchment-500 uppercase">Batch ID</p>
				<p class="mt-1">
					<a
						href="/tasks?batch={task.batch_id}"
						class="font-mono text-sm text-lapis-400 hover:text-lapis-300"
					>
						{task.batch_id}
					</a>
				</p>
			</div>

			<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
				<p class="text-xs font-medium tracking-wider text-parchment-500 uppercase">File Name</p>
				<p class="mt-1 text-parchment-200">{task.file_name ?? '—'}</p>
			</div>

			{#if task.document_id}
				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<p class="text-xs font-medium tracking-wider text-parchment-500 uppercase">Document ID</p>
					<p class="mt-1">
						<a
							href="/documents/{task.document_id}"
							class="font-mono text-sm text-lapis-400 hover:text-lapis-300"
						>
							{task.document_id}
						</a>
					</p>
				</div>
			{/if}

			<div class="rounded-lg border border-clay-800 bg-clay-900 p-4 sm:col-span-2 lg:col-span-2">
				<p class="text-xs font-medium tracking-wider text-parchment-500 uppercase">Timestamps</p>
				<div class="mt-2 grid grid-cols-3 gap-4 text-sm">
					<div>
						<p class="text-parchment-500">Created</p>
						<p class="text-parchment-200">{formatDate(task.created_at)}</p>
					</div>
					<div>
						<p class="text-parchment-500">Started</p>
						<p class="text-parchment-200">{formatDate(task.started_at)}</p>
					</div>
					<div>
						<p class="text-parchment-500">Completed</p>
						<p class="text-parchment-200">{formatDate(task.completed_at)}</p>
					</div>
				</div>
			</div>
		</div>

		{#if task.error}
			<div class="rounded-lg border border-terracotta-600 bg-terracotta-800/30 p-4">
				<p class="text-xs font-medium tracking-wider text-parchment-400 uppercase">Error</p>
				<p class="mt-1 text-sm whitespace-pre-wrap text-parchment-200">{task.error}</p>
			</div>
		{/if}

		{#if task.status === 'failed'}
			<button
				onclick={handleRetry}
				disabled={retrying}
				class="rounded-lg bg-gold-500 px-4 py-2 text-sm font-medium text-clay-950 hover:bg-gold-600 disabled:opacity-50"
			>
				{retrying ? 'Retrying…' : 'Retry Task'}
			</button>
		{/if}
	{/if}
</div>
