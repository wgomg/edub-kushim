<script>
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { api } from '$lib/api';
	import * as authStore from '$lib/stores/authStore.js';
	import { toastStore } from '$lib/stores/toastStore.svelte.js';
	import { confirmStore } from '$lib/stores/confirmStore.svelte.js';

	let profile = $state(null);
	let keyStatus = $state(null);
	let loading = $state(true);
	let rawKey = $state('');
	let showRawKey = $state(false);
	let generating = $state(false);
	let rotating = $state(false);
	let revoking = $state(false);
	let copied = $state(false);

	onMount(async () => {
		if (!authStore.isAuthenticated()) {
			goto(resolve('/'));
			return;
		}
		await load();
		loading = false;
	});

	async function load() {
		const [p, k] = await Promise.all([api.me.profile(), api.me.keyStatus()]);
		profile = p;
		keyStatus = k;
	}

	async function generate() {
		generating = true;
		const res = await api.me.generateKey();
		if (res.ok && res.data?.api_key) {
			rawKey = res.data.api_key;
			showRawKey = true;
			await load();
			toastStore.success('API key generated');
		} else {
			toastStore.error('Failed to generate API key');
		}
		generating = false;
	}

	async function rotate() {
		const ok = await confirmStore.confirm({
			title: 'Rotate API key',
			message: 'Rotate API key? The current key will be invalidated immediately.',
			danger: true
		});
		if (!ok) return;
		rotating = true;
		const res = await api.me.rotateKey();
		if (res.ok && res.data?.api_key) {
			rawKey = res.data.api_key;
			showRawKey = true;
			await load();
			toastStore.success('API key rotated');
		} else {
			toastStore.error('Failed to rotate API key');
		}
		rotating = false;
	}

	async function revoke() {
		const ok = await confirmStore.confirm({
			title: 'Revoke API key',
			message: 'Revoke API key? This action cannot be undone.',
			danger: true
		});
		if (!ok) return;
		revoking = true;
		const res = await api.me.revokeKey();
		if (res.ok) {
			rawKey = '';
			showRawKey = false;
			await load();
			toastStore.success('API key revoked');
		} else {
			toastStore.error('Failed to revoke API key');
		}
		revoking = false;
	}

	async function copyKey() {
		try {
			await navigator.clipboard.writeText(rawKey);
			copied = true;
			setTimeout(() => (copied = false), 2000);
		} catch {
			toastStore.error('Failed to copy to clipboard');
		}
	}
</script>

{#if loading}
	<p class="text-parchment-500">Loading…</p>
{:else if !profile}
	<div class="mx-auto max-w-lg">
		<p class="text-parchment-500">Failed to load profile.</p>
		<button
			onclick={load}
			class="mt-4 rounded-md bg-clay-800 px-4 py-2 text-sm font-medium text-parchment-400 hover:bg-clay-700 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
		>
			Retry
		</button>
	</div>
{:else}
	<div class="mx-auto max-w-lg space-y-6">
		<h1 class="text-2xl font-bold text-balance text-parchment-200">Profile</h1>

		<div class="rounded-xl border border-clay-800 bg-clay-900 p-5">
			<h2 class="mb-4 text-lg font-semibold text-parchment-200">Account</h2>
			<div class="space-y-3">
				<div>
					<span class="text-xs font-medium text-parchment-500">Username</span>
					<p class="text-parchment-200">{profile.username}</p>
				</div>
				<div>
					<span class="text-xs font-medium text-parchment-500">Role</span>
					<p>
						<span
							class="inline-block rounded-full px-2 py-0.5 text-xs font-medium {authStore.roleBadgeClass(
								profile.role
							)}">{profile.role}</span
						>
					</p>
				</div>
				<div>
					<span class="text-xs font-medium text-parchment-500">Created</span>
					<p class="text-parchment-200">{new Date(profile.created_at).toLocaleDateString()}</p>
				</div>
				<div>
					<span class="text-xs font-medium text-parchment-500">API Key</span>
					<p class="text-parchment-200">
						{keyStatus?.has_api_key ? 'Has key' : 'No key'}
						{#if keyStatus?.api_key_prefix}
							<span class="text-parchment-400">({keyStatus.api_key_prefix}…)</span>
						{/if}
					</p>
				</div>
			</div>
		</div>

		<div class="rounded-xl border border-clay-800 bg-clay-900 p-5">
			<h2 class="mb-4 text-lg font-semibold text-parchment-200">API Key</h2>
			<div class="space-y-3">
				{#if keyStatus?.has_api_key}
					{#if showRawKey && rawKey}
						<div class="rounded-md border border-clay-700 bg-clay-950 p-3">
							<p class="mb-2 font-mono text-sm break-all text-parchment-200">{rawKey}</p>
							<button
								onclick={copyKey}
								aria-live="polite"
								class="rounded-md bg-gold-600 px-3 py-1 text-xs font-medium text-clay-950 hover:bg-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							>
								{copied ? 'Copied!' : 'Copy to Clipboard'}
							</button>
							<p class="mt-2 text-xs text-terracotta-500" aria-live="polite">
								Save this key — it will not be shown again
							</p>
						</div>
					{/if}
					<div class="flex gap-2">
						<button
							onclick={rotate}
							disabled={rotating}
							class="rounded-lg bg-gold-600 px-4 py-2 text-sm font-medium text-clay-950 hover:bg-gold-500 disabled:opacity-50"
						>
							{rotating ? 'Rotating…' : 'Rotate'}
						</button>
						<button
							onclick={revoke}
							disabled={revoking}
							class="rounded-lg border border-terracotta-600 px-4 py-2 text-sm font-medium text-terracotta-500 hover:bg-terracotta-900/50 disabled:opacity-50"
						>
							{revoking ? 'Revoking…' : 'Revoke'}
						</button>
					</div>
				{:else}
					<button
						onclick={generate}
						disabled={generating}
						class="rounded-lg bg-gold-600 px-4 py-2 text-sm font-medium text-clay-950 hover:bg-gold-500 disabled:opacity-50"
					>
						{generating ? 'Generating…' : 'Generate API Key'}
					</button>
				{/if}
			</div>
		</div>
	</div>
{/if}
