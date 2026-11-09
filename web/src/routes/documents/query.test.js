import { describe, it, expect, vi } from 'vitest';

vi.mock('$app/navigation', () => ({ goto: vi.fn() }));
vi.mock('$app/paths', () => ({ resolve: (path) => path }));
vi.mock('$app/state', () => ({ page: { url: new URL('http://localhost/') } }));

import {
	documentsQuery,
	normalizeSavedFilter,
	PERSON_ANY_TYPE,
	savedFilter,
	searchRequestBody,
	snippetSegments
} from './query.js';

function parse(queryString) {
	return documentsQuery.parse(new URLSearchParams(queryString));
}

describe('documentsQuery.parse', () => {
	it('falls back per key for invalid view values', () => {
		const state = parse('dt_page=-4&dt_size=999&dt_sort=bogus&dt_order=sideways');
		expect(state.page).toBe(0);
		expect(state.pageSize).toBe(25);
		expect(state.sortOrder).toBe('desc');
	});

	it('parses every facet parameter', () => {
		const state = parse(
			'q=report&tag=finance&tag=annual&person=author%3AJane&person=person%3AACME%3A+Europe' +
				'&type=Invoice&lang=eng&created_from=2024-01&created_to=2024-06&size_min=1024' +
				'&missing=lang&missing=tags'
		);
		expect(state.q).toBe('report');
		expect(state.tags).toEqual(['finance', 'annual']);
		expect(state.people).toEqual([
			{ type: 'author', name: 'Jane' },
			{ type: PERSON_ANY_TYPE, name: 'ACME: Europe' }
		]);
		expect(state.documentType).toBe('Invoice');
		expect(state.language).toBe('eng');
		expect(state.dateCreated).toEqual({ from: '2024-01', to: '2024-06' });
		expect(state.fileSize).toEqual({ min: 1024, max: null });
		expect(state.missingLanguage).toBe(true);
		expect(state.untagged).toBe(true);
		expect(state.missingType).toBe(false);
	});

	it('round-trips person names containing a colon through serialize', () => {
		const entries = documentsQuery.serialize(parse('person=person%3AACME%3A+Europe'));
		expect(entries).toContainEqual(['person', 'person:ACME: Europe']);
		expect(parse('person=person%3AACME%3A+Europe').people).toEqual([
			{ type: PERSON_ANY_TYPE, name: 'ACME: Europe' }
		]);
	});
});

describe('legacy q reader', () => {
	it('maps the old DSL into facet parameters', () => {
		const state = parse(
			'q=tag%3A%22annual+report%22+lang%3Aeng+missing%3Alang+search+size%3A%3E2mb'
		);
		expect(state.tags).toEqual(['annual report']);
		expect(state.language).toBe('eng');
		expect(state.missingLanguage).toBe(true);
		expect(state.q).toBe('search');
		expect(state.fileSize).toEqual({ min: 2 * 1024 * 1024, max: null });
	});

	it('keeps bare size syntax as both bounds and date ranges as from/to', () => {
		const state = parse('q=created%3A2024..2025+size%3A10kb');
		expect(state.dateCreated).toEqual({ from: '2024', to: '2025' });
		expect(state.fileSize).toEqual({ min: 10240, max: 10240 });
	});

	it('ignores the DSL when the new format is already used', () => {
		const state = parse('q=tag%3Afake&tag=real');
		expect(state.q).toBe('tag:fake');
		expect(state.tags).toEqual(['real']);
	});
});

describe('searchRequestBody', () => {
	it('keeps the legacy snake_case wire shape and omits empty ranges', () => {
		expect(searchRequestBody(parse(''))).toEqual({
			query: '',
			tags: [],
			people: [],
			document_type: '',
			language: '',
			missing_language: false,
			missing_type: false,
			untagged: false
		});

		const withRanges = searchRequestBody(
			parse('created_from=2024&size_min=10&size_max=20&type=Invoice&lang=eng')
		);
		expect(withRanges.date_created).toEqual({ from: '2024', to: null });
		expect(withRanges.file_size).toEqual({ min: 10, max: 20 });
		expect(withRanges.document_type).toBe('Invoice');
		expect(withRanges.language).toBe('eng');
	});
});

describe('saved searches', () => {
	it('normalizes a legacy snake_case payload into a complete replacement patch', () => {
		const patch = normalizeSavedFilter({
			query: 'annual',
			tags: ['finance'],
			document_type: 'Invoice',
			missing_language: true,
			date_created: { from: '2024', to: null }
		});
		expect(patch).toEqual({
			q: 'annual',
			tags: ['finance'],
			people: [],
			documentType: 'Invoice',
			language: '',
			dateCreated: { from: '2024', to: null },
			dateModified: { from: null, to: null },
			fileSize: { min: null, max: null },
			missingLanguage: true,
			missingType: false,
			untagged: false
		});
	});

	it('accepts the current camelCase payload and preserves explicit empties', () => {
		const patch = normalizeSavedFilter({
			query: '',
			tags: [],
			people: [{ name: 'Jane', type: 'author' }],
			documentType: '',
			missingType: false
		});
		expect(patch.q).toBe('');
		expect(patch.documentType).toBe('');
		expect(patch.missingType).toBe(false);
		expect(patch.people).toEqual([{ name: 'Jane', type: 'author' }]);
	});

	it('saves the codec state shape', () => {
		expect(savedFilter(parse('q=x&tag=a'))).toMatchObject({ query: 'x', tags: ['a'] });
	});
});

describe('snippetSegments', () => {
	it('splits bold markers and decodes the escaped text', () => {
		expect(snippetSegments('AT&amp;T <b>annual</b> &lt;report&gt;')).toEqual([
			{ text: 'AT&T ', bold: false },
			{ text: 'annual', bold: true },
			{ text: ' <report>', bold: false }
		]);
	});

	it('returns no segments for empty input', () => {
		expect(snippetSegments(null)).toEqual([]);
	});
});
