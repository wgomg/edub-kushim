<script>
	import { goto } from '$app/navigation';
	import { filterStore } from '$lib/stores/filterStore.js';
	import { defaultFilter } from '$lib/stores/searchFilter.js';
	import { onMount } from 'svelte';
	import { api } from '$lib/api';
	import { EDIT_ICON, DELETE_ICON, BTN_BASE, actionButton } from '$lib/icons.js';
	import { escapeHtml } from '$lib/utils/html.js';
	import DataTable from '$lib/components/DataTable.svelte';
	import Modal from '$lib/components/Modal.svelte';
	import { confirmStore } from '$lib/stores/confirmStore.svelte.js';
	import { toastStore } from '$lib/stores/toastStore.svelte.js';
	import * as authStore from '$lib/stores/authStore.js';

	let activeTab = $state(new URL(window.location.href).searchParams.get('tab') || 'people');

	let showPeopleModal = $state(false);
	let editingPerson = $state(null);
	let personFormName = $state('');
	let personFormNameNative = $state('');
	let personError = $state('');

	let personTypes = $state([]);
	let showPersonTypesModal = $state(false);
	let editingPersonType = $state(null);
	let personTypeFormName = $state('');
	let personTypeFormDescription = $state('');
	let personTypeError = $state('');

	let query = $state('');
	let refreshKey = $state(0);

	const columns = [
		{ key: 'name', label: 'Name', sortable: true, width: '100%' },
		{ key: 'name_native', label: 'Native Name', sortable: false },
		{
			key: 'document_count',
			label: 'Documents',
			sortable: true,
			cell: (v) => String(v ?? 0),
			width: '100px'
		},
		{
			key: 'actions',
			label: 'Actions',
			sortable: false,
			noUnderline: true,
			cellClass: 'whitespace-nowrap',
			cell: (_v, row) => {
				if (!authStore.authEnabled() || !authStore.isEditor()) return '';
				const safeName = escapeHtml(row.name);
				const safeNative = escapeHtml(row.name_native || '');
				return `${actionButton(EDIT_ICON, 'Edit', 'text-parchment-400 hover:text-gold-500', { 'data-edit-person': row.id, 'data-person-name': safeName, 'data-person-native': safeNative })}
${actionButton(DELETE_ICON, 'Delete', 'text-parchment-400 hover:text-terracotta-500', { 'data-delete-person': row.id, 'data-person-name': safeName })}`;
			}
		}
	];

	function filterByPerson(row) {
		filterStore.set({ ...defaultFilter, people: [{ name: row.name, type: 'author' }] });
		goto('/documents');
	}

	async function fetch({ limit, offset }) {
		return await api.people.list(query, limit, offset);
	}

	onMount(() => {
		loadPersonTypes();
	});

	async function loadPersonTypes() {
		personTypes = await api.peopleTypes.list();
	}

	function openNewPerson() {
		editingPerson = null;
		personFormName = '';
		personFormNameNative = '';
		personError = '';
		showPeopleModal = true;
	}

	function openEditPerson(p) {
		editingPerson = p;
		personFormName = p.name;
		personFormNameNative = p.name_native || '';
		personError = '';
		showPeopleModal = true;
	}

	async function savePerson() {
		personError = '';
		const name = personFormName.trim();
		if (!name) {
			personError = 'Name is required';
			return;
		}
		const body = { name, name_native: personFormNameNative.trim() || '' };
		let result;
		if (editingPerson) {
			result = await api.people.update(editingPerson.id, body);
		} else {
			result = await api.people.create(body);
		}
		if (result.ok) {
			showPeopleModal = false;
			refreshKey++;
		} else {
			toastStore.error('Failed to save person');
		}
	}

	async function handleDeletePerson(p) {
		const ok = await confirmStore.confirm({
			title: 'Delete person',
			message: `Remove "${p.name}" from people? This will also remove this person from all documents.`,
			danger: true
		});
		if (!ok) return;
		await api.people.delete(p.id);
		refreshKey++;
	}

	function handlePageClick(e) {
		const editBtn = e.target.closest('[data-edit-person]');
		if (editBtn) {
			const id = parseInt(editBtn.getAttribute('data-edit-person'));
			const name = editBtn.getAttribute('data-person-name');
			const nameNative = editBtn.getAttribute('data-person-native') || '';
			openEditPerson({ id, name, name_native: nameNative });
			return;
		}
		const deleteBtn = e.target.closest('[data-delete-person]');
		if (deleteBtn) {
			const id = parseInt(deleteBtn.getAttribute('data-delete-person'));
			const name = deleteBtn.getAttribute('data-person-name');
			handleDeletePerson({ id, name });
			return;
		}
	}

	function openNewPersonType() {
		editingPersonType = null;
		personTypeFormName = '';
		personTypeFormDescription = '';
		personTypeError = '';
		showPersonTypesModal = true;
	}

	function openEditPersonType(pt) {
		editingPersonType = pt;
		personTypeFormName = pt.name;
		personTypeFormDescription = pt.description || '';
		personTypeError = '';
		showPersonTypesModal = true;
	}

	async function savePersonType() {
		personTypeError = '';
		const name = personTypeFormName.trim();
		if (!name) {
			personTypeError = 'Name is required';
			return;
		}
		const body = { name, description: personTypeFormDescription.trim() || '' };
		let result;
		if (editingPersonType) {
			result = await api.peopleTypes.update(editingPersonType.id, body);
		} else {
			result = await api.peopleTypes.create(body);
		}
		if (result.ok) {
			showPersonTypesModal = false;
			await loadPersonTypes();
		} else if (result.status === 409) {
			personTypeError = 'Person type already exists';
		} else {
			toastStore.error('Failed to save person type');
		}
	}

	function switchTab(tab) {
		activeTab = tab;
		const url = new URL(window.location.href);
		url.searchParams.set('tab', tab);
		goto(url.pathname + url.search, { replaceState: true, keepFocus: true });
	}

	async function handleDeletePersonType(pt) {
		const ok = await confirmStore.confirm({
			title: 'Delete person type',
			message: `Delete person type "${pt.name}"?`,
			danger: true
		});
		if (!ok) return;
		const result = await api.peopleTypes.delete(pt.id);
		if (result.ok) {
			await loadPersonTypes();
		} else if (result.status === 409) {
			toastStore.error('Person type is in use by people — remove/reassign first.');
		} else {
			await loadPersonTypes();
		}
	}
