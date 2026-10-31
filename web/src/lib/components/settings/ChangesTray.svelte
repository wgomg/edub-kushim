<script>
	import { onMount } from 'svelte';
	import { fade, fly } from 'svelte/transition';
	import { confirmStore } from '#lib/stores/confirmStore.svelte.js';
	import { settingsStore } from '#lib/stores/settingsStore.svelte.js';
	import { SETTINGS_SECTION_TITLES } from '#lib/settingsSections.js';

	let { open = false, onClose = () => {} } = $props();

	let reducedMotion = $state(false);

	onMount(() => {
		const query = window.matchMedia('(prefers-reduced-motion: reduce)');
		reducedMotion = query.matches;
		const onChange = (event) => (reducedMotion = event.matches);
		query.addEventListener('change', onChange);
		return () => query.removeEventListener('change', onChange);
	});

	let groupedConfig = $derived.by(() => {
		const groups = {};
		for (const change of settingsStore.configChanges) {
			(groups[change.group] = groups[change.group] || []).push(change);
		}
		return groups;
	});

	function formatValue(value) {
		if (value === undefined) return '—';
		if (typeof value === 'string') return value.trim() === '' ? 'empty' : value;
		if (typeof value === 'boolean') return value ? 'on' : 'off';
		if (Array.isArray(value) || (value && typeof value === 'object')) {
			return JSON.stringify(value);
		}
		return String(value);
	}

	function handleKeydown(event) {
		if (event.key === 'Escape' && open) onClose();
	}

	async function handleDiscardAll() {
		const count = settingsStore.pendingCount;
		if (count === 0) return;
		const ok = await confirmStore.confirm({
			title: 'Discard Staged Changes',
			message: `Discard ${count === 1 ? '1 staged change' : `${count} staged changes`}? This cannot be undone.`,
			danger: true
		});
		if (!ok) return;
		await settingsStore.discardAll();
		onClose();
	}
</script>

<svelte:window onkeydown={handleKeydown} />

{#if open}
	<div
		class="fixed inset-0 z-[90] bg-[rgba(10,8,7,0.62)]"
		transition:fade={{ duration: reducedMotion ? 0 : 150 }}
		onclick={onClose}
		role="presentation"
	></div>
	<div
		class="fixed inset-y-0 right-0 z-[100] flex w-[min(25rem,100vw)] flex-col border-l border-clay-800 bg-clay-900 pr-[env(safe-area-inset-right)] pb-[env(safe-area-inset-bottom)] shadow-[-24px_0_48px_rgba(0,0,0,0.35)]"
		transition:fly={{ x: reducedMotion ? 0 : 320, duration: reducedMotion ? 0 : 180 }}
		role="dialog"
		aria-modal="true"
		aria-labelledby="changes-title"
	>
		<div class="flex items-start justify-between gap-4 border-b border-clay-800 px-5 py-4">
			<div>
				<p
					class="font-mono text-[0.625rem] font-semibold tracking-[0.14em] text-gold-500 uppercase"
				>
					Review
				</p>
				<h2 id="changes-title" class="mt-1 text-lg font-semibold text-parchment-200">
					Staged Changes
				</h2>
			</div>
			<button
				type="button"
				onclick={onClose}
				aria-label="Close staged changes"
				class="text-xl leading-none text-parchment-500 hover:text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
			>
				×
			</button>
		</div>

		<div class="flex-1 overflow-y-auto overscroll-contain px-5 py-4">
			{#if settingsStore.pendingCount === 0}
				<div class="py-10 text-center text-sm text-parchment-500">
					No staged changes.<br />Edits from any settings page collect here.
				</div>
			{:else}
				{#each Object.keys(groupedConfig) as group (group)}
					<div class="mb-5">
						<h3
							class="mb-1.5 font-mono text-[0.625rem] font-semibold tracking-[0.1em] text-parchment-500 uppercase"
						>
							{SETTINGS_SECTION_TITLES[group] ?? group}
						</h3>
						{#each groupedConfig[group] as change (change.key)}
							<div class="flex items-start gap-3 border-b border-clay-800/60 py-2 last:border-b-0">
								<div class="min-w-0 flex-1">
									<strong
										class="block font-mono text-[0.75rem] font-medium break-all text-parchment-300"
									>
										{change.label}
									</strong>
									<span class="block font-mono text-[0.6875rem] break-words text-parchment-500">
										{#if change.secret}
											value changed
										{:else}
											{formatValue(change.from)} → {formatValue(change.to)}
										{/if}
									</span>
								</div>
								<button
									type="button"
									onclick={() => settingsStore.discardConfigChange(change.key)}
									aria-label={`Discard change to ${change.label}`}
									class="text-sm leading-none text-parchment-500 hover:text-terracotta-400 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
								>
									×
								</button>
							</div>
						{/each}
					</div>
				{/each}

				{#if settingsStore.catalogOps.length > 0}
					<div class="mb-5">
						<h3
							class="mb-1.5 font-mono text-[0.625rem] font-semibold tracking-[0.1em] text-parchment-500 uppercase"
						>
							{SETTINGS_SECTION_TITLES.models}
						</h3>
						{#each settingsStore.catalogOps as op (op.id)}
							<div class="flex items-start gap-3 border-b border-clay-800/60 py-2 last:border-b-0">
								<div class="min-w-0 flex-1">
									<strong class="block text-[0.8125rem] font-medium text-parchment-300">
										{op.label}
									</strong>
									<span class="block font-mono text-[0.6875rem] text-parchment-500">
										catalog operation
									</span>
								</div>
							</div>
						{/each}
						<p class="mt-2 text-[0.6875rem] text-parchment-500">
							Catalog operations are replayed in order when you apply. Discard All to reset the
							catalog draft.
						</p>
					</div>
				{/if}
			{/if}
		</div>

		<div class="flex justify-end gap-2 border-t border-clay-800 px-5 py-3.5">
			<button
				type="button"
				onclick={handleDiscardAll}
				disabled={settingsStore.pendingCount === 0 || settingsStore.busy}
				class="rounded-lg border border-terracotta-700 px-3 py-1.5 text-[0.8125rem] font-medium text-terracotta-400 hover:bg-terracotta-950 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-40"
			>
				Discard All
			</button>
			<button
				type="button"
				onclick={() => settingsStore.applyAll()}
				disabled={settingsStore.pendingCount === 0 || settingsStore.busy}
				class="rounded-lg bg-gold-500 px-4 py-1.5 text-sm font-semibold text-clay-950 hover:bg-gold-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-40"
			>
				{settingsStore.saving ? 'Applying…' : 'Apply Changes'}
			</button>
		</div>
	</div>
{/if}
