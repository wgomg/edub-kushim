import { describe, it, expect, vi } from 'vitest';

vi.mock('$app/navigation', () => ({ goto: vi.fn() }));
vi.mock('$app/paths', () => ({ resolve: (path) => path }));
vi.mock('$app/state', () => ({ page: { url: new URL('http://localhost/') } }));

import { provisionalQuery } from './provisionalQuery.js';

function parse(queryString) {
	return provisionalQuery.parse(new URLSearchParams(queryString));
}

describe('provisionalQuery', () => {
	it('maps the legacy verdict parameter only when no states are present', () => {
		expect(parse('prov_verdict=admit').states).toEqual(['admitted', 'accepted']);
		expect(parse('prov_state=deferred&prov_verdict=admit').states).toEqual(['deferred']);
		expect(parse('prov_verdict=bogus').states).toEqual([]);
	});

	it('drops invalid criteria, states and page values', () => {
		const state = parse('prov_criteria=bogus%3Afail%2Catomic%3Amaybe%2Cdefer%3Apass&prov_page=-1');
		expect(state.criteria).toEqual([{ key: 'defer', state: 'pass' }]);
		expect(state.page).toBe(0);
	});

	it('serializes the legacy verdict away and the active slice back', () => {
		const entries = provisionalQuery.serialize({
			...parse('prov_verdict=admit'),
			page: 1,
			q: 'annual',
			sort: 'oldest'
		});
		expect(entries).toContainEqual(['prov_page', '1']);
		expect(entries).toContainEqual(['prov_q', 'annual']);
		expect(entries).toContainEqual(['prov_state', 'admitted,accepted']);
		expect(entries).toContainEqual(['prov_sort', 'oldest']);
		expect(entries.some(([key]) => key === 'prov_verdict')).toBe(false);
	});
});
