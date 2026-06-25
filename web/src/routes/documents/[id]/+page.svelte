<script>
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { api } from '$lib/api';
	import { confirmStore } from '$lib/stores/confirmStore.svelte.js';
	import { toastStore } from '$lib/stores/toastStore.svelte.js';

	let { params } = $props();

	let doc = $state();

	let documentTypes = $state([]);
	let peopleTypes = $state([]);

	let editTitle = $state('');
	let editDocumentTypeId = $state(1);
	let editLanguage = $state('');

	let savingMeta = $state(false);
	let deleting = $state(false);

	let tagQuery = $state('');
	let tagResults = $state([]);

	let peopleQuery = $state('');
	let peopleResults = $state([]);
	let selectedPeopleTypeId = $state(1);

	onMount(async () => {
		const [data, types, pTypes] = await Promise.all([
			api.documents.get(params.id),
			api.autocomplete.documentTypes(),
			api.autocomplete.peopleTypes()
		]);
		if (!data) {
			toastStore.error('Failed to load document');
			return;
		}
		doc = data;
		documentTypes = types;
		peopleTypes = pTypes;
		editTitle = doc.title;
		editDocumentTypeId = doc.document_type_id ?? 1;
		editLanguage = doc.language ?? '';
		if (pTypes.length > 0) {
			selectedPeopleTypeId = pTypes[0].id;
		}
	});

	function refreshDoc() {
		api.documents.get(params.id).then((data) => {
			if (data) doc = data;
		});
	}

	async function saveMetadata() {
		savingMeta = true;
		await api.documents.update(params.id, {
			title: editTitle,
			document_type_id: editDocumentTypeId,
			language: editLanguage
		});
		savingMeta = false;
		refreshDoc();
	}

	async function handleDelete() {
		const ok = await confirmStore.confirm({
			title: 'Delete document',
			message: 'Are you sure you want to delete this document? This cannot be undone.',
			danger: true
		});
		if (!ok || deleting) return;
		deleting = true;
		const res = await fetch(`/api/v1/documents/${params.id}`, { method: 'DELETE' });
		if (res.ok) {
			goto('/documents');
		} else {
			deleting = false;
			toastStore.error(`Failed to delete document: ${res.status} ${res.statusText}`);
		}
	}

	async function searchTags(q) {
		tagQuery = q;
		if (!q.trim()) {
			tagResults = [];
			return;
		}
		tagResults = await api.autocomplete.tags(q);
	}

	function selectTag(tag) {
		tagQuery = '';
		tagResults = [];
		addTag(tag.id);
	}

	async function addTag(tagId) {
		await api.documents.tags.add(params.id, tagId);
		refreshDoc();
	}

	async function removeTag(tagId) {
		await api.documents.tags.remove(params.id, tagId);
		refreshDoc();
	}

	async function searchPeople(q) {
		peopleQuery = q;
		if (!q.trim()) {
			peopleResults = [];
			return;
		}
		peopleResults = await api.autocomplete.people(q);
	}

	function selectPerson(person) {
		peopleQuery = '';
		peopleResults = [];
		addPerson(person.id);
	}

	async function addPerson(personId) {
		await api.documents.people.add(params.id, personId, selectedPeopleTypeId);
		refreshDoc();
	}

	async function removePerson(personId, personTypeId) {
		await api.documents.people.remove(params.id, personId, personTypeId);
		refreshDoc();
	}
</script>

