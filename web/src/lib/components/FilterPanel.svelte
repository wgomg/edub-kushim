<script>
	import { api } from '$lib/api';
	import { filterStore } from '$lib/stores/filterStore.js';
	import { getPersonTypes, formatSize } from '$lib/stores/searchFilter.js';

	let languages = $state([]);

	let f = $state({
		tags: [],
		people: [],
		documentType: '',
		language: '',
		dateCreated: { from: null, to: null },
		dateModified: { from: null, to: null },
		fileSize: { min: null, max: null }
	});

	let missingLanguage = $state(false);
	let missingType = $state(false);
	let untagged = $state(false);

	filterStore.subscribe((s) => {
		f = {
			tags: s.tags,
			people: s.people,
			documentType: s.documentType,
			language: s.language,
			dateCreated: s.dateCreated,
			dateModified: s.dateModified,
			fileSize: s.fileSize
		};
		missingLanguage = s.missingLanguage;
		missingType = s.missingType;
		untagged = s.untagged;
	});

	function emit(partial) {
		filterStore.setPartial(partial);
	}

	let tagInput = $state('');
	let tagSuggestions = $state([]);
	let tagDebounce = $state(null);
	let tagSelIndex = $state(-1);
	let personType = $state('');
	let personInput = $state('');
	let personSuggestions = $state([]);
	let personDebounce = $state(null);
	let personSelIndex = $state(-1);
	let documentTypes = $state([]);
	let fileMinRaw = $state('');
	let fileMaxRaw = $state('');

	$effect(() => {
		api.autocomplete.documentTypes().then((d) => (documentTypes = d));
	});

	$effect(() => {
		api.filterLanguages().then((d) => (languages = d));
	});

	function onTagInput() {
		if (tagDebounce) clearTimeout(tagDebounce);
		tagSelIndex = -1;
		const q = tagInput;
		if (!q || q.length < 1) {
			tagSuggestions = [];
			return;
		}
		tagDebounce = setTimeout(async () => {
			tagSuggestions = await api.autocomplete.tags(q, 10);
		}, 200);
	}

	function onTagKeydown(e) {
		if (e.key === 'Enter') {
			e.preventDefault();
			if (tagSuggestions.length > 0 && tagSelIndex >= 0) addTag(tagSuggestions[tagSelIndex].name);
			else if (tagSuggestions.length > 0) addTag(tagSuggestions[0].name);
			else if (tagInput.trim()) addTag(tagInput.trim());
		} else if (e.key === 'ArrowDown' && tagSuggestions.length > 0) {
			e.preventDefault();
			tagSelIndex = (tagSelIndex + 1) % tagSuggestions.length;
		} else if (e.key === 'ArrowUp' && tagSuggestions.length > 0) {
			e.preventDefault();
			tagSelIndex = (tagSelIndex - 1 + tagSuggestions.length) % tagSuggestions.length;
		}
	}

	function addTag(name) {
		if (name && !f.tags.includes(name)) {
			emit({ tags: [...f.tags, name] });
		}
		tagInput = '';
		tagSuggestions = [];
		tagSelIndex = -1;
	}

	function removeTag(name) {
		emit({ tags: f.tags.filter((t) => t !== name) });
	}

	function onPersonInput() {
		if (personDebounce) clearTimeout(personDebounce);
		personSelIndex = -1;
		const q = personInput;
		if (!q || q.length < 1 || !personType) {
			personSuggestions = [];
			return;
		}
		personDebounce = setTimeout(async () => {
			personSuggestions = await api.autocomplete.people(q, 10);
		}, 200);
	}

	function onPersonKeydown(e) {
		if (e.key === 'Enter') {
			e.preventDefault();
			if (personSuggestions.length > 0 && personSelIndex >= 0)
				addPerson(personSuggestions[personSelIndex].name);
			else if (personSuggestions.length > 0) addPerson(personSuggestions[0].name);
			else if (personInput.trim() && personType) addPerson(personInput.trim());
		} else if (e.key === 'ArrowDown' && personSuggestions.length > 0) {
			e.preventDefault();
			personSelIndex = (personSelIndex + 1) % personSuggestions.length;
		} else if (e.key === 'ArrowUp' && personSuggestions.length > 0) {
			e.preventDefault();
			personSelIndex = (personSelIndex - 1 + personSuggestions.length) % personSuggestions.length;
		}
	}

	function addPerson(name) {
		if (name && personType && !f.people.some((p) => p.name === name && p.type === personType)) {
			emit({ people: [...f.people, { name, type: personType }] });
		}
		personInput = '';
		personSuggestions = [];
		personSelIndex = -1;
	}

	function removePerson(name, type) {
		emit({ people: f.people.filter((p) => !(p.name === name && p.type === type)) });
	}

	function clearAll() {
		emit({
			tags: [],
			people: [],
			documentType: '',
			language: '',
			dateCreated: { from: null, to: null },
			dateModified: { from: null, to: null },
			fileSize: { min: null, max: null },
			missingLanguage: false,
			missingType: false,
			untagged: false
		});
		personType = '';
		fileMinRaw = '';
		fileMaxRaw = '';
	}

	function onFileMinInput() {
		const raw = fileMinRaw;
		if (!raw) {
			emit({ fileSize: { min: null, max: f.fileSize.max } });
			return;
		}
		const parsed = parseFloat(raw);
		if (isNaN(parsed)) return;
		const unit = raw.match(/[a-z]+$/i)?.[0]?.toLowerCase() || 'b';
		const mult = { b: 1, kb: 1024, mb: 1048576, gb: 1073741824 }[unit] || 1;
		emit({ fileSize: { min: Math.round(parsed * mult), max: f.fileSize.max } });
	}

	function onFileMaxInput() {
		const raw = fileMaxRaw;
		if (!raw) {
			emit({ fileSize: { min: f.fileSize.min, max: null } });
			return;
		}
		const parsed = parseFloat(raw);
		if (isNaN(parsed)) return;
		const unit = raw.match(/[a-z]+$/i)?.[0]?.toLowerCase() || 'b';
		const mult = { b: 1, kb: 1024, mb: 1048576, gb: 1073741824 }[unit] || 1;
		emit({ fileSize: { min: f.fileSize.min, max: Math.round(parsed * mult) } });
	}

	function commitFileMin() {
		if (!fileMinRaw) {
			emit({ fileSize: { min: null, max: f.fileSize.max } });
		} else {
			onFileMinInput();
		}
	}

	function commitFileMax() {
		if (!fileMaxRaw) {
			emit({ fileSize: { min: f.fileSize.min, max: null } });
		} else {
			onFileMaxInput();
		}
	}

	$effect(() => {
		if (f.fileSize.min != null) fileMinRaw = formatSize(f.fileSize.min);
		else fileMinRaw = '';
	});
	$effect(() => {
		if (f.fileSize.max != null) fileMaxRaw = formatSize(f.fileSize.max);
		else fileMaxRaw = '';
	});
