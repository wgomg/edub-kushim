<script>
	import './layout.css';
	import favicon from '$lib/assets/favicon.svg';
	import { onMount } from 'svelte';
	import { api } from '$lib/api.js';

	let { children } = $props();
	let missingTools = $state([]);

	let missingCount = $derived(missingTools.reduce((sum, t) => {
		let n = t.available === false ? 1 : 0;
		if (t.companions) {
			n += t.companions.filter(c => c.required && !c.available).length;
		}
		return sum + n;
	}, 0));

	onMount(async () => {
		const status = await api.config.status();
		if (status) {
			missingTools = status.missing_tools ?? [];
		}
	});
</script>

<svelte:head><link rel="icon" href={favicon} /></svelte:head>

<div class="flex h-screen overflow-hidden bg-clay-950">
	<!-- Sidebar -->
	<aside class="flex w-56 shrink-0 flex-col border-r border-clay-800 bg-clay-900">
		<div class="flex h-14 items-center gap-2 border-b border-clay-800 px-4">
			<span class="text-xl font-bold tracking-tight text-parchment-200">edub-kushim</span>
		</div>
		<nav class="flex-1 space-y-1 px-3 py-4">
			<a
				href="/"
				class="flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium text-parchment-400 hover:bg-clay-800 hover:text-parchment-200"
				>Dashboard</a
			>
			<a
				href="/documents"
				class="flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium text-parchment-400 hover:bg-clay-800 hover:text-parchment-200"
				>Documents</a
			>
			<a
				href="/tasks"
				class="flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium text-parchment-400 hover:bg-clay-800 hover:text-parchment-200"
				>Tasks</a
			>
			<a
				href="/tags"
				class="flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium text-parchment-400 hover:bg-clay-800 hover:text-parchment-200"
				>Tags</a
			>
			<a
				href="/people"
				class="flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium text-parchment-400 hover:bg-clay-800 hover:text-parchment-200"
				>People</a
			>
			<a
				href="/document-types"
				class="flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium text-parchment-400 hover:bg-clay-800 hover:text-parchment-200"
				>Document Types</a
			>
			<a
				href="/settings"
				class="flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium text-parchment-400 hover:bg-clay-800 hover:text-parchment-200"
				>Settings</a
			>
		</nav>
	</aside>

	<!-- Main area -->
	<div class="flex flex-1 flex-col overflow-hidden">
		<!-- Top bar -->
		<header class="flex h-14 shrink-0 items-center gap-4 border-b border-clay-800 bg-clay-900 px-6">
			<div class="flex-1"></div>
			<button
				class="rounded-lg bg-gold-500 px-4 py-2 text-sm font-medium text-clay-950 hover:bg-gold-600"
				>Upload</button
			>
		</header>

		{#if missingTools.length > 0}
			<div class="shrink-0 border-b border-gold-500/30 bg-gold-500/10 px-6 py-2 text-sm text-gold-500">
				⚠️ {missingCount} required tool(s) not installed —
				document consumption is paused.
				<a href="/settings" class="underline">Review settings</a>
			</div>
		{/if}

		<!-- Page content -->
		<main class="flex-1 overflow-y-auto bg-clay-950 p-6">
			{@render children()}
		</main>
	</div>
</div>
