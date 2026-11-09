import { DEFAULT_PAGE_SIZES, parseView, serializeView } from '#lib/list/listQuery.svelte.js';
import { parseDateRange, parseSize } from '#lib/utils/searchSyntax.js';

export const PERSON_ANY_TYPE = 'person';

const filterDefaults = {
	q: '',
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
};

const defaults = {
	...filterDefaults,
	page: 0,
	pageSize: 25,
	sortBy: 'created_at',
	sortOrder: 'desc'
};

const ownedKeys = [
	'q',
	'tag',
	'person',
	'type',
	'lang',
	'created_from',
	'created_to',
	'modified_from',
	'modified_to',
	'size_min',
	'size_max',
	'missing',
	'dt_page',
	'dt_size',
	'dt_sort',
	'dt_order'
];

const facetKeys = [
	'tag',
	'person',
	'type',
	'lang',
	'created_from',
	'created_to',
	'modified_from',
	'modified_to',
	'size_min',
	'size_max',
	'missing'
];

/**
 * The documents query codec. Free text lives in `q`; facets get their own parameters; `dt_*` carries
 * the view slice. Parsing is lenient and total.
 *
 * Legacy `q` reader: URLs written before the per-facet schema contain a `field:value` DSL in `q`.
 * When no facet parameter is present and `q` looks like that DSL, it is parsed into the facet fields
 * here and the next write emits the new format. Remove this reader one release after the schema ships.
 */
export const documentsQuery = {
	keys: ownedKeys,
	defaults,
	parse(searchParams) {
		const q = searchParams.get('q') ?? '';
		const view = parseView(searchParams, {
			prefix: 'dt',
			pageSizes: DEFAULT_PAGE_SIZES,
			defaultPageSize: defaults.pageSize,
			defaultSortBy: defaults.sortBy,
			defaultSortOrder: defaults.sortOrder
		});

		if (!facetKeys.some((key) => searchParams.has(key)) && /\w+:/.test(q)) {
			return { ...parseLegacyQuery(q), ...view };
		}

		return {
			q,
			tags: searchParams.getAll('tag'),
			people: searchParams.getAll('person').map(parsePerson),
			documentType: searchParams.get('type') ?? '',
			language: searchParams.get('lang') ?? '',
			dateCreated: {
				from: searchParams.get('created_from') || null,
				to: searchParams.get('created_to') || null
			},
			dateModified: {
				from: searchParams.get('modified_from') || null,
				to: searchParams.get('modified_to') || null
			},
			fileSize: {
				min: parseIntOrNull(searchParams.get('size_min')),
				max: parseIntOrNull(searchParams.get('size_max'))
			},
			missingLanguage: searchParams.getAll('missing').includes('lang'),
			missingType: searchParams.getAll('missing').includes('type'),
			untagged: searchParams.getAll('missing').includes('tags'),
			...view
		};
	},
	serialize(state) {
		const entries = [];
		if (state.q) entries.push(['q', state.q]);
		for (const tag of state.tags) entries.push(['tag', tag]);
		for (const person of state.people) entries.push(['person', serializePerson(person)]);
		if (state.documentType) entries.push(['type', state.documentType]);
		if (state.language) entries.push(['lang', state.language]);
		if (state.dateCreated.from) entries.push(['created_from', state.dateCreated.from]);
		if (state.dateCreated.to) entries.push(['created_to', state.dateCreated.to]);
		if (state.dateModified.from) entries.push(['modified_from', state.dateModified.from]);
		if (state.dateModified.to) entries.push(['modified_to', state.dateModified.to]);
		if (state.fileSize.min != null) entries.push(['size_min', String(state.fileSize.min)]);
		if (state.fileSize.max != null) entries.push(['size_max', String(state.fileSize.max)]);
		if (state.missingLanguage) entries.push(['missing', 'lang']);
		if (state.missingType) entries.push(['missing', 'type']);
		if (state.untagged) entries.push(['missing', 'tags']);
		entries.push(...serializeView(state, { prefix: 'dt', defaults }));
		return entries;
	}
};

