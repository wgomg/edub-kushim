<script>
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { page } from '$app/stores';
	import * as authStore from '$lib/stores/authStore.js';
	import { settingsStore } from '$lib/stores/settingsStore.svelte.js';
	import { SETTINGS_SECTION_TITLES, SETTINGS_SECTIONS } from '$lib/settingsSections.js';
	import ChangesTray from '$lib/components/settings/ChangesTray.svelte';
	import SaveBar from '$lib/components/settings/SaveBar.svelte';
	import SettingsPalette from '$lib/components/settings/SettingsPalette.svelte';

	let { children } = $props();

	let paletteOpen = $state(false);

	const HASH_ROUTES = {
		overview: '/settings',
		...Object.fromEntries(SETTINGS_SECTIONS.map((section) => [section.id, section.path]))
	};

	let segment = $derived.by(() => {
		const parts = $page.url.pathname.split('/').filter(Boolean);
		return parts[0] === 'settings' ? (parts[1] ?? '') : '';
	});
	let title = $derived(SETTINGS_SECTION_TITLES[segment] ?? 'Settings');
	let isHub = $derived(!segment);

	function handleBeforeUnload(event) {
		if (!settingsStore.isDirty()) return;
		event.preventDefault();
		event.returnValue = '';
	}

	onMount(() => {
		settingsStore.restoreAdvanced();
		settingsStore.load();

		const url = new URL(window.location.href);
		const tab = url.searchParams.get('tab');
		if (tab) {
			const next = tab.toLowerCase() === 'users' ? '/settings/access' : '/settings';
			goto(resolve(next), { replaceState: true });
		} else if (url.pathname === '/settings' && url.hash) {
			const key = url.hash.slice(1);
			if (Object.hasOwn(HASH_ROUTES, key)) {
				const target = HASH_ROUTES[key];
				if (typeof target === 'string') goto(resolve(target), { replaceState: true });
			}
		}

		window.addEventListener('beforeunload', handleBeforeUnload);
		return () => window.removeEventListener('beforeunload', handleBeforeUnload);
	});
</script>

{#if authStore.authEnabled() && !authStore.isAdmin()}
	<p class="text-parchment-500">You do not have permission to view this page.</p>
{:else if settingsStore.loadError}
	<div class="max-w-xl">
		<p class="text-terracotta-400">{settingsStore.loadError}</p>
		<button
			type="button"
			onclick={() => settingsStore.load()}
			class="mt-3 rounded-lg border border-clay-700 px-3 py-1.5 text-sm text-parchment-300 hover:bg-clay-800 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
		>
			Retry
		</button>
	</div>
{:else if !settingsStore.loaded}
	<div class="text-parchment-500">Loading settings…</div>
{:else}
	<div class="settings-scope mx-auto max-w-6xl pb-24" class:advanced={settingsStore.advanced}>
		<div
			class="sticky -top-6 z-20 mb-7 flex flex-wrap items-center justify-between gap-3 border-b border-clay-800 bg-clay-950 py-3"
		>
			<div class="flex items-center gap-2 text-sm">
				{#if isHub}
					<span
						class="font-mono text-[0.6875rem] font-semibold tracking-[0.14em] text-parchment-500 uppercase"
						>Configuration</span
					>
				{:else}
					<a
						href={resolve('/settings')}
						class="text-parchment-500 hover:text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					>
						<span aria-hidden="true">←</span> Settings
					</a>
					<span class="text-clay-600">/</span>
					<span class="text-parchment-200">{title}</span>
				{/if}
			</div>

			<div class="flex flex-wrap items-center gap-2">
				{#if !isHub}
					<div
						class="flex rounded-lg border border-clay-800 bg-clay-950 p-0.5"
						role="group"
						aria-label="Settings detail level"
					>
						<button
							type="button"
							aria-pressed={!settingsStore.advanced}
							onclick={() => settingsStore.setAdvanced(false)}
							class="rounded-md px-2.5 py-1 text-xs font-semibold focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none {!settingsStore.advanced
								? 'bg-clay-800 text-parchment-200'
								: 'text-parchment-500 hover:text-parchment-300'}"
						>
							Essentials
						</button>
						<button
							type="button"
							aria-pressed={settingsStore.advanced}
							onclick={() => settingsStore.setAdvanced(true)}
							class="rounded-md px-2.5 py-1 text-xs font-semibold focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none {settingsStore.advanced
								? 'bg-clay-800 text-parchment-200'
								: 'text-parchment-500 hover:text-parchment-300'}"
						>
							Advanced
						</button>
					</div>
				{/if}

				<button
					type="button"
					onclick={() => (paletteOpen = true)}
					class="flex items-center gap-2 rounded-lg border border-clay-800 bg-clay-950 px-2.5 py-1.5 text-xs text-parchment-500 hover:border-clay-700 hover:text-parchment-300 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
				>
					Search
					<kbd
						class="rounded-[0.25rem] border border-b-2 border-clay-700 px-1.5 py-px font-mono text-[0.625rem] font-semibold"
						>/</kbd
					>
				</button>

				<button
					type="button"
					onclick={() => settingsStore.openTray()}
					class="flex items-center gap-2 rounded-lg border px-2.5 py-1.5 text-xs font-semibold focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none {settingsStore.pendingCount >
					0
						? 'border-gold-800 bg-gold-500/10 text-gold-500'
						: 'border-clay-700 bg-clay-950 text-parchment-400'}"
				>
					<span
						class="size-1.5 rounded-full {settingsStore.pendingCount > 0
							? 'bg-gold-500'
							: 'bg-parchment-600'}"
					></span>
					{settingsStore.pendingCount} staged
				</button>
			</div>
		</div>

		<fieldset class="contents" disabled={settingsStore.busy}>
			{@render children()}
		</fieldset>
	</div>

	<ChangesTray open={settingsStore.trayOpen} onClose={() => settingsStore.closeTray()} />
	<SettingsPalette bind:open={paletteOpen} />
	<SaveBar
		count={settingsStore.pendingCount}
		saving={settingsStore.saving}
		busy={settingsStore.busy}
		onSave={settingsStore.applyAll}
		onRevert={settingsStore.discardAll}
	/>
{/if}
