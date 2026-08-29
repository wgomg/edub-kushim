import { describe, it, expect, beforeEach } from 'vitest';
import {
	tokenizeQuery,
	parseQueryString,
	serializeFilter,
	parseSize,
	parseDateRange,
	setPersonTypes,
	defaultFilter
} from './searchFilter.js';

describe('tokenizeQuery', () => {
	it('splits plain text into word tokens', () => {
		expect(tokenizeQuery('invoice  acme')).toEqual([
			{ type: 'text', text: 'invoice', raw: 'invoice' },
			{ type: 'text', text: 'acme', raw: 'acme' }
		]);
	});

	it('parses field:value tokens with quoted, bracket and paren values', () => {
		expect(tokenizeQuery('tag:"acme corp"')).toEqual([
			{ type: 'field', field: 'tag', value: 'acme corp', raw: 'tag:"acme corp"' }
		]);
		expect(tokenizeQuery('tag:[a b]')).toEqual([
			{ type: 'field', field: 'tag', value: 'a b', raw: 'tag:[a b]' }
		]);
		expect(tokenizeQuery('tag:(a b)')).toEqual([
			{ type: 'field', field: 'tag', value: 'a b', raw: 'tag:(a b)' }
		]);
		expect(tokenizeQuery('type:pdf')).toEqual([
			{ type: 'field', field: 'type', value: 'pdf', raw: 'type:pdf' }
		]);
	});

	it('mixes field tokens and free text', () => {
		expect(tokenizeQuery('invoice lang:eng 2025')).toEqual([
			{ type: 'text', text: 'invoice', raw: 'invoice' },
			{ type: 'field', field: 'lang', value: 'eng', raw: 'lang:eng' },
			{ type: 'text', text: '2025', raw: '2025' }
		]);
	});

	it('returns no tokens for empty or whitespace input', () => {
		expect(tokenizeQuery('')).toEqual([]);
		expect(tokenizeQuery('   ')).toEqual([]);
	});
});

describe('parseSize', () => {
	it('parses plain byte counts and unit suffixes', () => {
		expect(parseSize('500')).toEqual({ op: '', bytes: 500 });
		expect(parseSize('1kb')).toEqual({ op: '', bytes: 1024 });
		expect(parseSize('1.5MB')).toEqual({ op: '', bytes: 1572864 });
		expect(parseSize('2GB')).toEqual({ op: '', bytes: 2147483648 });
	});

	it('parses comparison operators', () => {
		expect(parseSize('>1MB')).toEqual({ op: '>', bytes: 1048576 });
		expect(parseSize('>=1MB')).toEqual({ op: '>=', bytes: 1048576 });
		expect(parseSize('<512kb')).toEqual({ op: '<', bytes: 524288 });
		expect(parseSize('<=1GB')).toEqual({ op: '<=', bytes: 1073741824 });
	});

	it('rejects garbage input', () => {
		expect(parseSize('')).toBeNull();
		expect(parseSize('huge')).toBeNull();
		expect(parseSize('1MBx')).toBeNull();
	});
});

describe('parseDateRange', () => {
	it('parses from..to ranges with open ends', () => {
		expect(parseDateRange('2024-01-01..2024-12-31')).toEqual({
			from: '2024-01-01',
			to: '2024-12-31'
		});
		expect(parseDateRange('..2024-01-01')).toEqual({ from: null, to: '2024-01-01' });
		expect(parseDateRange('2024-01-01..')).toBeNull();
	});

	it('parses comparison operators and bare dates', () => {
		expect(parseDateRange('>2024-01-01')).toEqual({ from: '2024-01-01', to: null });
		expect(parseDateRange('>=2024-01-01')).toEqual({ from: '2024-01-01', to: null });
		expect(parseDateRange('<2024-01-01')).toEqual({ from: null, to: '2024-01-01' });
		expect(parseDateRange('<=2024-01-01')).toEqual({ from: null, to: '2024-01-01' });
		expect(parseDateRange('2024-01-01')).toEqual({ from: '2024-01-01', to: null });
		expect(parseDateRange('2024')).toEqual({ from: '2024', to: null });
	});

	it('rejects garbage input', () => {
		expect(parseDateRange('')).toBeNull();
		expect(parseDateRange('yesterday')).toBeNull();
		expect(parseDateRange('2024-1')).toBeNull();
	});
});

describe('parseQueryString', () => {
	beforeEach(() => {
		setPersonTypes([]);
	});

	it('maps tag, type, lang and person fields', () => {
		const f = parseQueryString('tag:"acme corp" tag:urgent type:invoice lang:eng person:Jane');
		expect(f.tags).toEqual(['acme corp', 'urgent']);
		expect(f.documentType).toBe('invoice');
		expect(f.language).toBe('eng');
		expect(f.people).toEqual([{ name: 'Jane', type: 'person' }]);
		expect(f.query).toBe('');
	});

	it('parses date, size and missing fields', () => {
		const f = parseQueryString(
			'created:2024-01-01..2024-06-30 modified:>2024-01-01 size:>1MB missing:lang missing:type missing:tags'
		);
		expect(f.dateCreated).toEqual({ from: '2024-01-01', to: '2024-06-30' });
		expect(f.dateModified).toEqual({ from: '2024-01-01', to: null });
		expect(f.fileSize).toEqual({ min: 1048576, max: null });
		expect(f.missingLanguage).toBe(true);
		expect(f.missingType).toBe(true);
		expect(f.untagged).toBe(true);
	});

	it('keeps free text and unknown fields in the query', () => {
		const f = parseQueryString('invoice foo:bar created:notadate');
		expect(f.query).toBe('invoice foo:bar created:notadate');
		expect(f.dateCreated).toEqual({ from: null, to: null });
	});

	it('resolves registered person types via setPersonTypes', () => {
		setPersonTypes([{ name: 'author' }, { name: 'recipient' }]);
		const f = parseQueryString('author:Jane recipient:"Acme Corp"');
		expect(f.people).toEqual([
			{ name: 'Jane', type: 'author' },
			{ name: 'Acme Corp', type: 'recipient' }
		]);
		expect(f.query).toBe('');
	});

	it('returns the default filter for an empty query', () => {
		const f = parseQueryString('');
		expect(f).toEqual({ ...defaultFilter, query: '' });
	});
});

describe('serializeFilter', () => {
	it('round-trips a fully populated filter', () => {
		const filter = {
			...defaultFilter,
			query: 'invoice',
			tags: ['acme corp'],
			people: [{ name: 'Jane', type: 'person' }],
			documentType: 'pdf',
			language: 'eng',
			dateCreated: { from: '2024-01-01', to: '2024-06-30' },
			dateModified: { from: '2024-01-01', to: null },
			fileSize: { min: 1048576, max: null },
			missingLanguage: true,
			missingType: false,
			untagged: true
		};
		const reparsed = parseQueryString(serializeFilter(filter));
		expect(reparsed).toEqual(filter);
	});

	it('serializes open-ended dates and sizes with operators', () => {
		expect(
			serializeFilter({
				...defaultFilter,
				dateCreated: { from: '2024-01-01', to: null },
				fileSize: { min: null, max: 1048576 }
			})
		).toBe('created:>2024-01-01 size:<1MB');
	});

	it('serializes an empty filter to an empty string', () => {
		expect(serializeFilter(defaultFilter)).toBe('');
	});
});