<div class="space-y-6">
	<a
		href="/documents"
		class="inline-flex items-center gap-1 text-sm text-parchment-500 hover:text-parchment-200"
	>
		&larr; Back to documents
	</a>

	{#if !doc}
		<p class="text-parchment-500">Loading…</p>
	{:else}
		<div class="flex items-start gap-6">
			<div class="min-w-0 flex-1">
				<h1 class="text-2xl font-semibold text-parchment-200">{doc.title}</h1>

				{#if doc.mime_type === 'application/pdf'}
					<div class="mt-4 overflow-hidden rounded-lg border border-clay-800">
						<iframe
							src={`/api/v1/documents/${doc.id}/file`}
							class="h-[75vh] w-full"
							title={doc.title}
						></iframe>
					</div>
				{:else}
					<p class="mt-4 text-parchment-500">Preview not available for this file type.</p>
				{/if}
			</div>

			<div class="w-80 shrink-0 space-y-4">
				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<p class="text-xs font-medium tracking-wider text-parchment-500 uppercase">
						Title / Type / Language
					</p>
					<div class="mt-2 space-y-2">
						<div>
							<label class="text-xs text-parchment-500" for="edit-title">Title</label>
							<input
								id="edit-title"
								type="text"
								bind:value={editTitle}
								class="border-clay-700 placeholder-parchment-600 mt-0.5 w-full rounded-md border bg-clay-950 px-2 py-1 text-sm text-parchment-200 focus:border-gold-500 focus:ring-0 focus:outline-none"
							/>
						</div>
						<div>
							<label class="text-xs text-parchment-500" for="edit-doctype">Type</label>
							<select
								id="edit-doctype"
								bind:value={editDocumentTypeId}
								class="border-clay-700 mt-0.5 w-full rounded-md border bg-clay-950 px-2 py-1 text-sm text-parchment-200 focus:border-gold-500 focus:ring-0 focus:outline-none"
							>
								{#each documentTypes as dt}
									<option value={dt.id}>{dt.name}</option>
								{/each}
							</select>
						</div>
						<div>
							<label class="text-xs text-parchment-500" for="edit-lang">Language</label>
							<input
								id="edit-lang"
								type="text"
								bind:value={editLanguage}
								placeholder="und"
								class="border-clay-700 placeholder-parchment-600 mt-0.5 w-full rounded-md border bg-clay-950 px-2 py-1 text-sm text-parchment-200 focus:border-gold-500 focus:ring-0 focus:outline-none"
							/>
						</div>
						<button
							onclick={saveMetadata}
							disabled={savingMeta}
							class="w-full rounded-md bg-gold-600 px-3 py-1.5 text-xs font-medium text-clay-950 hover:bg-gold-500 disabled:opacity-50"
						>
							{savingMeta ? 'Saving…' : 'Save'}
						</button>
					</div>
				</div>

				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<p class="text-xs font-medium tracking-wider text-parchment-500 uppercase">Tags</p>
					{#if (doc.tags ?? []).length > 0}
						<div class="mt-2 flex flex-wrap gap-1.5">
							{#each doc.tags as tag (tag.id)}
								<span
									class="inline-flex items-center gap-1 rounded-full bg-lapis-700 px-2 py-0.5 text-xs text-parchment-200"
								>
									{tag.name}
									<button
										onclick={() => removeTag(tag.id)}
										class="hover:text-terracotta-400 text-parchment-400">&times;</button
									>
								</span>
							{/each}
						</div>
					{:else}
						<p class="mt-1 text-parchment-500">No tags</p>
					{/if}
					<div class="relative mt-2">
						<input
							type="text"
							bind:value={tagQuery}
							oninput={() => searchTags(tagQuery)}
							placeholder="Search tags…"
							class="border-clay-700 placeholder-parchment-600 w-full rounded-md border bg-clay-950 px-2 py-1 text-sm text-parchment-200 focus:border-gold-500 focus:ring-0 focus:outline-none"
						/>
						{#if tagResults.length > 0}
							<div
								class="border-clay-700 absolute top-full right-0 left-0 z-10 mt-1 max-h-40 overflow-y-auto rounded-md border bg-clay-950 shadow-lg"
							>
								{#each tagResults as tag, i}
									<button
										onclick={() => selectTag(tag)}
										class="w-full px-2 py-1 text-left text-sm text-parchment-200 hover:bg-clay-800"
									>
										{tag.name}
									</button>
								{/each}
							</div>
						{/if}
					</div>
				</div>

				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<p class="text-xs font-medium tracking-wider text-parchment-500 uppercase">People</p>
					{#if (doc.people ?? []).length > 0}
						<div class="mt-2 space-y-2">
							{#each doc.people as person (person.id + '-' + person.person_type_id)}
								<div class="flex items-start justify-between gap-2">
									<div class="min-w-0">
										<p class="text-parchment-200">
											{person.name}
											{#if person.name_native}
												<span class="text-parchment-500"> ({person.name_native})</span>
											{/if}
										</p>
										{#if person.person_type_name}
											<p class="text-xs text-parchment-500">{person.person_type_name}</p>
										{/if}
									</div>
									<button
										onclick={() => removePerson(person.id, person.person_type_id)}
										class="hover:text-terracotta-400 shrink-0 text-parchment-500">&times;</button
									>
								</div>
							{/each}
						</div>
					{:else}
						<p class="mt-1 text-parchment-500">No people</p>
					{/if}
					<div class="relative mt-2">
						<input
							type="text"
							bind:value={peopleQuery}
							oninput={() => searchPeople(peopleQuery)}
							placeholder="Search people…"
							class="border-clay-700 placeholder-parchment-600 w-full rounded-md border bg-clay-950 px-2 py-1 text-sm text-parchment-200 focus:border-gold-500 focus:ring-0 focus:outline-none"
						/>
						{#if peopleResults.length > 0}
							<div
								class="border-clay-700 absolute top-full right-0 left-0 z-10 mt-1 max-h-40 overflow-y-auto rounded-md border bg-clay-950 shadow-lg"
							>
								{#each peopleResults as person, i}
									<button
										onclick={() => selectPerson(person)}
										class="w-full px-2 py-1 text-left text-sm text-parchment-200 hover:bg-clay-800"
									>
										{person.name}
									</button>
								{/each}
							</div>
						{/if}
						<div class="mt-2 flex gap-2">
							<select
								bind:value={selectedPeopleTypeId}
								class="border-clay-700 flex-1 rounded-md border bg-clay-950 px-2 py-1 text-xs text-parchment-200 focus:border-gold-500 focus:ring-0 focus:outline-none"
							>
								{#each peopleTypes as pt}
									<option value={pt.id}>{pt.name}</option>
								{/each}
							</select>
							<button
								onclick={() => {
									const first = peopleResults[0];
									if (first) selectPerson(first);
								}}
								disabled={peopleResults.length === 0}
								class="shrink-0 rounded-md bg-gold-600 px-2 py-1 text-xs font-medium text-clay-950 hover:bg-gold-500 disabled:opacity-50"
							>
								Add
							</button>
						</div>
					</div>
				</div>

				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<p class="text-xs font-medium tracking-wider text-parchment-500 uppercase">MIME Type</p>
					<p class="mt-1 text-parchment-200">{doc.mime_type}</p>
				</div>
				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<p class="text-xs font-medium tracking-wider text-parchment-500 uppercase">File Size</p>
					<p class="mt-1 text-parchment-200">{(doc.file_size / 1024).toFixed(0)} KB</p>
				</div>
				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<p class="text-xs font-medium tracking-wider text-parchment-500 uppercase">
						Content Stats
					</p>
					<div class="mt-2 space-y-1 text-sm">
						<div class="flex justify-between">
							<span class="text-parchment-500">Pages</span>
							<span class="text-parchment-200">{doc.page_count ?? '—'}</span>
						</div>
						<div class="flex justify-between">
							<span class="text-parchment-500">Words</span>
							<span class="text-parchment-200">{doc.word_count ?? '—'}</span>
						</div>
						<div class="flex justify-between">
							<span class="text-parchment-500">Characters</span>
							<span class="text-parchment-200">{doc.char_count ?? '—'}</span>
						</div>
					</div>
				</div>
				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<p class="text-xs font-medium tracking-wider text-parchment-500 uppercase">Created</p>
					<p class="mt-1 text-parchment-200">{new Date(doc.created_at).toLocaleString()}</p>
				</div>
				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<p class="text-xs font-medium tracking-wider text-parchment-500 uppercase">Modified</p>
					<p class="mt-1 text-parchment-200">{new Date(doc.modified_at).toLocaleString()}</p>
				</div>
				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<p class="text-xs font-medium tracking-wider text-parchment-500 uppercase">
						MD5 Checksum
					</p>
					<p class="mt-1 font-mono text-xs break-all text-parchment-400">{doc.md5_checksum}</p>
				</div>
				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<p class="text-xs font-medium tracking-wider text-parchment-500 uppercase">
						SHA‑512 Checksum
					</p>
					<p class="mt-1 font-mono text-xs break-all text-parchment-400">{doc.sha512_checksum}</p>
				</div>

				<a
					href={`/api/v1/documents/${doc.id}/file?download=true`}
					class="block w-full rounded-lg bg-gold-600 px-4 py-2 text-sm font-medium text-clay-950 text-center hover:bg-gold-500"
				>
					Download PDF
				</a>

				<button
					onclick={handleDelete}
					disabled={deleting}
					class="bg-terracotta-800 hover:bg-terracotta-700 w-full rounded-lg border border-terracotta-600 px-4 py-2 text-sm font-medium text-parchment-200 disabled:opacity-50"
				>
					{deleting ? 'Deleting…' : 'Delete Document'}
				</button>
			</div>
		</div>
	{/if}
</div>
