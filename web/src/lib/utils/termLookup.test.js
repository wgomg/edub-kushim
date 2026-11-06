import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { forgetTerm, lookupTerm } from './termLookup.js';

function jsonResponse(status, body) {
	return { status, ok: status >= 200 && status < 300, json: async () => body };
}

function wikiSummary(overrides = {}) {
	return {
		type: 'standard',
		title: 'Serendipity',
		description: 'Faculty of making fortunate discoveries',
		extract: 'Serendipity is the occurrence of events by chance in a happy or beneficial way.',
		content_urls: { desktop: { page: 'https://en.wikipedia.org/wiki/Serendipity' } },
		...overrides
	};
}

describe('termLookup', () => {
	let fetchMock;

	beforeEach(() => {
		fetchMock = vi.fn();
		vi.stubGlobal('fetch', fetchMock);
	});

	afterEach(() => {
		vi.unstubAllGlobals();
	});

	it('normalizes all three sources for a hit without asking Wikipedia search', async () => {
		fetchMock.mockImplementation(async (url) => {
			const u = String(url);
			if (u.includes('/page/summary/')) return jsonResponse(200, wikiSummary());
			if (u.includes('/page/definition/'))
				return jsonResponse(200, {
					en: [
						{
							partOfSpeech: 'Noun',
							definitions: [
								{
									definition: 'A <b>fortunate</b> discovery',
									examples: ['What <i>luck</i>!']
								}
							]
						}
					]
				});
			if (u.includes('dictionaryapi.dev'))
				return jsonResponse(200, [
					{
						phonetics: [{ text: '/ˌserənˈdipɪti/' }],
						meanings: [
							{ partOfSpeech: 'noun', definitions: [{ definition: 'Chance occurrence.' }] }
						]
					}
				]);
			throw new Error(`unexpected fetch: ${u}`);
		});

		const result = await lookupTerm('serendipity');

		expect(result.wikipedia).toMatchObject({
			status: 'ok',
			disambiguation: false,
			title: 'Serendipity',
			url: 'https://en.wikipedia.org/wiki/Serendipity'
		});
		expect(result.wikipedia.extract).toContain('Serendipity is the occurrence');
		expect(result.wiktionary.senses[0].definition).toBe('A fortunate discovery');
		expect(result.wiktionary.senses[0].examples[0]).not.toContain('<');
		expect(result.dictionary.phonetic).toBe('/ˌserənˈdipɪti/');
		expect(result.dictionary.senses[0].definition).toBe('Chance occurrence.');
		expect(result.suggestions).toEqual([]);
		expect(fetchMock).toHaveBeenCalledTimes(3);
	});

	it('maps a Wikipedia miss to not-found and falls back to search suggestions', async () => {
		fetchMock.mockImplementation(async (url) => {
			const u = String(url);
			if (u.includes('/page/summary/')) return jsonResponse(404, {});
			if (u.includes('action=query'))
				return jsonResponse(200, {
					query: { search: [{ title: 'Blue Ridge Mountains' }, { title: 'Blue water' }] }
				});
			if (u.includes('/page/definition/')) return jsonResponse(404, {});
			if (u.includes('dictionaryapi.dev')) return jsonResponse(404, { message: 'no entry' });
			throw new Error(`unexpected fetch: ${u}`);
		});

		const result = await lookupTerm('blue mountain');

		expect(result.wikipedia.status).toBe('not-found');
		expect(result.wiktionary.status).toBe('not-found');
		expect(result.dictionary.status).toBe('not-found');
		expect(result.suggestions).toEqual(['Blue Ridge Mountains', 'Blue water']);
		expect(fetchMock).toHaveBeenCalledTimes(4);
	});

	it('flags a disambiguation summary, suppresses its list extract, and suggests titles', async () => {
		fetchMock.mockImplementation(async (url) => {
			const u = String(url);
			if (u.includes('/page/summary/'))
				return jsonResponse(
					200,
					wikiSummary({
						type: 'disambiguation',
						title: 'Mercury cell',
						extract: 'Mercury cell may refer to: a battery, a voltaic cell'
					})
				);
			if (u.includes('action=query'))
				return jsonResponse(200, {
					query: { search: [{ title: 'Mercury (planet)' }, { title: 'Mercury (element)' }] }
				});
			if (u.includes('/page/definition/')) return jsonResponse(404, {});
			if (u.includes('dictionaryapi.dev')) return jsonResponse(404, {});
			throw new Error(`unexpected fetch: ${u}`);
		});

		const result = await lookupTerm('mercury cell');

		expect(result.wikipedia).toMatchObject({ status: 'ok', disambiguation: true, extract: '' });
		expect(result.suggestions).toEqual(['Mercury (planet)', 'Mercury (element)']);
		expect(fetchMock).toHaveBeenCalledTimes(4);
	});

	it('memoizes a resolved lookup across case and whitespace differences', async () => {
		fetchMock.mockImplementation(async (url) => {
			const u = String(url);
			if (u.includes('/page/summary/')) return jsonResponse(200, wikiSummary());
			if (u.includes('/page/definition/')) return jsonResponse(404, {});
			if (u.includes('dictionaryapi.dev')) return jsonResponse(404, {});
			throw new Error(`unexpected fetch: ${u}`);
		});

		const first = await lookupTerm('orbits');
		const second = await lookupTerm('  Orbits  ');

		expect(second).toBe(first);
		expect(fetchMock).toHaveBeenCalledTimes(3);
	});

	it('drops the cache entry when every source fails so a retry refetches', async () => {
		fetchMock.mockRejectedValue(new TypeError('network down'));

		const failed = await lookupTerm('radial speed');
		expect([failed.wikipedia.status, failed.wiktionary.status, failed.dictionary.status]).toEqual([
			'error',
			'error',
			'error'
		]);

		fetchMock.mockImplementation(async (url) => {
			const u = String(url);
			if (u.includes('/page/summary/'))
				return jsonResponse(200, wikiSummary({ title: 'Radial speed' }));
			if (u.includes('/page/definition/')) return jsonResponse(404, {});
			if (u.includes('dictionaryapi.dev')) return jsonResponse(404, {});
			throw new Error(`unexpected fetch: ${u}`);
		});

		const retried = await lookupTerm('radial speed');
		expect(fetchMock).toHaveBeenCalledTimes(6);
		expect(retried.wikipedia).toMatchObject({ status: 'ok', title: 'Radial speed' });
	});

	it('forgets a term so the next lookup starts a fresh fetch', async () => {
		fetchMock.mockImplementation(async (url) => {
			const u = String(url);
			if (u.includes('/page/summary/')) return jsonResponse(200, wikiSummary());
			if (u.includes('/page/definition/')) return jsonResponse(404, {});
			if (u.includes('dictionaryapi.dev')) return jsonResponse(404, {});
			throw new Error(`unexpected fetch: ${u}`);
		});

		await lookupTerm('chiaroscuro');
		forgetTerm('chiaroscuro');
		await lookupTerm('chiaroscuro');

		expect(fetchMock).toHaveBeenCalledTimes(6);
	});
});
