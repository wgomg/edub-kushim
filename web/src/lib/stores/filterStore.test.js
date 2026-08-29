import { describe, it, expect, beforeEach } from 'vitest';
import { get } from 'svelte/store';
import { filterStore, queryString } from './filterStore.js';
import { defaultFilter } from './searchFilter.js';

describe('filterStore', () => {
	beforeEach(() => {
		filterStore.reset();
	});

	it('setPartial merges into the current filter', () => {
		filterStore.setPartial({ tags: ['acme'] });
		expect(get(filterStore).tags).toEqual(['acme']);
		expect(get(filterStore).query).toBe('');
	});

	it('reset restores the default filter', () => {
		filterStore.setPartial({ tags: ['acme'], language: 'eng' });
		filterStore.reset();
		expect(get(filterStore)).toEqual({ ...defaultFilter });
	});

	it('fromQueryString replaces the whole filter', () => {
		filterStore.setPartial({ tags: ['acme'] });
		filterStore.fromQueryString('lang:eng invoice');
		const f = get(filterStore);
		expect(f.language).toBe('eng');
		expect(f.query).toBe('invoice');
		expect(f.tags).toEqual([]);
	});

	it('derives the serialized query string', () => {
		filterStore.setPartial({ tags: ['acme corp'], language: 'eng' });
		expect(get(queryString)).toBe('tag:"acme corp" lang:"eng"');
	});
});
