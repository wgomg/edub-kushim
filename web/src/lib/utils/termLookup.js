// retrieves the meaning of a tag or term from each free keyless public source; results are memoized
// for the session and a lookup never throws, so callers render every failure path as an explicit state

const WIKIPEDIA = 'https://en.wikipedia.org';
const WIKTIONARY = 'https://en.wiktionary.org';
const FREE_DICTIONARY = 'https://api.dictionaryapi.dev/api/v2/entries/en';
const TIMEOUT_MS = 8000;
const MAX_SENSES = 12;

const cache = new Map();

export function lookupTerm(term) {
	const clean = String(term ?? '').trim();
	const key = cacheKey(clean);
	if (!key) {
		return Promise.resolve({
			wikipedia: { status: 'not-found' },
			wiktionary: { status: 'not-found' },
			dictionary: { status: 'not-found' },
			suggestions: []
		});
	}
	let pending = cache.get(key);
	if (!pending) {
		pending = runLookup(clean).then((result) => {
			const allFailed =
				result.wikipedia.status === 'error' &&
				result.wiktionary.status === 'error' &&
				result.dictionary.status === 'error';
			if (allFailed) cache.delete(key);
			return result;
		});
		cache.set(key, pending);
	}
	return pending;
}

export function forgetTerm(term) {
	cache.delete(cacheKey(term));
}

function cacheKey(term) {
	return String(term ?? '')
		.trim()
		.toLowerCase();
}

async function runLookup(term) {
	const [wikipedia, wiktionary, dictionary] = await Promise.all([
		lookupWikipedia(term),
		lookupWiktionary(term),
		lookupDictionary(term)
	]);
	const suggestions =
		wikipedia.status === 'not-found' || (wikipedia.status === 'ok' && wikipedia.disambiguation)
			? await lookupSuggestions(term)
			: [];
	return { wikipedia, wiktionary, dictionary, suggestions };
}

async function fetchJson(url) {
	const controller = new AbortController();
	const timer = setTimeout(() => controller.abort(), TIMEOUT_MS);
	try {
		const res = await fetch(url, {
			signal: controller.signal,
			headers: { Accept: 'application/json' }
		});
		if (res.status === 404) return { status: 'not-found' };
		if (!res.ok) return { status: 'error' };
		return { status: 'ok', data: await res.json() };
	} catch {
		return { status: 'error' };
	} finally {
		clearTimeout(timer);
	}
}

async function lookupWikipedia(term) {
	const res = await fetchJson(`${WIKIPEDIA}/api/rest_v1/page/summary/${encodeURIComponent(term)}`);
	if (res.status !== 'ok') return res;
	const page = res.data ?? {};
	const disambiguation = page.type === 'disambiguation';
	return {
		status: 'ok',
		title: page.title ?? term,
		description: page.description ?? '',
		extract: disambiguation ? '' : (page.extract ?? ''),
		url: safeUrl(page.content_urls?.desktop?.page),
		disambiguation
	};
}

async function lookupSuggestions(term) {
	const url = `${WIKIPEDIA}/w/api.php?action=query&list=search&srsearch=${encodeURIComponent(term)}&srlimit=5&format=json&origin=*`;
	const res = await fetchJson(url);
	if (res.status !== 'ok') return [];
	const hits = res.data?.query?.search;
	if (!Array.isArray(hits)) return [];
	return hits.map((hit) => hit?.title).filter(Boolean);
}

async function lookupWiktionary(term) {
	const res = await fetchJson(
		`${WIKTIONARY}/api/rest_v1/page/definition/${encodeURIComponent(term)}`
	);
	if (res.status !== 'ok') return res;
	const entries = Array.isArray(res.data?.en) ? res.data.en : [];
	const senses = [];
	for (const entry of entries) {
		if (senses.length >= MAX_SENSES) break;
		const partOfSpeech = entry?.partOfSpeech ?? '';
		const definitions = Array.isArray(entry?.definitions) ? entry.definitions : [];
		for (const definition of definitions.slice(0, 3)) {
			if (senses.length >= MAX_SENSES) break;
			const text = stripHtml(definition?.definition);
			if (!text) continue;
			const examples = Array.isArray(definition?.examples)
				? definition.examples.map(stripHtml).filter(Boolean).slice(0, 1)
				: [];
			senses.push({ partOfSpeech, definition: text, examples });
		}
	}
	if (senses.length === 0) return { status: 'not-found' };
	return {
		status: 'ok',
		url: `${WIKTIONARY}/wiki/${encodeURIComponent(term.replace(/\s+/g, '_'))}`,
		senses
	};
}

async function lookupDictionary(term) {
	const res = await fetchJson(`${FREE_DICTIONARY}/${encodeURIComponent(term)}`);
	if (res.status !== 'ok') return res;
	const entries = Array.isArray(res.data) ? res.data : [];
	const phonetics = [];
	const senses = [];
	for (const entry of entries) {
		for (const phonetic of entry?.phonetics ?? []) {
			if (phonetic?.text && !phonetics.includes(phonetic.text)) phonetics.push(phonetic.text);
		}
		for (const meaning of entry?.meanings ?? []) {
			if (senses.length >= MAX_SENSES) break;
			const definition = meaning?.definitions?.[0];
			if (!definition?.definition) continue;
			senses.push({
				partOfSpeech: meaning?.partOfSpeech ?? '',
				definition: definition.definition,
				examples: definition.example ? [definition.example] : []
			});
		}
	}
	if (senses.length === 0) return { status: 'not-found' };
	return {
		status: 'ok',
		phonetic: phonetics[0] ?? '',
		url: 'https://dictionaryapi.dev/',
		senses
	};
}

function safeUrl(value) {
	const url = String(value ?? '');
	return url.startsWith('https://') ? url : '';
}

function stripHtml(value) {
	return String(value ?? '')
		.replace(/<[^>]*>/g, ' ')
		.replace(/&nbsp;/gi, ' ')
		.replace(/&amp;/gi, '&')
		.replace(/&lt;/gi, '<')
		.replace(/&gt;/gi, '>')
		.replace(/&quot;/gi, '"')
		.replace(/&#0?39;/g, "'")
		.replace(/\s+/g, ' ')
		.trim();
}