function parseIntOrNull(raw) {
	if (raw == null || raw === '') return null;
	const parsed = parseInt(raw, 10);
	return Number.isFinite(parsed) && parsed >= 0 ? parsed : null;
}

function parsePerson(raw) {
	const separator = raw.indexOf(':');
	if (separator > 0) {
		return { type: raw.slice(0, separator), name: raw.slice(separator + 1) };
	}
	return { name: raw, type: PERSON_ANY_TYPE };
}

/**
 * Always prefixes the type, including the any-type marker, so a person name containing `:` can never
 * be misread as a type on the next parse.
 */
function serializePerson(person) {
	const type = person.type || PERSON_ANY_TYPE;
	return `${type}:${person.name}`;
}

/** Builds the structured search request body from a query state. */
export function searchRequestBody(state) {
	const body = {
		query: state.q,
		tags: state.tags,
		people: state.people,
		document_type: state.documentType || '',
		language: state.language || '',
		missing_language: state.missingLanguage || false,
		missing_type: state.missingType || false,
		untagged: state.untagged || false
	};
	if (state.dateCreated.from || state.dateCreated.to) {
		body.date_created = {
			from: state.dateCreated.from || null,
			to: state.dateCreated.to || null
		};
	}
	if (state.dateModified.from || state.dateModified.to) {
		body.date_modified = {
			from: state.dateModified.from || null,
			to: state.dateModified.to || null
		};
	}
	if (state.fileSize.min != null || state.fileSize.max != null) {
		body.file_size = { min: state.fileSize.min, max: state.fileSize.max };
	}
	return body;
}

/** The persisted saved-search payload: the filter fields, without view state. */
export function savedFilter(state) {
	return {
		query: state.q,
		tags: state.tags,
		people: state.people,
		documentType: state.documentType,
		language: state.language,
		dateCreated: state.dateCreated,
		dateModified: state.dateModified,
		fileSize: state.fileSize,
		missingLanguage: state.missingLanguage,
		missingType: state.missingType,
		untagged: state.untagged
	};
}

/**
 * Normalizes a stored saved-search payload into a complete filter patch. Entries written by the
 * previous version use the structured request body's snake_case shape; they are mapped here so old
 * saved searches keep loading. Every filter field is emitted, including empty and false values, so
 * applying a saved search replaces the current filter instead of merging into it.
 */
export function normalizeSavedFilter(raw) {
	const source = raw && typeof raw === 'object' ? raw : {};
	const dateRange = (value) => ({ from: value?.from ?? null, to: value?.to ?? null });
	const sizeRange = (value) => ({ min: value?.min ?? null, max: value?.max ?? null });
	const fileSize = source.fileSize ?? source.file_size;
	const dateCreated = source.dateCreated ?? source.date_created;
	const dateModified = source.dateModified ?? source.date_modified;

	return {
		q: source.query ?? source.q ?? '',
		tags: Array.isArray(source.tags) ? source.tags : [],
		people: Array.isArray(source.people) ? source.people.map(normalizePerson) : [],
		documentType: source.documentType ?? source.document_type ?? '',
		language: source.language ?? '',
		dateCreated: dateRange(dateCreated),
		dateModified: dateRange(dateModified),
		fileSize: sizeRange(fileSize),
		missingLanguage: Boolean(source.missingLanguage ?? source.missing_language),
		missingType: Boolean(source.missingType ?? source.missing_type),
		untagged: Boolean(source.untagged)
	};
}

function normalizePerson(person) {
	if (!person || typeof person !== 'object') return person;
	return { name: person.name ?? '', type: person.type || PERSON_ANY_TYPE };
}

