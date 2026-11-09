<script>
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { api } from '#lib/api.js';
	import { BTN_BASE } from '#lib/icons.js';
	import Icon from '#lib/components/Icon.svelte';
	import ListTable from '#lib/components/ListTable.svelte';
	import {
		createListQuery,
		effectiveUrl,
		toggleSortState,
		writeRouteParams
	} from '#lib/list/listQuery.svelte.js';
	import { QueryDraft } from '#lib/list/queryDraft.svelte.js';
	import Modal from '#lib/components/Modal.svelte';
	import { confirmStore } from '#lib/stores/confirmStore.svelte.js';
	import { toastStore } from '#lib/stores/toastStore.svelte.js';
	import * as authStore from '#lib/stores/authStore.svelte.js';
	import { peopleQuery } from './query.js';

	let activeTab = $derived(effectiveUrl().searchParams.get('tab') || 'people');

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

	let savingPerson = $state(false);
	let savingPersonType = $state(false);

	async function fetchPeople({ limit, offset, query, signal }) {
		const data = await api.people.list(query.q, limit, offset, signal);
		if (!data) throw new Error('Failed to load people');
		return { items: data.results ?? [], total: data.total ?? null };
	}

	const people = createListQuery({
		fetcher: fetchPeople,
		codec: peopleQuery,
		sortKeys: ['name', 'name_native', 'document_count'],
		keyField: 'id'
	});

	const draft = new QueryDraft(people);

	function sortByColumn(key) {
		people.update(toggleSortState(people.query, key));
	}

	const columns = [
		{ key: 'name', label: 'Name', sortable: true, width: '100%' },
		{ key: 'name_native', label: 'Native Name', sortable: false },
		{
			key: 'document_count',
			label: 'Documents',
			sortable: true,
			width: '100px'
		},
		{
			key: 'actions',
			label: 'Actions',
			sortable: false,
			cellClass: 'whitespace-nowrap'
		}
	];

	function filterByPerson(row) {
		const params = new URLSearchParams({ person: `author:${row.name}` });
		goto(resolve(`documents?${params.toString()}`));
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
		savingPerson = true;
		const body = { name, name_native: personFormNameNative.trim() || '' };
		let result;
		if (editingPerson) {
			result = await api.people.update(editingPerson.id, body);
		} else {
			result = await api.people.create(body);
		}
		savingPerson = false;
		if (result.ok) {
			showPeopleModal = false;
			people.reload();
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
		people.reload();
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
		savingPersonType = true;
		const body = { name, description: personTypeFormDescription.trim() || '' };
		let result;
		if (editingPersonType) {
			result = await api.peopleTypes.update(editingPersonType.id, body);
		} else {
			result = await api.peopleTypes.create(body);
		}
		savingPersonType = false;
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
		writeRouteParams(['tab'], [['tab', tab]]);
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
	<div class="flex items-center gap-4 border-b border-clay-800" role="tablist">
		<button
			role="tab"
			aria-selected={activeTab === 'people'}
			class="px-1 pb-2 text-sm font-medium transition-colors focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none {activeTab ===
			'people'
				? 'border-b-2 border-gold-500 text-parchment-200'
				: 'text-parchment-500 hover:text-parchment-200'}"
			onclick={() => switchTab('people')}
		>
			People
		</button>
		<button
			role="tab"
			aria-selected={activeTab === 'types'}
			class="px-1 pb-2 text-sm font-medium transition-colors focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none {activeTab ===
			'types'
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
				<h2 class="text-xl font-semibold text-balance text-parchment-200">People</h2>
				{#if !authStore.authEnabled() || authStore.isEditor()}
					<button
						onclick={openNewPerson}
						class="rounded-lg bg-gold-500 px-4 py-2 text-sm font-medium text-clay-950 hover:bg-gold-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
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
					bind:value={draft.value}
					oninput={draft.markEdited}
					onfocus={draft.focus}
					onblur={draft.blur}
					name="people-filter"
					autocomplete="off"
					placeholder="Filter people…"
					class="w-full max-w-xs rounded-md border border-clay-700 bg-clay-900 px-3 py-1.5 text-sm text-parchment-200 placeholder-parchment-600 focus:border-gold-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-gold-500"
				/>
			</div>

			{#snippet cell(col, row)}
				{#if col.key === 'document_count'}
					{String(row.document_count ?? 0)}
				{:else if col.key === 'actions'}
					{#if !authStore.authEnabled() || authStore.isEditor()}
						<div class="flex gap-1">
							<button
								onclick={(e) => {
									e.stopPropagation();
									openEditPerson(row);
								}}
								title="Edit"
								aria-label="Edit person"
								class="{BTN_BASE} text-parchment-400 hover:text-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							>
								<Icon name="edit" />
							</button>
							<button
								onclick={(e) => {
									e.stopPropagation();
									handleDeletePerson(row);
								}}
								title="Delete"
								aria-label="Delete person"
								class="{BTN_BASE} text-parchment-400 hover:text-terracotta-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							>
								<Icon name="delete" />
							</button>
						</div>
					{/if}
				{:else}
					{row[col.key] ?? ''}
				{/if}
			{/snippet}

			<ListTable
				{columns}
				{cell}
				rows={people.rows}
				total={people.total}
				loading={people.loading}
				error={people.error}
				page={people.query.page}
				pageSize={people.query.pageSize}
				pageSizes={[10, 25, 50, 100]}
				sortBy={people.query.sortBy}
				sortOrder={people.query.sortOrder}
				keyField="id"
				title=""
				onSort={sortByColumn}
				onPage={(next) => people.update({ page: next })}
				onPageSize={(size) => people.update({ pageSize: size })}
				onRowClick={filterByPerson}
			/>
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
						<p class="text-sm text-terracotta-500" aria-live="polite">{personError}</p>
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
							disabled={savingPerson}
							class="rounded-md bg-gold-500 px-3 py-1.5 text-xs font-medium text-clay-950 hover:bg-gold-600 disabled:opacity-50"
						>
							{savingPerson ? 'Saving…' : editingPerson ? 'Save' : 'Create'}
						</button>
					</div>
				</div>
			</form>
		</Modal>
	{:else}
		<div class="space-y-4">
			<div class="flex items-center justify-between">
				<h2 class="text-xl font-semibold text-balance text-parchment-200">Person Types</h2>
				{#if !authStore.authEnabled() || authStore.isEditor()}
					<button
						onclick={openNewPersonType}
						class="rounded-lg bg-gold-500 px-4 py-2 text-sm font-medium text-clay-950 hover:bg-gold-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					>
						New Person Type
					</button>
				{/if}
			</div>

			{#if personTypeError}
				<p class="text-sm text-terracotta-500" aria-live="polite">{personTypeError}</p>
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
													class="{BTN_BASE} text-parchment-400 hover:text-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
												>
													<Icon name="edit" />
												</button>
												<button
													onclick={(e) => {
														e.stopPropagation();
														handleDeletePersonType(pt);
													}}
													title="Delete"
													aria-label="Delete person type"
													class="{BTN_BASE} text-parchment-400 hover:text-terracotta-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
												>
													<Icon name="delete" />
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
							disabled={savingPersonType}
							class="rounded-md bg-gold-500 px-3 py-1.5 text-xs font-medium text-clay-950 hover:bg-gold-600 disabled:opacity-50"
						>
							{savingPersonType ? 'Saving…' : editingPersonType ? 'Save' : 'Create'}
						</button>
					</div>
				</div>
			</form>
		</Modal>
	{/if}
</div>
