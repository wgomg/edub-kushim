<!--
  Folds the tag the caller passes onto another live tag that the user picks here; the submission is made
  by name, surfaces the server's 409 reason (seeded values cannot be folded away), and calls onfolded after
  a successful fold, even when the dialog was closed while the request was in flight.
-->
<script>
	import { api } from '#lib/api.js';
	import Modal from '#lib/components/Modal.svelte';
	import Suggest from '#lib/components/Suggest.svelte';
	import { toastStore } from '#lib/stores/toastStore.svelte.js';
	import { documentsLabel } from '#lib/utils/html.js';

	let { open, tag, onClose, onfolded } = $props();

	const TAG_PAGE = 100;
	const inputClass =
		'w-full rounded-md border border-clay-700 bg-clay-950 px-3 py-1.5 text-sm text-parchment-200 placeholder-parchment-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus:outline-none';
	const listClass =
		'absolute top-full right-0 left-0 z-20 mt-1 max-h-40 overflow-y-auto rounded-md border border-clay-700 bg-clay-950 shadow-lg';
	const btnPrimary =
		'rounded-lg bg-gold-500 px-3 py-1.5 text-xs font-medium text-clay-950 hover:bg-gold-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus:outline-none disabled:opacity-50';

	let query = $state('');
	let picked = $state('');
	let busy = $state(false);
	let error = $state('');
	const chosen = $derived(picked && query.trim() === picked ? picked : '');

	$effect(() => {
		if (!open) return;
		query = '';
		picked = '';
		busy = false;
		error = '';
	});

	async function fetchTags(q, offset, signal) {
		const data = await api.tags.list(q, TAG_PAGE, offset, signal);
		if (!data) throw new Error('Failed to load tags');
		return { items: data.results ?? [], total: data.total ?? 0 };
	}

	function isTarget(t) {
		return t.name !== tag?.name;
	}

	function pickTarget(t) {
		picked = t.name;
		query = t.name;
		error = '';
	}

	function pickFirstTarget(_q, first) {
		if (!first) return false;
		pickTarget(first);
		return true;
	}

	async function submit(e) {
		e.preventDefault();
		if (!chosen || busy || !tag) return;
		const loser = tag.name;
		const target = chosen;
		busy = true;
		error = '';
		const result = await api.tags.fold(loser, target);
		busy = false;
		if (result.ok) {
			toastStore.success(`Folded "${loser}" into "${target}"`);
			if (onfolded) onfolded();
			onClose();
			return;
		}
		if (result.status === 404) {
			error = 'This tag no longer exists';
		} else if (result.status === 409) {
			error = result.data?.error ?? 'Fold did not apply';
		} else {
			error = 'Failed to fold tag';
		}
	}
</script>

<Modal {open} title="Fold tag into another" {onClose}>
	<form onsubmit={submit}>
		<div class="space-y-4">
			<p class="text-[0.8125rem] leading-relaxed text-parchment-400">
				Fold <b class="text-parchment-300">{tag?.name}</b> into another tag? Its
				{documentsLabel(tag?.document_count ?? 0)} will move onto the target, and
				<b class="text-parchment-300">{tag?.name}</b> becomes an alias and stops existing as a tag.
			</p>
			<div>
				<label for="fold-target" class="mb-1 block text-xs font-medium text-parchment-400"
					>Target tag</label
				>
				<div class="relative">
					<Suggest
						id="fold-target"
						placeholder="Type to search tags…"
						fetch={fetchTags}
						filter={isTarget}
						onenter={pickFirstTarget}
						onselect={pickTarget}
						onerror={() => toastStore.error('Could not load suggestions')}
						bind:query
						{inputClass}
						{listClass}
					/>
				</div>
				<p class="mt-1.5 text-[0.75rem] text-parchment-600">
					{#if chosen}
						Folding into <b class="text-parchment-300">{chosen}</b>.
					{:else}
						Pick an existing tag; the kept value is the target.
					{/if}
				</p>
			</div>
			{#if error}
				<p class="text-sm text-terracotta-500" aria-live="polite">{error}</p>
			{/if}
			<div class="flex justify-end gap-2">
				<button
					type="button"
					onclick={onClose}
					class="rounded-md px-3 py-1.5 text-xs font-medium text-parchment-400 hover:bg-clay-800 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
				>
					Cancel
				</button>
				<button type="submit" disabled={!chosen || busy} class={btnPrimary}>
					{busy ? 'Folding…' : 'Fold'}
				</button>
			</div>
		</div>
	</form>
</Modal>