/**
 * Splits the server-generated snippet HTML into text and highlight segments so the caller renders it
 * without injecting markup. The backend escapes all text and only re-introduces `<b>` markers.
 */
export function snippetSegments(html) {
	const parts = String(html ?? '').split(/(<\/?b>)/i);
	const segments = [];
	let bold = false;
	for (const part of parts) {
		if (/^<b>$/i.test(part)) {
			bold = true;
			continue;
		}
		if (/^<\/b>$/i.test(part)) {
			bold = false;
			continue;
		}
		if (part) segments.push({ text: decodeEntities(part), bold });
	}
	return segments;
}

function decodeEntities(text) {
	return text
		.replace(/&#34;|&quot;/g, '"')
		.replace(/&#39;|&apos;/g, "'")
		.replace(/&lt;/g, '<')
		.replace(/&gt;/g, '>')
		.replace(/&amp;/g, '&');
}

// legacy `q` DSL reader: kept only for one release so old links keep working; the new format is
// written on the next filter change, and tokens whose field is unknown here (typed person fields
// included) degrade to free text
function tokenizeLegacyQuery(str) {
	const tokens = [];
	let remaining = str.trim();
	while (remaining.length > 0) {
		const fieldMatch = remaining.match(
			/^(\w+):("(?:[^"]*)"|(?:\[(?:[^\]]*)\]|(?!\s)\([^)]*\)|(?!["\s\])])\S+))/
		);
		if (fieldMatch) {
			let value = fieldMatch[2];
			if (
				(value.startsWith('"') && value.endsWith('"')) ||
				(value.startsWith('[') && value.endsWith(']')) ||
				(value.startsWith('(') && value.endsWith(')'))
			) {
				value = value.slice(1, -1);
			}
			tokens.push({ type: 'field', field: fieldMatch[1], value, raw: fieldMatch[0] });
			remaining = remaining.slice(fieldMatch[0].length).trimStart();
			continue;
		}
		const wordMatch = remaining.match(/^\S+/);
		if (wordMatch) {
			tokens.push({ type: 'text', text: wordMatch[0], raw: wordMatch[0] });
			remaining = remaining.slice(wordMatch[0].length).trimStart();
			continue;
		}
		remaining = remaining.slice(1).trimStart();
	}
	return tokens;
}

function parseLegacyQuery(str) {
	const state = structuredClone(filterDefaults);
	const textParts = [];

	for (const token of tokenizeLegacyQuery(str)) {
		if (token.type === 'text') {
			textParts.push(token.text);
			continue;
		}
		switch (token.field) {
			case 'tag':
				state.tags.push(token.value);
				break;
			case 'type':
				state.documentType = token.value;
				break;
			case 'lang':
				state.language = token.value;
				break;
			case PERSON_ANY_TYPE:
				state.people.push({ name: token.value, type: PERSON_ANY_TYPE });
				break;
			case 'created': {
				const range = parseDateRange(token.value);
				if (range) state.dateCreated = range;
				else textParts.push(token.raw);
				break;
			}
			case 'modified': {
				const range = parseDateRange(token.value);
				if (range) state.dateModified = range;
				else textParts.push(token.raw);
				break;
			}
			case 'missing':
				if (token.value === 'lang') state.missingLanguage = true;
				else if (token.value === 'type') state.missingType = true;
				else if (token.value === 'tags') state.untagged = true;
				break;
			case 'size': {
				const size = parseSize(token.value);
				if (!size) {
					textParts.push(token.raw);
					break;
				}
				if (size.op === '' || size.op === '>=' || size.op === '>') state.fileSize.min = size.bytes;
				if (size.op === '' || size.op === '<=' || size.op === '<') state.fileSize.max = size.bytes;
				if (size.op === '>') state.fileSize.max = null;
				if (size.op === '<') state.fileSize.min = null;
				break;
			}
			default:
				textParts.push(token.raw);
		}
	}

	state.q = textParts.join(' ');
	return state;
}
