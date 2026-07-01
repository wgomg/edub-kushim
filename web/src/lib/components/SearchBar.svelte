<script>
	import { api } from '$lib/api';
	import {
		getPersonTypes,
		formatSize,
		parseDateRange,
		parseSize
	} from '$lib/stores/searchFilter.js';

	let {
		query = '',
		tags = [],
		people = [],
		documentType = '',
		language = '',
		mimeType = '',
		dateCreated = { from: null, to: null },
		dateModified = { from: null, to: null },
		fileSize = { min: null, max: null },
		onSearch = () => {}
	} = $props();

	let inputEl = $state(null);
	let inputValue = $state('');
	let showDropdown = $state(false);
	let suggestions = $state([]);
	let selectedIndex = $state(-1);
	let debounceTimer = $state(null);
	let pendingField = $state(null);

	function emit(partial) {
		onSearch({
			query,
			tags,
			people,
			documentType,
			language,
			mimeType,
			dateCreated,
			dateModified,
			fileSize,
			...partial
		});
	}

	function getFieldPrefix(text) {
		const m = text.match(/^(\w+):(.*)$/);
		if (m) return { field: m[1], value: m[2] };
		return null;
	}

	function isPersonField(field) {
		return getPersonTypes().has(field);
	}

	async function fetchSuggestions(prefix, field) {
		if (field === 'tag') {
			const data = await api.autocomplete.tags(prefix, 10);
			return data.map((t) => ({ label: t.name, value: t.name, type: 'tag' }));
		}

		if (field === 'type') {
			const data = await api.autocomplete.documentTypes();
			return data
				.filter((dt) => dt.name.includes(prefix || ''))
				.slice(0, 10)
				.map((dt) => ({ label: dt.name, value: dt.name, type: 'documentType' }));
		}

		if (field && isPersonField(field)) {
			const data = await api.autocomplete.people(prefix, 10);
			return data.map((p) => ({ label: `${field}: ${p.name}`, value: p.name, type: field }));
		}

		if (field && ['lang', 'mime', 'created', 'modified', 'size'].includes(field)) {
			return [];
		}

		if (!prefix || prefix.length < 2) return [];

		const [tagsData, peopleData, typesData] = await Promise.all([
			api.autocomplete.tags(prefix, 5),
			api.autocomplete.people(prefix, 5),
			api.autocomplete.documentTypes()
		]);

		const results = [];
		for (const t of tagsData) results.push({ label: `tag: ${t.name}`, value: t.name, type: 'tag' });
		for (const p of peopleData)
			results.push({ label: `author: ${p.name}`, value: p.name, type: 'author' });
		for (const dt of typesData) {
			if (dt.name.includes(prefix))
				results.push({ label: `type: ${dt.name}`, value: dt.name, type: 'documentType' });
		}
		return results.slice(0, 10);
	}

	function onInput() {
		const text = inputValue;
		pendingField = null;

		const fieldPrefix = getFieldPrefix(text);
		if (fieldPrefix) {
			pendingField = fieldPrefix.field;
			debouncedFetch(fieldPrefix.value, fieldPrefix.field);
		} else if (text.length >= 2) {
			debouncedFetch(text, null);
		} else {
			showDropdown = false;
			suggestions = [];
		}
	}

	function debouncedFetch(prefix, field) {
		if (debounceTimer) clearTimeout(debounceTimer);
		debounceTimer = setTimeout(async () => {
			suggestions = await fetchSuggestions(prefix, field);
			showDropdown = suggestions.length > 0;
			selectedIndex = -1;
		}, 250);
	}

	function selectSuggestion(s) {
		inputValue = '';
		pendingField = null;
		if (s.type === 'tag') {
			emit({ tags: [...tags, s.value] });
		} else if (s.type === 'documentType') {
			emit({ documentType: s.value });
		} else {
			emit({ people: [...people, { name: s.value, type: s.type }] });
		}
		showDropdown = false;
		suggestions = [];
		if (inputEl) inputEl.focus();
	}

	function onSubmit() {
		if (!inputValue.trim()) return;
		const fp = getFieldPrefix(inputValue.trim());
		if (fp) {
			if (fp.field === 'tag') {
				emit({ tags: [...tags, fp.value] });
			} else if (fp.field === 'type') {
				emit({ documentType: fp.value });
			} else if (fp.field === 'lang') {
				emit({ language: fp.value });
			} else if (fp.field === 'mime') {
				emit({ mimeType: fp.value });
			} else if (fp.field === 'created') {
				const dr = parseDateRange(fp.value);
				if (dr) emit({ dateCreated: dr });
			} else if (fp.field === 'modified') {
				const dr = parseDateRange(fp.value);
				if (dr) emit({ dateModified: dr });
			} else if (fp.field === 'size') {
				const sz = parseSize(fp.value);
				if (sz) {
					const fs = { min: fileSize.min, max: fileSize.max };
					if (sz.op === '' || sz.op === '>=' || sz.op === '>') fs.min = sz.bytes;
					if (sz.op === '' || sz.op === '<=' || sz.op === '<') fs.max = sz.bytes;
					if (sz.op === '>') fs.max = null;
					if (sz.op === '<') fs.min = null;
					emit({ fileSize: fs });
				}
			} else if (isPersonField(fp.field)) {
				emit({ people: [...people, { name: fp.value, type: fp.field }] });
			}
		} else {
			emit({ query: (query + ' ' + inputValue).trim() });
		}
		inputValue = '';
		showDropdown = false;
		suggestions = [];
	}

	function onKeyDown(e) {
		if (e.key === 'Enter') {
			e.preventDefault();
			if (showDropdown && selectedIndex >= 0) {
				selectSuggestion(suggestions[selectedIndex]);
			} else {
				onSubmit();
			}
		} else if (e.key === 'ArrowDown') {
			e.preventDefault();
			if (showDropdown) {
				selectedIndex = Math.min(selectedIndex + 1, suggestions.length - 1);
			}
		} else if (e.key === 'ArrowUp') {
			e.preventDefault();
			if (showDropdown) {
				selectedIndex = Math.max(selectedIndex - 1, -1);
			}
		} else if (e.key === 'Escape') {
			showDropdown = false;
			suggestions = [];
		} else if (e.key === 'Backspace' && !inputValue) {
			if (tags.length > 0) {
				emit({ tags: tags.slice(0, -1) });
			} else if (people.length > 0) {
				emit({ people: people.slice(0, -1) });
			} else if (documentType) {
				emit({ documentType: '' });
			} else if (mimeType) {
				emit({ mimeType: '' });
			} else if (language) {
				emit({ language: '' });
			} else if (dateCreated.from || dateCreated.to) {
				emit({ dateCreated: { from: null, to: null } });
			} else if (dateModified.from || dateModified.to) {
				emit({ dateModified: { from: null, to: null } });
			} else if (fileSize.min != null || fileSize.max != null) {
				emit({ fileSize: { min: null, max: null } });
			} else if (query) {
				emit({ query: '' });
			}
		}
	}

	function removeTag(index) {
		emit({ tags: tags.filter((_, i) => i !== index) });
	}

	function removePerson(index) {
		emit({ people: people.filter((_, i) => i !== index) });
	}

	function clearDocumentType() {
		emit({ documentType: '' });
	}

	function clearLanguage() {
		emit({ language: '' });
	}

	function clearMimeType() {
		emit({ mimeType: '' });
	}

	function clearDateCreated() {
		emit({ dateCreated: { from: null, to: null } });
	}

	function clearDateModified() {
		emit({ dateModified: { from: null, to: null } });
	}

	function clearFileSize() {
		emit({ fileSize: { min: null, max: null } });
	}

	function clearQuery() {
		emit({ query: '' });
	}

	function dateChipLabel(dr) {
		if (dr.from && dr.to) return `${dr.from}..${dr.to}`;
		if (dr.from) return `>${dr.from}`;
		return `<${dr.to}`;
	}

	function sizeChipLabel(fs) {
		if (fs.min != null && fs.max != null) return `${formatSize(fs.min)}..${formatSize(fs.max)}`;
		if (fs.min != null) return `>${formatSize(fs.min)}`;
		return `<${formatSize(fs.max)}`;
	}