</script>

<div class="space-y-4">
	<div class="flex items-center gap-4 border-b border-clay-800">
		<button
			class="px-1 pb-2 text-sm font-medium transition-colors {activeTab === 'people'
				? 'border-b-2 border-gold-500 text-parchment-200'
				: 'text-parchment-500 hover:text-parchment-200'}"
			onclick={() => switchTab('people')}
		>
			People
		</button>
		<button
			class="px-1 pb-2 text-sm font-medium transition-colors {activeTab === 'types'
				? 'border-b-2 border-gold-500 text-parchment-200'
				: 'text-parchment-500 hover:text-parchment-200'}"
			onclick={() => switchTab('types')}
		>
			Person Types
		</button>
	</div>

	{#if activeTab === 'people'}
		<div class="space-y-4">
			<div class="flex items-center justify-between">
				<h2 class="text-xl font-semibold text-parchment-200">People</h2>
				{#if !authStore.authEnabled() || authStore.isEditor()}
					<button
						onclick={openNewPerson}
						class="rounded-lg bg-gold-500 px-4 py-2 text-sm font-medium text-clay-950 hover:bg-gold-600"
					>
						New Person
					</button>
				{/if}
			</div>

			<div class="flex items-center gap-2">
				<label for="people-filter" class="sr-only">Filter people</label>
				<input
					id="people-filter"
					type="text"
					bind:value={query}
					oninput={() => refreshKey++}
					name="people-filter"
					placeholder="Filter people…"
					class="w-full max-w-xs rounded-md border border-clay-700 bg-clay-900 px-3 py-1.5 text-sm text-parchment-200 placeholder-parchment-600 focus:border-gold-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-gold-500"
				/>
			</div>

			<div onclick={handlePageClick} onkeydown={() => {}} role="presentation">
				<DataTable
					{columns}
					{fetch}
					title=""
					defaultPageSize={50}
					pageSizes={[10, 25, 50, 100]}
					{refreshKey}
					onRowClick={filterByPerson}
				/>
			</div>
		</div>

		<Modal
			open={showPeopleModal}
			title={editingPerson ? 'Edit Person' : 'New Person'}
			onClose={() => (showPeopleModal = false)}
		>
			<form
				onsubmit={(e) => {
					e.preventDefault();
					savePerson();
				}}
			>
				<div class="space-y-4">
					<div>
						<label for="person-name" class="mb-1 block text-xs font-medium text-parchment-400"
							>Name</label
						>
						<input
							id="person-name"
							name="person-name"
							type="text"
							bind:value={personFormName}
							placeholder="Person name…"
							class="w-full rounded-md border border-clay-700 bg-clay-900 px-3 py-1.5 text-sm text-parchment-200 placeholder-parchment-600 focus:border-gold-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-gold-500"
						/>
					</div>
					<div>
						<label
							for="person-native-name"
							class="mb-1 block text-xs font-medium text-parchment-400">Native Name</label
						>
						<input
							id="person-native-name"
							name="person-native-name"
							type="text"
							bind:value={personFormNameNative}
							placeholder="Native name (optional)…"
							class="w-full rounded-md border border-clay-700 bg-clay-900 px-3 py-1.5 text-sm text-parchment-200 placeholder-parchment-600 focus:border-gold-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-gold-500"
						/>
					</div>
					{#if personError}
						<p class="text-sm text-terracotta-500">{personError}</p>
					{/if}
					<div class="flex justify-end gap-2">
						<button
							type="button"
							onclick={() => (showPeopleModal = false)}
							class="rounded-md px-3 py-1.5 text-xs font-medium text-parchment-400 hover:bg-clay-800"
						>
							Cancel
						</button>
						<button
							type="submit"
							disabled={!personFormName.trim()}
							class="rounded-md bg-gold-500 px-3 py-1.5 text-xs font-medium text-clay-950 hover:bg-gold-600 disabled:opacity-50"
						>
							{editingPerson ? 'Save' : 'Create'}
						</button>
					</div>
				</div>
			</form>
		</Modal>
	{:else}
		<div class="space-y-4">
			<div class="flex items-center justify-between">
				<h2 class="text-xl font-semibold text-parchment-200">Person Types</h2>
				{#if !authStore.authEnabled() || authStore.isEditor()}
					<button
						onclick={openNewPersonType}
						class="rounded-lg bg-gold-500 px-4 py-2 text-sm font-medium text-clay-950 hover:bg-gold-600"
					>
						New Person Type
					</button>
				{/if}
			</div>

			{#if personTypeError}
				<p class="text-sm text-terracotta-500">{personTypeError}</p>
			{/if}

			<div class="overflow-x-auto rounded-lg border border-clay-800">
				<table class="w-full table-auto text-sm">
					<thead class="sticky top-0 bg-clay-900 text-left text-parchment-400">
						<tr>
							<th scope="col" class="px-4 py-3 font-medium whitespace-nowrap">Name</th>
							<th scope="col" class="px-4 py-3 font-medium whitespace-nowrap">Description</th>
							<th scope="col" class="w-[1%] px-4 py-3 font-medium whitespace-nowrap">Actions</th>
						</tr>
					</thead>
					<tbody class="divide-y divide-clay-800">
						{#if personTypes.length === 0}
							<tr class="bg-clay-950">
								<td class="px-4 py-8 text-parchment-500" colspan="3">No person types found.</td>
							</tr>
						{:else}
							{#each personTypes as pt (pt.id)}
								<tr class="bg-clay-950">
									<td class="px-4 py-3 text-parchment-200">{pt.name}</td>
									<td class="px-4 py-3 text-parchment-400">{pt.description || ''}</td>
									<td class="px-4 py-3 whitespace-nowrap">
										{#if !authStore.authEnabled() || authStore.isEditor()}
											<div class="flex gap-2">
												<button
													onclick={(e) => {
														e.stopPropagation();
														openEditPersonType(pt);
													}}
													title="Edit"
													aria-label="Edit person type"
													class="{BTN_BASE} text-parchment-400 hover:text-gold-500"
												>
													{@html EDIT_ICON}
												</button>
												<button
													onclick={(e) => {
														e.stopPropagation();
														handleDeletePersonType(pt);
													}}
													title="Delete"
													aria-label="Delete person type"
													class="{BTN_BASE} text-parchment-400 hover:text-terracotta-500"
												>
													{@html DELETE_ICON}
												</button>
											</div>
										{/if}
									</td>
								</tr>
							{/each}
						{/if}
					</tbody>
				</table>
			</div>
		</div>

		<Modal
			open={showPersonTypesModal}
			title={editingPersonType ? 'Edit Person Type' : 'New Person Type'}
			onClose={() => (showPersonTypesModal = false)}
		>
			<form
				onsubmit={(e) => {
					e.preventDefault();
					savePersonType();
				}}
			>
				<div class="space-y-4">
					<div>
						<label for="pt-name" class="mb-1 block text-xs font-medium text-parchment-400"
							>Name</label
						>
						<input
							id="pt-name"
							name="pt-name"
							type="text"
							bind:value={personTypeFormName}
							placeholder="Person type name…"
							class="w-full rounded-md border border-clay-700 bg-clay-900 px-3 py-1.5 text-sm text-parchment-200 placeholder-parchment-600 focus:border-gold-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-gold-500"
						/>
					</div>
					<div>
						<label for="pt-description" class="mb-1 block text-xs font-medium text-parchment-400"
							>Description</label
						>
						<input
							id="pt-description"
							name="pt-description"
							type="text"
							bind:value={personTypeFormDescription}
							placeholder="Description (optional)…"
							class="w-full rounded-md border border-clay-700 bg-clay-900 px-3 py-1.5 text-sm text-parchment-200 placeholder-parchment-600 focus:border-gold-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-gold-500"
						/>
					</div>
					<div class="flex justify-end gap-2">
						<button
							type="button"
							onclick={() => (showPersonTypesModal = false)}
							class="rounded-md px-3 py-1.5 text-xs font-medium text-parchment-400 hover:bg-clay-800"
						>
							Cancel
						</button>
						<button
							type="submit"
							disabled={!personTypeFormName.trim()}
							class="rounded-md bg-gold-500 px-3 py-1.5 text-xs font-medium text-clay-950 hover:bg-gold-600 disabled:opacity-50"
						>
							{editingPersonType ? 'Save' : 'Create'}
						</button>
					</div>
				</div>
			</form>
		</Modal>
	{/if}
</div>
