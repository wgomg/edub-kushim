<script>
	import './layout.css';
	import favicon from '$lib/assets/favicon.svg';
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import { api } from '$lib/api.js';
	import * as authStore from '$lib/stores/authStore.js';
	import Toast from '$lib/components/Toast.svelte';
	import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
	import UploadModal from '$lib/components/UploadModal.svelte';
	import { confirmStore } from '$lib/stores/confirmStore.svelte.js';

	let { children } = $props();
	let missingTools = $state([]);
	let uploadOpen = $state(false);
	let authEnabled = $state(true);
	let configLoaded = $state(false);

	let missingCount = $derived(
		missingTools.reduce((sum, t) => {
			let n = t.available === false ? 1 : 0;
			if (t.companions) {
				n += t.companions.filter((c) => c.required && !c.available).length;
			}
			return sum + n;
		}, 0)
	);

	$effect(() => {
		if (
			configLoaded &&
			authEnabled &&
			!authStore.isAuthenticated() &&
			$page.url.pathname !== '/login'
		) {
			goto('/login');
		}
	});

	onMount(async () => {
		const [status, cfg] = await Promise.all([api.config.status(), api.config.get()]);
		if (status) {
			missingTools = status.missing_tools ?? [];
		}
		authEnabled = cfg?.server?.auth_enabled ?? true;
		configLoaded = true;
	});

	async function handleLogout() {
		const ok = await confirmStore.confirm({
			title: 'Log Out',
			message: 'Are you sure you want to log out?',
			danger: true
		});
		if (!ok) return;
		authStore.logout();
		goto('/login');
	}
</script>

<svelte:head><link rel="icon" href={favicon} /></svelte:head>

{#if $page.url.pathname !== '/login'}
	<a
		href="#main-content"
		class="sr-only focus:not-sr-only focus:fixed focus:top-4 focus:left-4 focus:z-50 focus:rounded-lg focus:bg-clay-900 focus:px-4 focus:py-2 focus:text-parchment-200 focus:ring-2 focus:ring-gold-500 focus:outline-none"
	>
		Skip to main content
	</a>
	<div class="flex h-screen overflow-hidden bg-clay-950">
		<!-- Sidebar -->
		<aside class="flex w-56 shrink-0 flex-col border-r border-clay-800 bg-clay-900">
			<div class="flex h-14 items-center gap-2 border-b border-clay-800 px-4">
				<span class="text-xl font-bold tracking-tight text-parchment-200" translate="no"
					>edub-kushim</span
				>
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
					href="/documents/orphaned"
					class="flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium text-parchment-400 hover:bg-clay-800 hover:text-parchment-200"
					>Orphaned &amp; Errored</a
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
			<header
				class="flex h-14 shrink-0 items-center gap-4 border-b border-clay-800 bg-clay-900 px-6"
			>
				<div class="flex-1"></div>
				{#if authStore.isAuthenticated()}
					<span class="text-sm text-parchment-400">{authStore.getUser().username}</span>
					<button
						onclick={handleLogout}
						class="text-sm text-parchment-400 hover:text-terracotta-500"
					>
						Logout
					</button>
				{/if}
				<button
					onclick={() => (uploadOpen = true)}
					class="rounded-lg bg-gold-500 px-4 py-2 text-sm font-medium text-clay-950 hover:bg-gold-600"
					>Upload</button
				>
			</header>

			{#if missingTools.length > 0}
				<div
					class="shrink-0 border-b border-gold-500/30 bg-gold-500/10 px-6 py-2 text-sm text-gold-500"
				>
					⚠️ {missingCount} required tool(s) not installed — document consumption is paused.
					<a href="/settings" class="underline">Review Settings</a>
				</div>
			{/if}

			<!-- Page content -->
			<main id="main-content" class="flex-1 overflow-y-auto bg-clay-950 p-6">
				{@render children()}
			</main>

			<Toast />
			<ConfirmDialog />
			<UploadModal open={uploadOpen} onClose={() => (uploadOpen = false)} />
		</div>
	</div>
{:else}
	<div class="flex min-h-screen items-center justify-center bg-clay-950 p-4">
		{@render children()}
	</div>
{/if}
