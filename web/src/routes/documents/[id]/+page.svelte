<script>
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { api } from '$lib/api';
	import { formatSize } from '$lib/utils/html.js';
	import { confirmStore } from '$lib/stores/confirmStore.svelte.js';
	import { toastStore } from '$lib/stores/toastStore.svelte.js';
	import * as authStore from '$lib/stores/authStore.js';
	import PdfViewer from '$lib/components/PdfViewer.svelte';
	import Icon from '$lib/components/Icon.svelte';
	let { params } = $props();

	let doc = $state(null);

	let inspectorOpen = $state(true);

	let documentTypes = $state([]);
	let peopleTypes = $state([]);

	let editTitle = $state('');
	let editDocumentTypeId = $state(1);
	let editLanguage = $state('');

	let savingMeta = $state(false);
	let deleting = $state(false);
	let reenriching = $state(false);

	let tagQuery = $state('');
	let tagResults = $state([]);
	let tagHighlight = $state(-1);

	let peopleQuery = $state('');
	let peopleResults = $state([]);
	let selectedPeopleTypeId = $state(1);
	let peopleHighlight = $state(-1);

	let dirty = $derived(
		doc &&
			(editTitle !== doc.title ||
				editLanguage !== (doc.language ?? '') ||
				editDocumentTypeId !== (doc.document_type_id ?? 1))
	);

	onMount(async () => {
		const onBeforeUnload = (e) => {
			if (dirty) {
				e.preventDefault();
				e.returnValue = '';
			}
		};
		window.addEventListener('beforeunload', onBeforeUnload);
		return () => window.removeEventListener('beforeunload', onBeforeUnload);
	});

	onMount(async () => {
		const [docRes, typesRes, pTypesRes] = await Promise.allSettled([
			api.documents.get(params.id),
			api.autocomplete.documentTypes(),
			api.autocomplete.peopleTypes()
		]);
		const data = docRes.status === 'fulfilled' ? docRes.value : null;
		const types = typesRes.status === 'fulfilled' ? typesRes.value : [];
		const pTypes = pTypesRes.status === 'fulfilled' ? pTypesRes.value : [];
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
			goto(resolve('/documents'));
		} else {
			deleting = false;
			toastStore.error(`Failed to delete document: ${res.status} ${res.statusText}`);
		}
	}

	async function handleReenrich() {
		if (reenriching) return;
		reenriching = true;
		const data = await api.documents.reenrich(params.id);
		reenriching = false;
		if (data) {
			toastStore.success('Re-enrichment queued');
		} else {
			toastStore.error('Failed to queue re-enrichment');
		}
	}

	async function searchTags(q) {
		tagQuery = q;
		tagHighlight = -1;
		if (!q.trim()) {
			tagResults = [];
			return;
		}
		try {
			tagResults = await api.autocomplete.tags(q);
		} catch {
			tagResults = [];
		}
	}

	function onTagKeydown(e) {
		if (e.key === 'ArrowDown') {
			e.preventDefault();
			if (tagResults.length > 0) {
				tagHighlight = (tagHighlight + 1) % tagResults.length;
				scrollTagHighlight();
			}
		} else if (e.key === 'ArrowUp') {
			e.preventDefault();
			if (tagResults.length > 0) {
				tagHighlight =
					tagHighlight < 0
						? tagResults.length - 1
						: (tagHighlight - 1 + tagResults.length) % tagResults.length;
				scrollTagHighlight();
			}
		} else if (e.key === 'Enter') {
			const tag = tagResults[tagHighlight];
			if (tag) {
				e.preventDefault();
				selectTag(tag);
			}
		} else if (e.key === 'Escape') {
			tagResults = [];
			tagHighlight = -1;
		}
	}

	function scrollTagHighlight() {
		if (tagHighlight < 0) return;
		document.getElementById(`tag-option-${tagHighlight}`)?.scrollIntoView({ block: 'nearest' });
	}

	function selectTag(tag) {
		tagQuery = '';
		tagResults = [];
		tagHighlight = -1;
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
		peopleHighlight = -1;
		if (!q.trim()) {
			peopleResults = [];
			return;
		}
		try {
			peopleResults = await api.autocomplete.people(q);
		} catch {
			peopleResults = [];
		}
	}

	function onPeopleKeydown(e) {
		if (e.key === 'ArrowDown') {
			e.preventDefault();
			if (peopleResults.length > 0) {
				peopleHighlight = (peopleHighlight + 1) % peopleResults.length;
				scrollPeopleHighlight();
			}
		} else if (e.key === 'ArrowUp') {
			e.preventDefault();
			if (peopleResults.length > 0) {
				peopleHighlight =
					peopleHighlight < 0
						? peopleResults.length - 1
						: (peopleHighlight - 1 + peopleResults.length) % peopleResults.length;
				scrollPeopleHighlight();
			}
		} else if (e.key === 'Enter') {
			const person = peopleResults[peopleHighlight];
			if (person) {
				e.preventDefault();
				selectPerson(person);
			}
		} else if (e.key === 'Escape') {
			peopleResults = [];
			peopleHighlight = -1;
		}
	}

	function scrollPeopleHighlight() {
		if (peopleHighlight < 0) return;
		document
			.getElementById(`person-option-${peopleHighlight}`)
			?.scrollIntoView({ block: 'nearest' });
	}

	function selectPerson(person) {
		peopleQuery = '';
		peopleResults = [];
		peopleHighlight = -1;
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

<div class="flex h-full min-h-0 flex-col gap-4">
	<a
		href={resolve('/documents')}
		class="inline-flex items-center gap-1 text-sm text-parchment-500 hover:text-parchment-200"
	>
		&larr; Back to documents
	</a>

	{#if !doc}
		<p class="text-parchment-500">Loading…</p>
	{:else}
		<h1 class="text-2xl font-semibold text-pretty wrap-break-word text-parchment-200">
			{doc.title}
		</h1>

		<div class="flex flex-wrap items-center gap-2 text-sm text-parchment-400">
			<span class="rounded-full bg-clay-800 px-2 py-0.5">{doc.original_type}</span>
			<span class="rounded-full bg-clay-800 px-2 py-0.5">{formatSize(doc.file_size)}</span>
			<span class="rounded-full bg-clay-800 px-2 py-0.5">{doc.page_count ?? '—'} pages</span>
			<span class="rounded-full bg-clay-800 px-2 py-0.5">{doc.word_count ?? '—'} words</span>
			<span>
				Modified
				{new Date(doc.modified_at).toLocaleDateString(undefined, {
					year: 'numeric',
					month: 'short',
					day: 'numeric'
				})}
			</span>
		</div>

		<div
			class="grid min-h-0 flex-1 grid-cols-1 gap-4 overflow-hidden {inspectorOpen
				? 'lg:grid-cols-2'
				: 'lg:grid-cols-1'}"
		>
			<div class="h-full min-w-0 overflow-hidden">
				<PdfViewer
					url={resolve(`/api/v1/documents/${doc.id}/file`)}
					title={doc.title}
					rootClass="h-full"
					scrollClass="flex-1 min-h-0 overflow-y-auto"
				>
					{#snippet actions()}
						<a
							href={resolve(`/api/v1/documents/${doc.id}/file?download=true`)}
							download
							aria-label="Download PDF"
							title="Download PDF"
							class="rounded-md p-1.5 text-parchment-300 hover:bg-clay-800 hover:text-parchment-100 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
						>
							<Icon name="download" />
						</a>
						{#if !authStore.authEnabled() || authStore.isEditor()}
							<button
								type="button"
								onclick={handleReenrich}
								disabled={reenriching}
								aria-label="Re-enrich"
								title="Re-enrich"
								class="rounded-md p-1.5 text-parchment-300 hover:bg-clay-800 hover:text-parchment-100 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-40"
							>
								<Icon name="rotate-cw" />
							</button>
							<button
								type="button"
								onclick={handleDelete}
								disabled={deleting}
								aria-label="Delete document"
								title="Delete document"
								class="rounded-md p-1.5 text-parchment-400 hover:bg-clay-800 hover:text-terracotta-400 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-40"
							>
								<Icon name="trash-2" />
							</button>
						{/if}
						<span class="mx-1 h-5 w-px bg-clay-700" aria-hidden="true"></span>
						<button
							type="button"
							onclick={() => (inspectorOpen = !inspectorOpen)}
							class="rounded-md p-1.5 text-parchment-300 hover:bg-clay-800 hover:text-parchment-100 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							aria-expanded={inspectorOpen}
							aria-label={inspectorOpen ? 'Hide document info' : 'Show document info'}
							title={inspectorOpen ? 'Hide document info' : 'Show document info'}
						>
							<Icon name={inspectorOpen ? 'panel-right-close' : 'panel-right-open'} />
						</button>
					{/snippet}
				</PdfViewer>
			</div>

			{#if inspectorOpen}
				<aside
					class="h-full min-w-0 space-y-4 overflow-y-auto rounded-lg border border-clay-800 bg-clay-900 p-4"
				>
					{#if !authStore.authEnabled() || authStore.isEditor()}
						<div class="rounded-lg border border-clay-800 bg-clay-950 p-4">
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
										autocomplete="off"
										class="mt-0.5 w-full rounded-md border border-clay-700 bg-clay-950 px-2 py-1 text-sm text-parchment-200 placeholder-parchment-600 focus:border-gold-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-gold-500"
									/>
								</div>
								<div>
									<label class="text-xs text-parchment-500" for="edit-doctype">Type</label>
									<select
										id="edit-doctype"
										bind:value={editDocumentTypeId}
										class="mt-0.5 w-full rounded-md border border-clay-700 bg-clay-950 px-2 py-1 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-gold-500"
									>
										{#each documentTypes as dt (dt.id)}
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
										autocomplete="off"
										class="mt-0.5 w-full rounded-md border border-clay-700 bg-clay-950 px-2 py-1 text-sm text-parchment-200 placeholder-parchment-600 focus:border-gold-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-gold-500"
									/>
								</div>
								<button
									onclick={saveMetadata}
									disabled={savingMeta}
									class="w-full rounded-md bg-gold-600 px-3 py-1.5 text-xs font-medium text-clay-950 hover:bg-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-50"
								>
									{savingMeta ? 'Saving…' : 'Save'}
								</button>
							</div>
						</div>
					{/if}

					<div class="rounded-lg border border-clay-800 bg-clay-950 p-4">
						<p class="text-xs font-medium tracking-wider text-parchment-500 uppercase">Tags</p>
						{#if (doc.tags ?? []).length > 0}
							<div class="mt-2 flex flex-wrap gap-1.5">
								{#each doc.tags as tag (tag.id)}
									<span
										class="inline-flex items-center gap-1 rounded-full bg-lapis-700 px-2 py-0.5 text-xs text-parchment-200"
									>
										{tag.name}
										{#if !authStore.authEnabled() || authStore.isEditor()}
											<button
												onclick={() => removeTag(tag.id)}
												class="text-parchment-400 hover:text-terracotta-400"
												aria-label={`Remove tag ${tag.name}`}>&times;</button
											>
										{/if}
									</span>
								{/each}
							</div>
						{:else}
							<p class="mt-1 text-parchment-500">No tags</p>
						{/if}
						{#if !authStore.authEnabled() || authStore.isEditor()}
							<div class="relative mt-2">
								<input
									type="text"
									aria-label="Search tags"
									autocomplete="off"
									bind:value={tagQuery}
									oninput={() => searchTags(tagQuery)}
									onkeydown={onTagKeydown}
									placeholder="Search tags…"
									class="w-full rounded-md border border-clay-700 bg-clay-950 px-2 py-1 text-sm text-parchment-200 placeholder-parchment-600 focus:border-gold-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-gold-500"
								/>
								{#if tagResults.length > 0}
									<div
										class="absolute top-full right-0 left-0 z-10 mt-1 max-h-40 overflow-y-auto rounded-md border border-clay-700 bg-clay-950 shadow-lg"
									>
										{#each tagResults as tag, i (i)}
											<button
												id={`tag-option-${i}`}
												onclick={() => selectTag(tag)}
												onmouseenter={() => (tagHighlight = i)}
												class="w-full px-2 py-1 text-left text-sm text-parchment-200 hover:bg-clay-800 focus-visible:bg-clay-800 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none {tagHighlight ===
												i
													? 'bg-clay-800'
													: ''}"
											>
												{tag.name}
											</button>
										{/each}
									</div>
								{/if}
							</div>
						{/if}
					</div>

					<div class="rounded-lg border border-clay-800 bg-clay-950 p-4">
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
										{#if !authStore.authEnabled() || authStore.isEditor()}
											<button
												onclick={() => removePerson(person.id, person.person_type_id)}
												class="shrink-0 text-parchment-500 hover:text-terracotta-400"
												aria-label="Remove person">&times;</button
											>
										{/if}
									</div>
								{/each}
							</div>
						{:else}
							<p class="mt-1 text-parchment-500">No people</p>
						{/if}
						{#if !authStore.authEnabled() || authStore.isEditor()}
							<div class="relative mt-2">
								<input
									type="text"
									aria-label="Search people"
									autocomplete="off"
									bind:value={peopleQuery}
									oninput={() => searchPeople(peopleQuery)}
									onkeydown={onPeopleKeydown}
									placeholder="Search people…"
									class="w-full rounded-md border border-clay-700 bg-clay-950 px-2 py-1 text-sm text-parchment-200 placeholder-parchment-600 focus:border-gold-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-gold-500"
								/>
								{#if peopleResults.length > 0}
									<div
										class="absolute top-full right-0 left-0 z-10 mt-1 max-h-40 overflow-y-auto rounded-md border border-clay-700 bg-clay-950 shadow-lg"
									>
										{#each peopleResults as person, i (i)}
											<button
												id={`person-option-${i}`}
												onclick={() => selectPerson(person)}
												onmouseenter={() => (peopleHighlight = i)}
												class="w-full px-2 py-1 text-left text-sm text-parchment-200 hover:bg-clay-800 focus-visible:bg-clay-800 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none {peopleHighlight ===
												i
													? 'bg-clay-800'
													: ''}"
											>
												{person.name}
											</button>
										{/each}
									</div>
								{/if}
								<div class="mt-2 flex gap-2">
									<select
										aria-label="Person type"
										bind:value={selectedPeopleTypeId}
										class="flex-1 rounded-md border border-clay-700 bg-clay-950 px-2 py-1 text-xs text-parchment-200 focus:border-gold-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-gold-500"
									>
										{#each peopleTypes as pt (pt.id)}
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
						{/if}
					</div>

					<details open class="rounded-lg border border-clay-800 bg-clay-950">
						<summary
							class="cursor-pointer px-4 py-3 text-xs font-medium tracking-wider text-parchment-500 uppercase select-none"
						>
							File details
						</summary>
						<dl class="space-y-1.5 border-t border-clay-800 px-4 py-3 text-sm">
							<div class="flex justify-between gap-4">
								<dt class="text-parchment-500">Original type</dt>
								<dd class="text-parchment-200">{doc.original_type}</dd>
							</div>
							<div class="flex justify-between gap-4">
								<dt class="text-parchment-500">File size</dt>
								<dd class="text-parchment-200 tabular-nums">{formatSize(doc.file_size)}</dd>
							</div>
							<div class="flex justify-between gap-4">
								<dt class="text-parchment-500">Pages</dt>
								<dd class="text-parchment-200 tabular-nums">{doc.page_count ?? '—'}</dd>
							</div>
							<div class="flex justify-between gap-4">
								<dt class="text-parchment-500">Words</dt>
								<dd class="text-parchment-200 tabular-nums">{doc.word_count ?? '—'}</dd>
							</div>
							<div class="flex justify-between gap-4">
								<dt class="text-parchment-500">Characters</dt>
								<dd class="text-parchment-200 tabular-nums">{doc.char_count ?? '—'}</dd>
							</div>
							<div class="flex justify-between gap-4">
								<dt class="text-parchment-500">Created</dt>
								<dd class="text-parchment-200">{new Date(doc.created_at).toLocaleString()}</dd>
							</div>
							<div class="flex justify-between gap-4">
								<dt class="text-parchment-500">Modified</dt>
								<dd class="text-parchment-200">{new Date(doc.modified_at).toLocaleString()}</dd>
							</div>
						</dl>
					</details>

					<details class="rounded-lg border border-clay-800 bg-clay-950">
						<summary
							class="cursor-pointer px-4 py-3 text-xs font-medium tracking-wider text-parchment-500 uppercase select-none"
						>
							Checksums
						</summary>
						<div class="space-y-3 border-t border-clay-800 px-4 py-3">
							<div>
								<p class="text-xs text-parchment-500">MD5</p>
								<p class="mt-0.5 font-mono text-xs break-all text-parchment-400" translate="no">
									{doc.md5_checksum}
								</p>
							</div>
							<div>
								<p class="text-xs text-parchment-500">SHA‑512</p>
								<p class="mt-0.5 font-mono text-xs break-all text-parchment-400" translate="no">
									{doc.sha512_checksum}
								</p>
							</div>
						</div>
					</details>
				</aside>
			{/if}
		</div>
	{/if}
</div>
