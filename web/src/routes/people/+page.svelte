<script>
	import { onMount } from 'svelte';
	import { api } from '$lib/api';
	import Modal from '$lib/components/Modal.svelte';
	import { confirmStore } from '$lib/stores/confirmStore.svelte.js';
	import { toastStore } from '$lib/stores/toastStore.svelte.js';

	let activeTab = $state('people');

	let people = $state([]);
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

	onMount(() => {
		loadPeople();
		loadPersonTypes();
	});

	async function loadPeople() {
		people = await api.people.list();
	}

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
			await loadPeople();
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
		await loadPeople();
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
			onclick={() => (activeTab = 'people')}
		>
			People
		</button>
		<button
			class="px-1 pb-2 text-sm font-medium transition-colors {activeTab === 'types'
				? 'border-b-2 border-gold-500 text-parchment-200'
				: 'text-parchment-500 hover:text-parchment-200'}"
			onclick={() => (activeTab = 'types')}
		>
			Person Types
		</button>
	</div>

	{#if activeTab === 'people'}
		<div class="space-y-4">
			<div class="flex items-center justify-between">
				<h2 class="text-xl font-semibold text-parchment-200">People</h2>
				<button
					onclick={openNewPerson}
					class="rounded-lg bg-gold-500 px-4 py-2 text-sm font-medium text-clay-950 hover:bg-gold-600"
				>
					New Person
				</button>
			</div>

			<div class="overflow-x-auto rounded-lg border border-clay-800">
				<table class="w-full table-auto text-sm">
					<thead class="sticky top-0 bg-clay-900 text-left text-parchment-400">
						<tr>
							<th class="px-4 py-3 font-medium whitespace-nowrap">Name</th>
							<th class="px-4 py-3 font-medium whitespace-nowrap">Native Name</th>
							<th class="w-40 px-4 py-3 font-medium whitespace-nowrap">Actions</th>
						</tr>
					</thead>
					<tbody class="divide-y divide-clay-800">
						{#if people.length === 0}
							<tr class="bg-clay-950">
								<td class="px-4 py-8 text-parchment-500" colspan="3">No people found.</td>
							</tr>
						{:else}
							{#each people as p (p.id)}
								<tr class="bg-clay-950">
									<td class="px-4 py-3 text-parchment-200">{p.name}</td>
									<td class="px-4 py-3 text-parchment-400">{p.name_native || ''}</td>
									<td class="px-4 py-3">
										<div class="flex gap-2">
											<button
												onclick={() => openEditPerson(p)}
												class="rounded-md px-2 py-1 text-xs font-medium text-parchment-400 hover:bg-clay-800"
											>
												Edit
											</button>
											<button
												onclick={() => handleDeletePerson(p)}
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
							type="text"
							bind:value={personFormName}
							placeholder="Person name"
							class="border-clay-700 placeholder-parchment-600 w-full rounded-md border bg-clay-900 px-3 py-1.5 text-sm text-parchment-200 focus:border-gold-500 focus:ring-0 focus:outline-none"
						/>
					</div>
					<div>
						<label
							for="person-native-name"
							class="mb-1 block text-xs font-medium text-parchment-400">Native Name</label
						>
						<input
							id="person-native-name"
							type="text"
							bind:value={personFormNameNative}
							placeholder="Native name (optional)"
							class="border-clay-700 placeholder-parchment-600 w-full rounded-md border bg-clay-900 px-3 py-1.5 text-sm text-parchment-200 focus:border-gold-500 focus:ring-0 focus:outline-none"
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
				<button
					onclick={openNewPersonType}
					class="rounded-lg bg-gold-500 px-4 py-2 text-sm font-medium text-clay-950 hover:bg-gold-600"
				>
					New Person Type
				</button>
			</div>

			{#if personTypeError}
				<p class="text-sm text-terracotta-500">{personTypeError}</p>
			{/if}

			<div class="overflow-x-auto rounded-lg border border-clay-800">
				<table class="w-full table-auto text-sm">
					<thead class="sticky top-0 bg-clay-900 text-left text-parchment-400">
						<tr>
							<th class="px-4 py-3 font-medium whitespace-nowrap">Name</th>
							<th class="px-4 py-3 font-medium whitespace-nowrap">Description</th>
							<th class="w-40 px-4 py-3 font-medium whitespace-nowrap">Actions</th>
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
									<td class="px-4 py-3">
										<div class="flex gap-2">
											<button
												onclick={() => openEditPersonType(pt)}
												class="rounded-md px-2 py-1 text-xs font-medium text-parchment-400 hover:bg-clay-800"
											>
												Edit
											</button>
											<button
												onclick={() => handleDeletePersonType(pt)}
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
							type="text"
							bind:value={personTypeFormName}
							placeholder="Person type name"
							class="border-clay-700 placeholder-parchment-600 w-full rounded-md border bg-clay-900 px-3 py-1.5 text-sm text-parchment-200 focus:border-gold-500 focus:ring-0 focus:outline-none"
						/>
					</div>
					<div>
						<label for="pt-description" class="mb-1 block text-xs font-medium text-parchment-400"
							>Description</label
						>
						<input
							id="pt-description"
							type="text"
							bind:value={personTypeFormDescription}
							placeholder="Description (optional)"
							class="border-clay-700 placeholder-parchment-600 w-full rounded-md border bg-clay-900 px-3 py-1.5 text-sm text-parchment-200 focus:border-gold-500 focus:ring-0 focus:outline-none"
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
