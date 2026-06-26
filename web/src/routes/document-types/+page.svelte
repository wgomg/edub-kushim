<script>
	import { onMount } from 'svelte';
	import { api } from '$lib/api';
	import { EDIT_ICON, DELETE_ICON, BTN_BASE } from '$lib/icons.js';
	import Modal from '$lib/components/Modal.svelte';
	import { confirmStore } from '$lib/stores/confirmStore.svelte.js';
	import { toastStore } from '$lib/stores/toastStore.svelte.js';

	let documentTypes = $state([]);
	let showModal = $state(false);
	let editing = $state(null);
	let formName = $state('');
	let formDescription = $state('');
	let error = $state('');

	onMount(() => load());

	async function load() {
		documentTypes = await api.documentTypes.list();
	}

	function openNew() {
		editing = null;
		formName = '';
		formDescription = '';
		error = '';
		showModal = true;
	}

	function openEdit(dt) {
		editing = dt;
		formName = dt.name;
		formDescription = dt.description || '';
		error = '';
		showModal = true;
	}

	async function save() {
		error = '';
		const name = formName.trim();
		if (!name) {
			error = 'Name is required';
			return;
		}
		const body = { name, description: formDescription.trim() || '' };
		let result;
		if (editing) {
			result = await api.documentTypes.update(editing.id, body);
		} else {
			result = await api.documentTypes.create(body);
		}
		if (result.ok) {
			showModal = false;
			await load();
		} else if (result.status === 409) {
			error = 'Document type already exists';
		} else {
			toastStore.error('Failed to save document type');
		}
	}

	async function handleDelete(dt) {
		const ok = await confirmStore.confirm({
			title: 'Delete document type',
			message: `Delete document type "${dt.name}"?`,
			danger: true
		});
		if (!ok) return;
		const result = await api.documentTypes.delete(dt.id);
		if (result.ok) {
			await load();
		} else if (result.status === 409) {
			toastStore.error('Document type is in use by documents — remove/reassign first.');
		} else {
			await load();
		}
	}
</script>

<div class="space-y-4">
	<div class="flex items-center justify-between">
		<h1 class="text-2xl font-semibold text-parchment-200">Document Types</h1>
		<button
			onclick={openNew}
			class="rounded-lg bg-gold-500 px-4 py-2 text-sm font-medium text-clay-950 hover:bg-gold-600"
		>
			New Document Type
		</button>
	</div>

	{#if error}
		<p class="text-sm text-terracotta-500">{error}</p>
	{/if}

	<div class="overflow-x-auto rounded-lg border border-clay-800">
		<table class="w-full table-auto text-sm">
			<thead class="sticky top-0 bg-clay-900 text-left text-parchment-400">
				<tr>
					<th class="px-4 py-3 font-medium whitespace-nowrap">Name</th>
					<th class="px-4 py-3 font-medium whitespace-nowrap">Description</th>
					<th class="w-[1%] px-4 py-3 font-medium whitespace-nowrap">Actions</th>
				</tr>
			</thead>
			<tbody class="divide-y divide-clay-800">
				{#if documentTypes.length === 0}
					<tr class="bg-clay-950">
						<td class="px-4 py-8 text-parchment-500" colspan="3">No document types found.</td>
					</tr>
				{:else}
					{#each documentTypes as dt (dt.id)}
						<tr class="bg-clay-950">
							<td class="px-4 py-3 text-parchment-200">{dt.name}</td>
							<td class="px-4 py-3 text-parchment-400">{dt.description || ''}</td>
							<td class="px-4 py-3 whitespace-nowrap">
								<div class="flex gap-2">
									<button
										onclick={() => openEdit(dt)}
										title="Edit"
										class="{BTN_BASE} text-parchment-400 hover:text-gold-500"
									>
										{@html EDIT_ICON}
									</button>
									<button
										onclick={() => handleDelete(dt)}
										title="Delete"
										class="{BTN_BASE} text-parchment-400 hover:text-terracotta-500"
									>
										{@html DELETE_ICON}
									</button>
								</div>
							</td>
						</tr>
					{/each}
				{/if}
			</tbody>
		</table>
	</div>
</div>

<Modal
	open={showModal}
	title={editing ? 'Edit Document Type' : 'New Document Type'}
	onClose={() => (showModal = false)}
>
	<form
		onsubmit={(e) => {
			e.preventDefault();
			save();
		}}
	>
		<div class="space-y-4">
			<div>
				<label for="dt-name" class="mb-1 block text-xs font-medium text-parchment-400">Name</label>
				<input
					id="dt-name"
					type="text"
					bind:value={formName}
					placeholder="Document type name"
					class="border-clay-700 placeholder-parchment-600 w-full rounded-md border bg-clay-900 px-3 py-1.5 text-sm text-parchment-200 focus:border-gold-500 focus:ring-0 focus:outline-none"
				/>
			</div>
			<div>
				<label for="dt-description" class="mb-1 block text-xs font-medium text-parchment-400"
					>Description</label
				>
				<input
					id="dt-description"
					type="text"
					bind:value={formDescription}
					placeholder="Description (optional)"
					class="border-clay-700 placeholder-parchment-600 w-full rounded-md border bg-clay-900 px-3 py-1.5 text-sm text-parchment-200 focus:border-gold-500 focus:ring-0 focus:outline-none"
				/>
			</div>
			<div class="flex justify-end gap-2">
				<button
					type="button"
					onclick={() => (showModal = false)}
					class="rounded-md px-3 py-1.5 text-xs font-medium text-parchment-400 hover:bg-clay-800"
				>
					Cancel
				</button>
				<button
					type="submit"
					disabled={!formName.trim()}
					class="rounded-md bg-gold-500 px-3 py-1.5 text-xs font-medium text-clay-950 hover:bg-gold-600 disabled:opacity-50"
				>
					{editing ? 'Save' : 'Create'}
				</button>
			</div>
		</div>
	</form>
</Modal>
