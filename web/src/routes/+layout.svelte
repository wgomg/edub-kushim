<script>
	import './layout.css';
	import favicon from '$lib/assets/favicon.svg';

	let { children } = $props();
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

		<!-- Page content -->
		<main class="flex-1 overflow-y-auto bg-clay-950 p-6">
			{@render children()}
		</main>
	</div>
</div>