</script>

<div class="relative">
	<div
		class="flex min-h-[42px] flex-wrap items-center gap-1.5 rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus-within:border-gold-600"
	>
		{#each tags as tag, i}
			<span
				class="inline-flex items-center gap-1 rounded-full bg-lapis-700 px-2.5 py-0.5 text-xs text-parchment-200"
			>
				tag:{tag}
				<button onclick={() => removeTag(i)} class="text-parchment-400 hover:text-parchment-200"
					>&times;</button
				>
			</span>
		{/each}

		{#each people as p, i}
			<span
				class="inline-flex items-center gap-1 rounded-full bg-lapis-700 px-2.5 py-0.5 text-xs text-parchment-200"
			>
				{p.type}:{p.name}
				<button onclick={() => removePerson(i)} class="text-parchment-400 hover:text-parchment-200"
					>&times;</button
				>
			</span>
		{/each}

		{#if documentType}
			<span
				class="inline-flex items-center gap-1 rounded-full bg-lapis-700 px-2.5 py-0.5 text-xs text-parchment-200"
			>
				type:{documentType}
				<button onclick={clearDocumentType} class="text-parchment-400 hover:text-parchment-200"
					>&times;</button
				>
			</span>
		{/if}

		{#if language}
			<span
				class="inline-flex items-center gap-1 rounded-full bg-emerald-700 px-2.5 py-0.5 text-xs text-parchment-200"
			>
				lang:{language}
				<button onclick={clearLanguage} class="text-parchment-400 hover:text-parchment-200"
					>&times;</button
				>
			</span>
		{/if}

		{#if mimeType}
			<span
				class="inline-flex items-center gap-1 rounded-full bg-emerald-700 px-2.5 py-0.5 text-xs text-parchment-200"
			>
				mime:{mimeType}
				<button onclick={clearMimeType} class="text-parchment-400 hover:text-parchment-200"
					>&times;</button
				>
			</span>
		{/if}

		{#if dateCreated.from || dateCreated.to}
			<span
				class="inline-flex items-center gap-1 rounded-full bg-amber-700 px-2.5 py-0.5 text-xs text-parchment-200"
			>
				created:{dateChipLabel(dateCreated)}
				<button onclick={clearDateCreated} class="text-parchment-400 hover:text-parchment-200"
					>&times;</button
				>
			</span>
		{/if}

		{#if dateModified.from || dateModified.to}
			<span
				class="inline-flex items-center gap-1 rounded-full bg-amber-700 px-2.5 py-0.5 text-xs text-parchment-200"
			>
				modified:{dateChipLabel(dateModified)}
				<button onclick={clearDateModified} class="text-parchment-400 hover:text-parchment-200"
					>&times;</button
				>
			</span>
		{/if}

		{#if fileSize.min != null || fileSize.max != null}
			<span
				class="inline-flex items-center gap-1 rounded-full bg-purple-700 px-2.5 py-0.5 text-xs text-parchment-200"
			>
				size:{sizeChipLabel(fileSize)}
				<button onclick={clearFileSize} class="text-parchment-400 hover:text-parchment-200"
					>&times;</button
				>
			</span>
		{/if}

		{#if query}
			<span
				class="inline-flex items-center gap-1 rounded-full bg-clay-700 px-2.5 py-0.5 text-xs text-parchment-200"
			>
				{query}
				<button onclick={clearQuery} class="text-parchment-400 hover:text-parchment-200"
					>&times;</button
				>
			</span>
		{/if}

		<input
			bind:this={inputEl}
			bind:value={inputValue}
			oninput={onInput}
			onkeydown={onKeyDown}
			onblur={() =>
				setTimeout(() => {
					showDropdown = false;
				}, 200)}
			type="text"
			placeholder={tags.length === 0 &&
			people.length === 0 &&
			!documentType &&
			!language &&
			!mimeType &&
			!dateCreated.from &&
			!dateCreated.to &&
			!dateModified.from &&
			!dateModified.to &&
			fileSize.min == null &&
			fileSize.max == null &&
			!query
				? 'Search documents...'
				: ''}
			class="min-w-[120px] flex-1 border-0 bg-transparent p-0 text-parchment-200 placeholder-parchment-500 outline-none focus:ring-0"
		/>
	</div>

	{#if showDropdown}
		<ul
			class="absolute top-full right-0 left-0 z-50 mt-1 max-h-60 overflow-y-auto rounded-lg border border-clay-800 bg-clay-900 shadow-lg"
			role="listbox"
		>
			{#each suggestions as s, i}
				<li
					role="option"
					aria-selected={i === selectedIndex}
					class="cursor-pointer px-3 py-2 text-sm text-parchment-200 hover:bg-clay-800 {i ===
					selectedIndex
						? 'bg-clay-800'
						: ''}"
					onmousedown={() => selectSuggestion(s)}
				>
					{s.label}
				</li>
			{/each}
		</ul>
	{/if}
</div>
