<!--
  Folds the tag the caller passes onto another live tag that the user picks here; the submission is made
  by name, surfaces the server's 409 reason (seeded values cannot be folded away), and calls onfolded after
  a successful fold, even when the dialog was closed while the request was in flight.
-->
<script>
	import { onDestroy } from 'svelte';
	import { api } from '#lib/api.js';
	import Modal from '#lib/components/Modal.svelte';
	import { toastStore } from '#lib/stores/toastStore.svelte.js';
	import { documentsLabel } from '#lib/utils/html.js';

	let { open, tag, onClose, onfolded } = $props();

	const inputClass =
		'w-full rounded-md border border-clay-700 bg-clay-950 px-3 py-1.5 text-sm text-parchment-200 placeholder-parchment-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none';
	const listClass =
		'absolute top-full right-0 left-0 z-20 mt-1 max-h-40 overflow-y-auto rounded-md border border-clay-700 bg-clay-950 shadow-lg';
	const optionClass = 'cursor-pointer px-3 py-1.5 text-sm text-parchment-200 hover:bg-clay-800';
	const btnPrimary =
		'rounded-lg bg-gold-500 px-3 py-1.5 text-xs font-medium text-clay-950 hover:bg-gold-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-50';

	let query = $state('');
	let suggestions = $state([]);
	let selIndex = $state(-1);
	let chosen = $state('');
	let busy = $state(false);
	let error = $state('');
	let debounce;
	// invalidates in-flight suggestion responses on close, reopen, keystroke, and pick
	let requestSeq = 0;

	$effect(() => {
		if (!open) {
			clearTimeout(debounce);
			requestSeq++;
			return;
		}
		requestSeq++;
		query = '';
		suggestions = [];
		selIndex = -1;
		chosen = '';
		busy = false;
		error = '';
	});

	onDestroy(() => clearTimeout(debounce));

	function onInput() {
		chosen = '';
		selIndex = -1;
		error = '';
		clearTimeout(debounce);
		requestSeq++;
		const q = query.trim();
		if (!q) {
			suggestions = [];
			return;
		}
		debounce = setTimeout(async () => {
			const seq = ++requestSeq;
			try {
				const list = await api.autocomplete.tags(q, 10);
				if (seq !== requestSeq) return;
				suggestions = list.filter((t) => t.name !== tag?.name);
			} catch {
				if (seq !== requestSeq) return;
				suggestions = [];
				toastStore.error('Could not load suggestions');
			}
		}, 200);
	}

	function onKeydown(e) {
		if (e.key === 'Enter') {
			e.preventDefault();
			if (suggestions.length > 0) pick(suggestions[selIndex >= 0 ? selIndex : 0].name);
			else if (chosen) submit(e);
			return;
		}
		if (e.key === 'ArrowDown' && suggestions.length > 0) {
			e.preventDefault();
			selIndex = (selIndex + 1) % suggestions.length;
			return;
		}
		if (e.key === 'ArrowUp' && suggestions.length > 0) {
			e.preventDefault();
			selIndex = (selIndex - 1 + suggestions.length) % suggestions.length;
		}
	}

	function pick(name) {
		chosen = name;
		query = name;
		suggestions = [];
		selIndex = -1;
		error = '';
		requestSeq++;
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
					<input
						id="fold-target"
						name="fold-target"
						type="text"
						bind:value={query}
						oninput={onInput}
						onkeydown={onKeydown}
						role="combobox"
						aria-expanded={suggestions.length > 0}
						aria-controls="fold-target-listbox"
						aria-autocomplete="list"
						aria-activedescendant={selIndex >= 0 ? `fold-target-option-${selIndex}` : undefined}
						placeholder="Type to search tags…"
						autocomplete="off"
						spellcheck="false"
						class={inputClass}
					/>
					{#if suggestions.length > 0}
						<ul
							id="fold-target-listbox"
							role="listbox"
							aria-labelledby="fold-target"
							class={listClass}
						>
							{#each suggestions as t, i (t.name)}
								<li
									id={`fold-target-option-${i}`}
									role="option"
									aria-selected={i === selIndex}
									class="{optionClass} {i === selIndex ? 'bg-clay-800' : ''}"
									onmousedown={() => pick(t.name)}
								>
									{t.name}
								</li>
							{/each}
						</ul>
					{/if}
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