</script>

<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
	<div class="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3">
		<!-- Tags -->
		<div>
			<label for="fp-tags" class="mb-1 block text-xs font-medium text-parchment-400">Tags</label>
			<div class="relative">
				<input
					id="fp-tags"
					name="fp-tags"
					type="text"
					bind:value={tagInput}
					oninput={onTagInput}
					onkeydown={onTagKeydown}
					role="combobox"
					aria-expanded={tagSuggestions.length > 0}
					aria-controls="fp-tags-listbox"
					aria-autocomplete="list"
					aria-activedescendant={tagSelIndex >= 0 ? `fp-tag-option-${tagSelIndex}` : undefined}
					placeholder="Type to search…"
					autocomplete="off"
					spellcheck="false"
					class="w-full rounded-md border border-clay-700 bg-clay-950 px-3 py-1.5 text-xs text-parchment-200 placeholder-parchment-600 focus:border-gold-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-gold-500"
				/>
				{#if tagSuggestions.length > 0}
					<ul
						id="fp-tags-listbox"
						role="listbox"
						aria-labelledby="fp-tags"
						class="absolute top-full right-0 left-0 z-20 mt-1 max-h-32 overflow-y-auto rounded-md border border-clay-700 bg-clay-950 shadow-lg"
					>
						{#each tagSuggestions as t, i (i)}
							<li
								id={`fp-tag-option-${i}`}
								role="option"
								aria-selected={i === tagSelIndex}
								class="cursor-pointer px-3 py-1.5 text-xs text-parchment-200 hover:bg-clay-800 {i ===
								tagSelIndex
									? 'bg-clay-800'
									: ''}"
								onmousedown={() => addTag(t.name)}
							>
								{t.name}
							</li>
						{/each}
					</ul>
				{/if}
			</div>
			{#if f.tags.length > 0}
				<div class="mt-1.5 flex flex-wrap gap-1">
					{#each f.tags as t (t)}
						<span
							class="inline-flex items-center gap-1 rounded-full bg-lapis-700 px-2 py-0.5 text-xs text-parchment-200"
						>
							{t}
							<button
								onclick={() => removeTag(t)}
								class="text-parchment-400 hover:text-parchment-200"
								aria-label="Remove tag {t}">&times;</button
							>
						</span>
					{/each}
				</div>
			{/if}
		</div>

		<!-- People -->
		<div>
			<label for="fp-person-type" class="mb-1 block text-xs font-medium text-parchment-400"
				>People</label
			>
			<div class="flex gap-1">
				<select
					id="fp-person-type"
					name="fp-person-type"
					bind:value={personType}
					class="w-1/2 rounded-md border border-clay-700 bg-clay-950 px-2 py-1.5 text-xs text-parchment-200 focus:border-gold-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-gold-500"
				>
					<option value="">Type…</option>
					{#each [...getPersonTypes()] as pt (pt)}
						<option value={pt}>{pt}</option>
					{/each}
				</select>
				<div class="relative w-1/2">
					<input
						id="fp-person"
						name="fp-person"
						type="text"
						aria-label="Filter by person"
						bind:value={personInput}
						oninput={onPersonInput}
						onkeydown={onPersonKeydown}
						role="combobox"
						aria-expanded={personSuggestions.length > 0}
						aria-controls="fp-person-listbox"
						aria-autocomplete="list"
						aria-activedescendant={personSelIndex >= 0
							? `fp-person-option-${personSelIndex}`
							: undefined}
						disabled={!personType}
						placeholder={personType ? 'Name…' : 'Select type'}
						autocomplete="off"
						spellcheck="false"
						class="w-full rounded-md border border-clay-700 bg-clay-950 px-3 py-1.5 text-xs text-parchment-200 placeholder-parchment-600 focus:border-gold-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-gold-500 disabled:opacity-50"
					/>
					{#if personSuggestions.length > 0}
						<ul
							id="fp-person-listbox"
							role="listbox"
							aria-labelledby="fp-person"
							class="absolute top-full right-0 left-0 z-20 mt-1 max-h-32 overflow-y-auto rounded-md border border-clay-700 bg-clay-950 shadow-lg"
						>
							{#each personSuggestions as p, i (i)}
								<li
									id={`fp-person-option-${i}`}
									role="option"
									aria-selected={i === personSelIndex}
									class="cursor-pointer px-3 py-1.5 text-xs text-parchment-200 hover:bg-clay-800 {i ===
									personSelIndex
										? 'bg-clay-800'
										: ''}"
									onmousedown={() => addPerson(p.name)}
								>
									{p.name}
								</li>
							{/each}
						</ul>
					{/if}
				</div>
			</div>
			{#if f.people.length > 0}
				<div class="mt-1.5 flex flex-wrap gap-1">
					{#each f.people as p (p.name + p.type)}
						<span
							class="inline-flex items-center gap-1 rounded-full bg-lapis-700 px-2 py-0.5 text-xs text-parchment-200"
						>
							{p.type}:{p.name}
							<button
								onclick={() => removePerson(p.name, p.type)}
								class="text-parchment-400 hover:text-parchment-200"
								aria-label="Remove {p.type}:{p.name}">&times;</button
							>
						</span>
					{/each}
				</div>
			{/if}
		</div>

		<!-- Document Type -->
		<div>
			<label for="fp-doctype" class="mb-1 block text-xs font-medium text-parchment-400"
				>Document Type</label
			>
			<select
				id="fp-doctype"
				name="fp-doctype"
				bind:value={f.documentType}
				onchange={(e) => emit({ documentType: e.target.value })}
				class="w-full rounded-md border border-clay-700 bg-clay-950 px-3 py-1.5 text-xs text-parchment-200 focus:border-gold-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-gold-500"
			>
				<option value="">Any</option>
				{#each documentTypes as dt (dt.id)}
					<option value={dt.name}>{dt.name}</option>
				{/each}
			</select>
		</div>

		<!-- Language -->
		<div>
			<label for="fp-lang" class="mb-1 block text-xs font-medium text-parchment-400">Language</label
			>
			<select
				id="fp-lang"
				name="fp-lang"
				bind:value={f.language}
				onchange={(e) => emit({ language: e.target.value })}
				class="w-full rounded-md border border-clay-700 bg-clay-950 px-3 py-1.5 text-xs text-parchment-200 focus:border-gold-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-gold-500"
			>
				<option value="">Any</option>
				{#each languages as code (code)}
					<option value={code}>{code}</option>
				{/each}
			</select>
		</div>

		<!-- Date Created -->
		<div>
			<label for="fp-date-created-from" class="mb-1 block text-xs font-medium text-parchment-400"
				>Date Created</label
			>
			<div class="flex gap-1">
				<input
					id="fp-date-created-from"
					name="fp-date-created-from"
					type="date"
					value={f.dateCreated.from || ''}
					onchange={(e) =>
						emit({
							dateCreated: { from: e.target.value || null, to: f.dateCreated.to }
						})}
					class="w-1/2 rounded-md border border-clay-700 bg-clay-950 px-2 py-1.5 text-xs text-parchment-200 focus:border-gold-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-gold-500"
					aria-label="From"
				/>
				<input
					id="fp-date-created-to"
					name="fp-date-created-to"
					type="date"
					value={f.dateCreated.to || ''}
					onchange={(e) =>
						emit({
							dateCreated: { from: f.dateCreated.from, to: e.target.value || null }
						})}
					class="w-1/2 rounded-md border border-clay-700 bg-clay-950 px-2 py-1.5 text-xs text-parchment-200 focus:border-gold-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-gold-500"
					aria-label="To"
				/>
			</div>
		</div>

		<!-- Date Modified -->
		<div>
			<label for="fp-date-modified-from" class="mb-1 block text-xs font-medium text-parchment-400"
				>Date Modified</label
			>
			<div class="flex gap-1">
				<input
					id="fp-date-modified-from"
					name="fp-date-modified-from"
					type="date"
					value={f.dateModified.from || ''}
					onchange={(e) =>
						emit({
							dateModified: { from: e.target.value || null, to: f.dateModified.to }
						})}
					class="w-1/2 rounded-md border border-clay-700 bg-clay-950 px-2 py-1.5 text-xs text-parchment-200 focus:border-gold-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-gold-500"
					aria-label="From"
				/>
				<input
					id="fp-date-modified-to"
					name="fp-date-modified-to"
					type="date"
					value={f.dateModified.to || ''}
					onchange={(e) =>
						emit({
							dateModified: { from: f.dateModified.from, to: e.target.value || null }
						})}
					class="w-1/2 rounded-md border border-clay-700 bg-clay-950 px-2 py-1.5 text-xs text-parchment-200 focus:border-gold-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-gold-500"
					aria-label="To"
				/>
			</div>
		</div>

		<!-- File Size -->
		<div>
			<label for="fp-file-min" class="mb-1 block text-xs font-medium text-parchment-400"
				>File Size</label
			>
			<div class="flex gap-1">
				<input
					id="fp-file-min"
					name="fp-file-min"
					type="text"
					bind:value={fileMinRaw}
					onkeydown={(e) => {
						if (e.key === 'Enter') commitFileMin();
					}}
					onblur={commitFileMin}
					placeholder="Min (e.g. 1MB)…"
					autocomplete="off"
					class="w-1/2 rounded-md border border-clay-700 bg-clay-950 px-2 py-1.5 text-xs text-parchment-200 placeholder-parchment-600 focus:border-gold-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-gold-500"
				/>
				<input
					id="fp-file-max"
					name="fp-file-max"
					type="text"
					bind:value={fileMaxRaw}
					onkeydown={(e) => {
						if (e.key === 'Enter') commitFileMax();
					}}
					onblur={commitFileMax}
					placeholder="Max (e.g. 10MB)…"
					autocomplete="off"
					class="w-1/2 rounded-md border border-clay-700 bg-clay-950 px-2 py-1.5 text-xs text-parchment-200 placeholder-parchment-600 focus:border-gold-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-gold-500"
				/>
			</div>
		</div>
	</div>

	<!-- Missing flags -->
	<div class="mt-3 flex flex-wrap gap-3 border-t border-clay-800 pt-3">
		<label
			class="flex cursor-pointer items-center gap-1.5 text-xs text-parchment-400 hover:text-parchment-200"
		>
			<input
				type="checkbox"
				class="accent-gold-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-gold-500"
				checked={missingLanguage}
				onchange={() => emit({ missingLanguage: !missingLanguage })}
			/>
			Missing Language
		</label>
		<label
			class="flex cursor-pointer items-center gap-1.5 text-xs text-parchment-400 hover:text-parchment-200"
		>
			<input
				type="checkbox"
				class="accent-gold-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-gold-500"
				checked={missingType}
				onchange={() => emit({ missingType: !missingType })}
			/>
			Missing Type
		</label>
		<label
			class="flex cursor-pointer items-center gap-1.5 text-xs text-parchment-400 hover:text-parchment-200"
		>
			<input
				type="checkbox"
				class="accent-gold-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-gold-500"
				checked={untagged}
				onchange={() => emit({ untagged: !untagged })}
			/>
			Untagged
		</label>
	</div>

	<!-- Clear All -->
	<div class="mt-3 flex justify-end border-t border-clay-800 pt-3">
		<button
			onclick={clearAll}
			class="rounded-md px-3 py-1.5 text-xs font-medium text-parchment-400 hover:bg-clay-800 hover:text-parchment-200"
		>
			Clear all filters
		</button>
	</div>
</div>
