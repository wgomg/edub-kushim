<script>
	import { api } from '#lib/api.js';
	import Suggest from '#lib/components/Suggest.svelte';
	import { formatSize } from '#lib/utils/html.js';
	import { parseDateRange, parseSize } from '#lib/utils/searchSyntax.js';
	import { toastStore } from '#lib/stores/toastStore.svelte.js';

	/**
	 * Controlled search bar: the filter values come in as props — `q` is the free-text filter — and
	 * every change leaves through `onSearch` as a partial filter patch. It owns only the text draft
	 * and the suggestion popup, which pages tags and people through the shared `Suggest` component.
	 */
	let {
		q = '',
		tags = [],
		people = [],
		documentType = '',
		language = '',
		dateCreated = { from: null, to: null },
		dateModified = { from: null, to: null },
		fileSize = { min: null, max: null },
		personTypes = [],
		personAnyType = 'person',
		onSearch = () => {}
	} = $props();

	const SUGGEST_PAGE = 10;
	const MIXED_PAGE = 5;

	let inputEl = $state(null);
	let inputValue = $state('');
	let mixed = null;

	function emit(partial) {
		onSearch({
			q,
			tags,
			people,
			documentType,
			language,
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

	function tagSuggestionLabel(name, matchedAlias) {
		if (!matchedAlias) return name;
		return `${name} (alias: ${matchedAlias})`;
	}

	function isPersonField(field) {
		return personTypes.includes(field);
	}

	async function fetchTagPage(q, offset, signal) {
		const data = await api.tags.list(q, SUGGEST_PAGE, offset, signal);
		if (!data) throw new Error('Failed to load tags');
		return {
			items: (data.results ?? []).map((t) => ({
				name: tagSuggestionLabel(t.name, t.matched_alias),
				value: t.name,
				type: 'tag'
			})),
			total: data.total ?? 0
		};
	}

	async function fetchPeoplePage(q, offset, signal, type) {
		const data = await api.people.list(q, SUGGEST_PAGE, offset, signal);
		if (!data) throw new Error('Failed to load people');
		return {
			items: (data.results ?? []).map((p) => ({
				name: `${type}: ${p.name}`,
				value: p.name,
				type
			})),
			total: data.total ?? 0
		};
	}

	async function fetchTypes(prefix) {
		const types = await api.autocomplete.documentTypes();
		const items = (types ?? [])
			.filter((dt) => dt.name.includes(prefix))
			.map((dt) => ({ name: `type: ${dt.name}`, value: dt.name, type: 'documentType' }));
		return { items, total: items.length };
	}

	async function fetchMixed(q, offset, signal) {
		const prefix = q.trim();
		if (prefix.length < 2) return { items: [], total: 0 };

		if (offset === 0 || !mixed || mixed.prefix !== prefix) {
			mixed = {
				prefix,
				tagsOffset: 0,
				peopleOffset: 0,
				tagsTotal: 0,
				peopleTotal: 0,
				tagsDone: false,
				peopleDone: false,
				types: []
			};
		}

		const cursors = mixed;
		const items = [];

		if (!cursors.tagsDone) {
			const data = await api.tags.list(prefix, MIXED_PAGE, cursors.tagsOffset, signal);
			if (!data) throw new Error('Failed to load tags');
			const rows = data.results ?? [];
			cursors.tagsOffset += rows.length;
			cursors.tagsTotal = data.total ?? 0;
			cursors.tagsDone = rows.length === 0 || cursors.tagsOffset >= cursors.tagsTotal;
			items.push(
				...rows.map((t) => ({
					name: `tag: ${tagSuggestionLabel(t.name, t.matched_alias)}`,
					value: t.name,
					type: 'tag'
				}))
			);
		}

		if (!cursors.peopleDone) {
			const data = await api.people.list(prefix, MIXED_PAGE, cursors.peopleOffset, signal);
			if (!data) throw new Error('Failed to load people');
			const rows = data.results ?? [];
			cursors.peopleOffset += rows.length;
			cursors.peopleTotal = data.total ?? 0;
			cursors.peopleDone = rows.length === 0 || cursors.peopleOffset >= cursors.peopleTotal;
			items.push(
				...rows.map((p) => ({
					name: `person: ${p.name}`,
					value: p.name,
					type: personAnyType
				}))
			);
		}

		if (offset === 0) {
			const types = await api.autocomplete.documentTypes();
			cursors.types = (types ?? [])
				.filter((dt) => dt.name.includes(prefix))
				.map((dt) => ({ name: `type: ${dt.name}`, value: dt.name, type: 'documentType' }));
			items.push(...cursors.types);
		}

		return {
			items,
			total: cursors.tagsTotal + cursors.peopleTotal + cursors.types.length
		};
	}

	async function fetchSuggestions(q, offset, signal) {
		const fp = getFieldPrefix(q.trim());

		if (!fp) return fetchMixed(q, offset, signal);
		if (fp.field === 'tag') return fetchTagPage(fp.value, offset, signal);
		if (fp.field === 'type') return fetchTypes(fp.value);
		if (fp.field === personAnyType || isPersonField(fp.field)) {
			return fetchPeoplePage(fp.value, offset, signal, fp.field);
		}
		return { items: [], total: 0 };
	}

	function onPick(s) {
		inputValue = '';
		if (s.type === 'tag') {
			emit({ tags: [...tags, s.value] });
		} else if (s.type === 'documentType') {
			emit({ documentType: s.value });
		} else {
			emit({ people: [...people, { name: s.value, type: s.type }] });
		}
		if (inputEl) inputEl.focus();
	}

	function emitFreeText() {
		emit({ q: (q + ' ' + inputValue).trim() });
	}

	// an unrecognized field or an unparsable value is kept as free text; dropping it would clear the
	// input without the user learning anything
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
			} else if (fp.field === 'created') {
				const dr = parseDateRange(fp.value);
				if (dr) emit({ dateCreated: dr });
				else emitFreeText();
			} else if (fp.field === 'modified') {
				const dr = parseDateRange(fp.value);
				if (dr) emit({ dateModified: dr });
				else emitFreeText();
			} else if (fp.field === 'size') {
				const sz = parseSize(fp.value);
				if (sz) {
					const fs = { min: fileSize.min, max: fileSize.max };
					if (sz.op === '' || sz.op === '>=' || sz.op === '>') fs.min = sz.bytes;
					if (sz.op === '' || sz.op === '<=' || sz.op === '<') fs.max = sz.bytes;
					if (sz.op === '>') fs.max = null;
					if (sz.op === '<') fs.min = null;
					emit({ fileSize: fs });
				} else {
					emitFreeText();
				}
			} else if (fp.field === personAnyType) {
				emit({ people: [...people, { name: fp.value, type: personAnyType }] });
			} else if (isPersonField(fp.field)) {
				emit({ people: [...people, { name: fp.value, type: fp.field }] });
			} else {
				emitFreeText();
			}
		} else {
			emitFreeText();
		}
		inputValue = '';
	}

	function onKeyDown(e) {
		if (e.key === 'Enter') {
			e.preventDefault();
			onSubmit();
		} else if (e.key === 'Backspace' && !inputValue) {
			if (tags.length > 0) {
				emit({ tags: tags.slice(0, -1) });
			} else if (people.length > 0) {
				emit({ people: people.slice(0, -1) });
			} else if (documentType) {
				emit({ documentType: '' });
			} else if (language) {
				emit({ language: '' });
			} else if (dateCreated.from || dateCreated.to) {
				emit({ dateCreated: { from: null, to: null } });
			} else if (dateModified.from || dateModified.to) {
				emit({ dateModified: { from: null, to: null } });
			} else if (fileSize.min != null || fileSize.max != null) {
				emit({ fileSize: { min: null, max: null } });
			} else if (q) {
				emit({ q: '' });
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
		emit({ q: '' });
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
		{#each tags as tag, i (i)}
			<span
				class="inline-flex items-center gap-1 rounded-full bg-lapis-700 px-2.5 py-0.5 text-xs text-parchment-200"
			>
				tag:{tag}
				<button
					onclick={() => removeTag(i)}
					aria-label={`Remove tag ${tag}`}
					class="text-parchment-400 hover:text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					>&times;</button
				>
			</span>
		{/each}

		{#each people as p, i (i)}
			<span
				class="inline-flex items-center gap-1 rounded-full bg-lapis-700 px-2.5 py-0.5 text-xs text-parchment-200"
			>
				{p.type}:{p.name}
				<button
					onclick={() => removePerson(i)}
					aria-label={`Remove ${p.type} ${p.name}`}
					class="text-parchment-400 hover:text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					>&times;</button
				>
			</span>
		{/each}

		{#if documentType}
			<span
				class="inline-flex items-center gap-1 rounded-full bg-lapis-700 px-2.5 py-0.5 text-xs text-parchment-200"
			>
				type:{documentType}
				<button
					onclick={clearDocumentType}
					aria-label={`Remove document type ${documentType}`}
					class="text-parchment-400 hover:text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					>&times;</button
				>
			</span>
		{/if}

		{#if language}
			<span
				class="inline-flex items-center gap-1 rounded-full bg-emerald-700 px-2.5 py-0.5 text-xs text-parchment-200"
			>
				lang:{language}
				<button
					onclick={clearLanguage}
					aria-label={`Remove language ${language}`}
					class="text-parchment-400 hover:text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					>&times;</button
				>
			</span>
		{/if}

		{#if dateCreated.from || dateCreated.to}
			<span
				class="inline-flex items-center gap-1 rounded-full bg-amber-700 px-2.5 py-0.5 text-xs text-parchment-200"
			>
				created:{dateChipLabel(dateCreated)}
				<button
					onclick={clearDateCreated}
					aria-label="Remove created date filter"
					class="text-parchment-400 hover:text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					>&times;</button
				>
			</span>
		{/if}

		{#if dateModified.from || dateModified.to}
			<span
				class="inline-flex items-center gap-1 rounded-full bg-amber-700 px-2.5 py-0.5 text-xs text-parchment-200"
			>
				modified:{dateChipLabel(dateModified)}
				<button
					onclick={clearDateModified}
					aria-label="Remove modified date filter"
					class="text-parchment-400 hover:text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					>&times;</button
				>
			</span>
		{/if}

		{#if fileSize.min != null || fileSize.max != null}
			<span
				class="inline-flex items-center gap-1 rounded-full bg-purple-700 px-2.5 py-0.5 text-xs text-parchment-200"
			>
				size:{sizeChipLabel(fileSize)}
				<button
					onclick={clearFileSize}
					aria-label="Remove file size filter"
					class="text-parchment-400 hover:text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					>&times;</button
				>
			</span>
		{/if}

		{#if q}
			<span
				class="inline-flex items-center gap-1 rounded-full bg-clay-700 px-2.5 py-0.5 text-xs text-parchment-200"
			>
				{q}
				<button
					onclick={clearQuery}
					aria-label="Remove text filter"
					class="text-parchment-400 hover:text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					>&times;</button
				>
			</span>
		{/if}

		<Suggest
			id="searchbar"
			fetch={fetchSuggestions}
			onselect={onPick}
			onerror={() => toastStore.error('Could not load suggestions')}
			bind:query={inputValue}
			listClass="absolute top-full right-0 left-0 z-50 mt-1 max-h-60 overflow-y-auto rounded-lg border border-clay-800 bg-clay-900 shadow-lg"
			rowClass="px-3 py-2 text-sm"
		>
			{#snippet input({ oninput, onkeydown, close, panelOpen, listboxId, activeDescendant })}
				<input
					id="searchbar-input"
					name="searchbar-input"
					bind:this={inputEl}
					bind:value={inputValue}
					{oninput}
					onkeydown={(e) => {
						if (onkeydown(e)) return;
						onKeyDown(e);
					}}
					onblur={() => setTimeout(close, 200)}
					type="text"
					role="combobox"
					aria-label="Search documents"
					aria-expanded={panelOpen}
					aria-controls={listboxId}
					aria-autocomplete="list"
					aria-activedescendant={activeDescendant}
					autocomplete="off"
					spellcheck="false"
					placeholder={tags.length === 0 &&
					people.length === 0 &&
					!documentType &&
					!language &&
					!dateCreated.from &&
					!dateCreated.to &&
					!dateModified.from &&
					!dateModified.to &&
					fileSize.min == null &&
					fileSize.max == null &&
					!q
						? 'Search documents…'
						: ''}
					class="min-w-[120px] flex-1 border-0 bg-transparent p-0 text-parchment-200 placeholder-parchment-500 outline-none focus:ring-0"
				/>
			{/snippet}
		</Suggest>
	</div>
</div>
