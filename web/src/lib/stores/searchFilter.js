let _personTypes = new Set();

export function setPersonTypes(types) {
	_personTypes = new Set(types.map((t) => t.name));
}

export function getPersonTypes() {
	return _personTypes;
}

export const defaultFilter = Object.freeze({
	query: '',
	tags: [],
	people: [],
	documentType: '',
	language: '',
	mimeType: '',
	dateCreated: { from: null, to: null },
	dateModified: { from: null, to: null },
	fileSize: { min: null, max: null },
	missingLanguage: false,
	missingType: false,
	untagged: false,
	sortBy: 'created_at',
	sortOrder: 'desc',
	limit: 25,
	offset: 0
});

const SIZE_UNITS = { b: 1, kb: 1024, mb: 1048576, gb: 1073741824 };

export function parseSize(raw) {
	const m = raw.match(/^([<>]=?)?\s*(-?[\d.]+)\s*(kb|mb|gb|b)?$/i);
	if (!m) return null;
	const op = m[1] || '';
	const num = parseFloat(m[2]);
	const unit = (m[3] || 'b').toLowerCase();
	const bytes = Math.round(num * (SIZE_UNITS[unit] || 1));
	return { op, bytes };
}

export function formatSize(bytes) {
	if (bytes >= 1073741824) return (bytes / 1073741824).toFixed(1).replace(/\.0$/, '') + 'GB';
	if (bytes >= 1048576) return (bytes / 1048576).toFixed(1).replace(/\.0$/, '') + 'MB';
	if (bytes >= 1024) return (bytes / 1024).toFixed(0) + 'KB';
	return bytes + 'B';
}

/**
 * Tokenizes a raw query string into structured tokens.
 * Supports field:value syntax (tag:, author:, type:, lang:, mime:)
 * and plain free-text words.
 */
export function tokenizeQuery(str) {
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

export function parseDateRange(raw) {
	const rangeMatch = raw.match(/^([^.]*)\.\.(.+)$/);
	if (rangeMatch) {
		return { from: rangeMatch[1] || null, to: rangeMatch[2] || null };
	}
	const opMatch = raw.match(/^([<>]=?)\s*(.+)$/);
	if (opMatch) {
		if (opMatch[1] === '>' || opMatch[1] === '>=') return { from: opMatch[2], to: null };
		if (opMatch[1] === '<' || opMatch[1] === '<=') return { from: null, to: opMatch[2] };
	}
	const dateMatch = raw.match(/^\d{4}(-\d{2}(-\d{2})?)?$/);
	if (dateMatch) {
		return { from: raw, to: null };
	}
	return null;
}

/**
 * Parses a raw query string into a complete filter object.
 */
export function parseQueryString(str) {
	const filter = {
		query: '',
		tags: [],
		people: [],
		documentType: '',
		language: '',
		mimeType: '',
		dateCreated: { from: null, to: null },
		dateModified: { from: null, to: null },
		fileSize: { min: null, max: null },
		missingLanguage: false,
		missingType: false,
		untagged: false,
		sortBy: 'created_at',
		sortOrder: 'desc',
		limit: 25,
		offset: 0
	};

	const tokens = tokenizeQuery(str);
	const textParts = [];

	for (const token of tokens) {
		if (token.type === 'field') {
			switch (token.field) {
				case 'tag':
					filter.tags.push(token.value);
					break;
				case 'type':
					filter.documentType = token.value;
					break;
				case 'lang':
					filter.language = token.value;
					break;
				case 'mime':
					filter.mimeType = token.value;
					break;
				case 'created': {
					const dr = parseDateRange(token.value);
					if (dr) filter.dateCreated = dr;
					else textParts.push(token.raw);
					break;
				}
				case 'modified': {
					const dr = parseDateRange(token.value);
					if (dr) filter.dateModified = dr;
					else textParts.push(token.raw);
					break;
				}
				case 'missing':
					if (token.value === 'lang') filter.missingLanguage = true;
					else if (token.value === 'type') filter.missingType = true;
					else if (token.value === 'tags') filter.untagged = true;
					break;
				case 'size': {
					const sz = parseSize(token.value);
					if (sz) {
						if (sz.op === '' || sz.op === '>=' || sz.op === '>') filter.fileSize.min = sz.bytes;
						if (sz.op === '' || sz.op === '<=' || sz.op === '<') filter.fileSize.max = sz.bytes;
						if (sz.op === '>') filter.fileSize.max = null;
						if (sz.op === '<') filter.fileSize.min = null;
					} else {
						textParts.push(token.raw);
					}
					break;
				}
				default:
					if (_personTypes.has(token.field)) {
						filter.people.push({ name: token.value, type: token.field });
					} else {
						textParts.push(token.raw);
					}
			}
		} else {
			textParts.push(token.text);
		}
	}

	filter.query = textParts.join(' ');
	return filter;
}

/**
 * Serializes a filter object back into a query string.
 */
export function serializeFilter(filter) {
	const parts = [];

	if (filter.query) {
		parts.push(filter.query);
	}

	for (const tag of filter.tags) {
		parts.push(`tag:${tag}`);
	}

	for (const p of filter.people) {
		if (p.name.includes(' ')) {
			parts.push(`${p.type}:"${p.name}"`);
		} else {
			parts.push(`${p.type}:${p.name}`);
		}
	}

	if (filter.documentType) {
		parts.push(`type:${filter.documentType}`);
	}

	if (filter.language) {
		parts.push(`lang:${filter.language}`);
	}

	if (filter.mimeType) {
		parts.push(`mime:${filter.mimeType}`);
	}

	if (filter.dateCreated?.from && filter.dateCreated?.to) {
		parts.push(`created:${filter.dateCreated.from}..${filter.dateCreated.to}`);
	} else if (filter.dateCreated?.from) {
		parts.push(`created:>${filter.dateCreated.from}`);
	} else if (filter.dateCreated?.to) {
		parts.push(`created:<${filter.dateCreated.to}`);
	}

	if (filter.dateModified?.from && filter.dateModified?.to) {
		parts.push(`modified:${filter.dateModified.from}..${filter.dateModified.to}`);
	} else if (filter.dateModified?.from) {
		parts.push(`modified:>${filter.dateModified.from}`);
	} else if (filter.dateModified?.to) {
		parts.push(`modified:<${filter.dateModified.to}`);
	}

	if (filter.missingLanguage) parts.push('missing:lang');
	if (filter.missingType) parts.push('missing:type');
	if (filter.untagged) parts.push('missing:tags');

	if (filter.fileSize?.min != null && filter.fileSize?.max != null) {
		parts.push(`size:${formatSize(filter.fileSize.min)}..${formatSize(filter.fileSize.max)}`);
	} else if (filter.fileSize?.min != null) {
		parts.push(`size:>${formatSize(filter.fileSize.min)}`);
	} else if (filter.fileSize?.max != null) {
		parts.push(`size:<${formatSize(filter.fileSize.max)}`);
	}

	return parts.join(' ');
}
