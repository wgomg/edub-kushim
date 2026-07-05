<script>
	import DataTable from '$lib/components/DataTable.svelte';
	import Modal from '$lib/components/Modal.svelte';
	import { EDIT_ICON, DELETE_ICON, actionButton } from '$lib/icons.js';
	import { escapeHtml } from '$lib/utils/html.js';
	import { confirmStore } from '$lib/stores/confirmStore.svelte.js';
	import { toastStore } from '$lib/stores/toastStore.svelte.js';
	import { api } from '$lib/api';
	import * as authStore from '$lib/stores/authStore.js';

	let showModal = $state(false);
	let editingTag = $state(null);
	let formName = $state('');
	let error = $state('');
	let query = $state('');
	let refreshKey = $state(0);

	const columns = [
		{
			key: 'name',
			label: 'Name',
			sortable: true,
			width: '100%'
		},
		{
			key: 'actions',
			label: 'Actions',
			sortable: false,
			cellClass: 'whitespace-nowrap',
			cell: (_v, row) => {
				if (!authStore.authEnabled() || !authStore.isEditor()) return '';
				const safeName = escapeHtml(row.name);
				return `${actionButton(EDIT_ICON, 'Edit', 'text-parchment-400 hover:text-gold-500', { 'data-edit-tag': row.id, 'data-tag-name': safeName })}
${actionButton(DELETE_ICON, 'Delete', 'text-parchment-400 hover:text-terracotta-500', { 'data-delete-tag': row.id, 'data-tag-name': safeName })}`;
			}
		}
	];

	async function fetch({ limit, offset }) {
		return await api.tags.list(query, limit, offset);
	}

	function openNew() {
		editingTag = null;
		formName = '';
		error = '';
		showModal = true;
	}

	function openEdit(tagId, tagName) {
		editingTag = { id: tagId, name: tagName };
		formName = tagName;
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
			refreshKey++;
		} else if (result.status === 409) {
			error = 'Tag already exists';
		} else {
			toastStore.error('Failed to save tag');
		}
	}

	async function handleDelete(tagId, tagName) {
		const ok = await confirmStore.confirm({
			title: 'Delete tag',
			message: `Delete tag "${tagName}"?`,
			danger: true
		});
		if (!ok) return;
		await api.tags.delete(tagId);
		refreshKey++;
	}

	function handlePageClick(e) {
		const editBtn = e.target.closest('[data-edit-tag]');
		if (editBtn) {
			const id = parseInt(editBtn.getAttribute('data-edit-tag'));
			const name = editBtn.getAttribute('data-tag-name');
			openEdit(id, name);
			return;
		}
		const deleteBtn = e.target.closest('[data-delete-tag]');
		if (deleteBtn) {
			const id = parseInt(deleteBtn.getAttribute('data-delete-tag'));
			const name = deleteBtn.getAttribute('data-tag-name');
			handleDelete(id, name);
			return;
		}
	}
</script>

<div class="space-y-4" onclick={handlePageClick} onkeydown={() => {}} role="presentation">
	<div class="flex items-center justify-between">
		<h1 class="text-2xl font-semibold text-parchment-200">Tags</h1>
		{#if !authStore.authEnabled() || authStore.isEditor()}
			<button
				onclick={openNew}
				class="rounded-lg bg-gold-500 px-4 py-2 text-sm font-medium text-clay-950 hover:bg-gold-600"
			>
				New Tag
			</button>
		{/if}
	</div>

	<div class="flex items-center gap-2">
		<input
			type="text"
			bind:value={query}
			oninput={() => refreshKey++}
			placeholder="Filter tags…"
			class="w-full max-w-xs rounded-md border border-clay-700 bg-clay-900 px-3 py-1.5 text-sm text-parchment-200 placeholder-parchment-600 focus:border-gold-500 focus:ring-0 focus:outline-none"
		/>
	</div>

	<DataTable
		{columns}
		{fetch}
		title=""
		defaultPageSize={50}
		pageSizes={[10, 25, 50, 100]}
		{refreshKey}
	/>
</div>

<Modal
	open={showModal}
	title={editingTag ? 'Edit Tag' : 'New Tag'}
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
				<label for="tag-name" class="mb-1 block text-xs font-medium text-parchment-400">Name</label>
				<input
					id="tag-name"
					type="text"
					bind:value={formName}
					placeholder="Tag name"
					class="w-full rounded-md border border-clay-700 bg-clay-900 px-3 py-1.5 text-sm text-parchment-200 placeholder-parchment-600 focus:border-gold-500 focus:ring-0 focus:outline-none"
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
