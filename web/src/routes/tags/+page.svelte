<script>
	import { onMount } from 'svelte';
	import { api } from '$lib/api';
	import Modal from '$lib/components/Modal.svelte';
	import { confirmStore } from '$lib/stores/confirmStore.svelte.js';
	import { toastStore } from '$lib/stores/toastStore.svelte.js';

	let tags = $state([]);
	let showModal = $state(false);
	let editingTag = $state(null);
	let formName = $state('');
	let error = $state('');
	let query = $state('');

	onMount(() => load());

	async function load() {
		tags = await api.tags.list(query);
	}

	function openNew() {
		editingTag = null;
		formName = '';
		error = '';
		showModal = true;
	}

	function openEdit(tag) {
		editingTag = tag;
		formName = tag.name;
		error = '';
		showModal = true;
	}

	async function save() {
		error = '';
		const name = formName.trim();
		if (!name) {
			error = 'Tag name is required';
			return;
		}
		let result;
		if (editingTag) {
			result = await api.tags.update(editingTag.id, name);
		} else {
			result = await api.tags.create(name);
		}
		if (result.ok) {
			showModal = false;
			await load();
		} else if (result.status === 409) {
			error = 'Tag already exists';
		} else {
			toastStore.error('Failed to save tag');
		}
	}

	async function handleDelete(tag) {
		const ok = await confirmStore.confirm({
			title: 'Delete tag',
			message: `Delete tag "${tag.name}"?`,
			danger: true
		});
		if (!ok) return;
		await api.tags.delete(tag.id);
		await load();
	}
</script>

<div class="space-y-4">
	<div class="flex items-center justify-between">
		<h1 class="text-2xl font-semibold text-parchment-200">Tags</h1>
		<button
			onclick={openNew}
			class="rounded-lg bg-gold-500 px-4 py-2 text-sm font-medium text-clay-950 hover:bg-gold-600"
		>
			New Tag
		</button>
	</div>

	<div class="flex items-center gap-2">
		<input
			type="text"
			bind:value={query}
			oninput={() => load()}
			placeholder="Filter tags…"
			class="border-clay-700 placeholder-parchment-600 w-full max-w-xs rounded-md border bg-clay-900 px-3 py-1.5 text-sm text-parchment-200 focus:border-gold-500 focus:ring-0 focus:outline-none"
		/>
	</div>

	<div class="overflow-x-auto rounded-lg border border-clay-800">
		<table class="w-full table-auto text-sm">
			<thead class="sticky top-0 bg-clay-900 text-left text-parchment-400">
				<tr>
					<th class="px-4 py-3 font-medium whitespace-nowrap">Name</th>
					<th class="w-40 px-4 py-3 font-medium whitespace-nowrap">Actions</th>
				</tr>
			</thead>
			<tbody class="divide-y divide-clay-800">
				{#if tags.length === 0}
					<tr class="bg-clay-950">
						<td class="px-4 py-8 text-parchment-500" colspan="2">No tags found.</td>
					</tr>
				{:else}
					{#each tags as tag (tag.id)}
						<tr class="bg-clay-950">
							<td class="px-4 py-3 text-parchment-200">{tag.name}</td>
							<td class="px-4 py-3">
								<div class="flex gap-2">
									<button
										onclick={() => openEdit(tag)}
										class="rounded-md px-2 py-1 text-xs font-medium text-parchment-400 hover:bg-clay-800"
									>
										Edit
									</button>
									<button
										onclick={() => handleDelete(tag)}
										class="rounded-md px-2 py-1 text-xs font-medium text-terracotta-500 hover:bg-clay-800"
									>
										Delete
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

<Modal open={showModal} title={editingTag ? 'Edit Tag' : 'New Tag'} onClose={() => (showModal = false)}>
	<form
		onsubmit={(e) => {
			e.preventDefault();
			save();
		}}
	>
		<div class="space-y-4">
			<div>
				<label for="tag-name" class="mb-1 block text-xs font-medium text-parchment-400">Name</label>
				<input
					id="tag-name"
					type="text"
					bind:value={formName}
					placeholder="Tag name"
					class="border-clay-700 placeholder-parchment-600 w-full rounded-md border bg-clay-900 px-3 py-1.5 text-sm text-parchment-200 focus:border-gold-500 focus:ring-0 focus:outline-none"
				/>
			</div>
			{#if error}
				<p class="text-sm text-terracotta-500">{error}</p>
			{/if}
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
					{editingTag ? 'Save' : 'Create'}
				</button>
			</div>
		</div>
	</form>
</Modal>
