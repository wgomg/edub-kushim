<!--
  Replaces the tag the caller passes with the components the user confirms here; those components are
  created, the tag's documents are re-attached to them, and the tag is deleted. Works for provisional
  and promoted values; the server refuses seeded values, whose 409 reason is shown inline, and calls
  onsplit after a successful split, even when the dialog was closed while the request was in flight.
-->
<script>
	import { untrack } from 'svelte';
	import { api } from '#lib/api.js';
	import Modal from '#lib/components/Modal.svelte';
	import Icon from '#lib/components/Icon.svelte';
	import { toastStore } from '#lib/stores/toastStore.svelte.js';
	import { documentsLabel } from '#lib/utils/html.js';

	let { open, tag, initialParts = [], onClose, onsplit } = $props();

	const inputClass =
		'w-full rounded-md border border-clay-700 bg-clay-900 px-3 py-1.5 text-sm text-parchment-200 placeholder-parchment-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none';
	const iconButton =
		'rounded-md p-1.5 text-parchment-400 hover:bg-clay-800 hover:text-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-40';
	const btnGhost =
		'rounded-lg px-3 py-1.5 text-xs text-parchment-400 hover:bg-clay-800 hover:text-terracotta-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-50';
	const btnPrimary =
		'rounded-lg bg-gold-500 px-3 py-1.5 text-xs font-medium text-clay-950 hover:bg-gold-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-50';

	let parts = $state([]);
	let busy = $state(false);
	let error = $state('');

	// the parent owns the prefill; reading it untracked keeps a re-render while the dialog is open from
	// wiping the components the user is typing
	$effect(() => {
		if (!open) return;
		const seeded = untrack(() => (initialParts ?? []).map((name) => ({ name })));
		parts = seeded.length > 0 ? seeded : [{ name: '' }];
		busy = false;
		error = '';
	});

	function addPart() {
		parts = [...parts, { name: '' }];
	}

	function removePart(index) {
		parts = parts.filter((_, i) => i !== index);
		if (parts.length === 0) parts = [{ name: '' }];
	}

	async function submit(e) {
		e.preventDefault();
		if (!tag || busy) return;
		const names = parts.map((part) => part.name.trim()).filter(Boolean);
		if (names.length === 0) {
			error = 'At least one component is required';
			return;
		}
		busy = true;
		error = '';
		const result = await api.tags.split(tag.name, names);
		busy = false;
		if (result.ok) {
			toastStore.success(`Split "${tag.name}"`);
			if (onsplit) onsplit();
			onClose();
			return;
		}
		if (result.status === 404) {
			error = 'This tag no longer exists';
		} else if (result.status === 409) {
			error = result.data?.error ?? 'Split did not apply';
		} else {
			error = 'Failed to split the tag';
		}
	}
</script>

<Modal {open} title="Split compound" {onClose}>
	<form onsubmit={submit}>
		<div class="space-y-4">
			<p class="text-[0.8125rem] text-parchment-400">
				Replace <b class="text-parchment-300">{tag?.name}</b> with its components. The components
				are created, the tag's {documentsLabel(tag?.document_count ?? 0)} are attached to them, and the
				tag is deleted.
			</p>
			<div class="space-y-2">
				{#each parts as part, index (index)}
					<div class="flex items-center gap-2">
						<input
							type="text"
							bind:value={part.name}
							placeholder="Component name…"
							autocomplete="off"
							class={inputClass}
							aria-label="Component {index + 1}"
						/>
						<button
							type="button"
							class={iconButton}
							title="Remove this component"
							aria-label="Remove component {index + 1}"
							onclick={() => removePart(index)}
						>
							<Icon name="minus" size={14} />
						</button>
					</div>
				{/each}
			</div>
			<button type="button" class="{btnGhost} px-0" onclick={addPart}>+ Add component</button>
			{#if error}
				<p class="text-sm text-terracotta-500" aria-live="polite">{error}</p>
			{/if}
			<div class="flex justify-end gap-2">
				<button type="button" onclick={onClose} class={btnGhost}>Cancel</button>
				<button type="submit" disabled={busy} class={btnPrimary}>
					{busy ? 'Splitting…' : 'Split'}
				</button>
			</div>
		</div>
	</form>
</Modal>
