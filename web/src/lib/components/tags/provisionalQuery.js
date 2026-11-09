import { parseView } from '#lib/list/listQuery.svelte.js';

const PAGE_SIZE = 50;

export const criteria = [
	{ key: 'subject_matter', label: 'subject matter' },
	{ key: 'dimension', label: 'dimension' },
	{ key: 'not_person', label: 'not person' },
	{ key: 'atomic', label: 'atomic' },
	{ key: 'not_form_genre', label: 'not publishing format' },
	{ key: 'defer', label: 'defer' }
];

// review states the compact bar offers, grouped exactly as the menu renders them; the values mirror
// the server's canonical list
export const reviewStates = [
	{ value: 'no_verdict', label: 'no verdict', group: 'Verdict' },
	{ value: 'admitted', label: 'awaiting promotion floor', group: 'Verdict' },
	{ value: 'deferred', label: 'deferred to review', group: 'Verdict' },
	{ value: 'rejected', label: 'judge rejected', group: 'Verdict' },
	{ value: 'accepted', label: 'accepted deferral', group: 'Verdict' },
	{ value: 'identity_pending', label: 'identity pending', group: 'Blocking' },
	{ value: 'identity_hold', label: 'identity hold', group: 'Blocking' },
	{ value: 'split_hold', label: 'split hold', group: 'Blocking' },
	{ value: 'degraded', label: 'degraded composition', group: 'Blocking' },
	{ value: 'demoted', label: 'demoted', group: 'Blocking' },
	{ value: 'needs_decision', label: 'needs decision', group: 'Action' },
	{ value: 'unblocked', label: 'awaiting floor, unblocked', group: 'Action' }
];

// older links carry the single verdict filter; map it onto the states the server newly exposes so they
// keep landing filtered; the old admit class folded accepted deferrals in, so it maps them to both
// admitted and accepted
const LEGACY_VERDICT_STATES = {
	none: ['no_verdict'],
	admit: ['admitted', 'accepted'],
	defer: ['deferred'],
	reject: ['rejected']
};

const CRITERION_STATES = new Set(['pass', 'fail', 'absent']);

const defaults = {
	q: '',
	criteria: [],
	states: [],
	sort: 'newest',
	page: 0,
	pageSize: PAGE_SIZE,
	sortBy: '',
	sortOrder: 'desc'
};

/**
 * Codec for the provisional-review URL slice. `prov_q`, `prov_criteria`, `prov_state`, `prov_sort`
 * and `prov_page` are the slice; the legacy `prov_verdict` is mapped onto states on read and
 * dropped on every write.
 */
export const provisionalQuery = {
	keys: ['prov_page', 'prov_q', 'prov_criteria', 'prov_state', 'prov_sort', 'prov_verdict'],
	defaults,
	parse(searchParams) {
		const view = parseView(searchParams, {
			prefix: 'prov',
			pageSizes: [PAGE_SIZE],
			defaultPageSize: PAGE_SIZE,
			defaultSortBy: '',
			defaultSortOrder: 'desc'
		});

		const activeCriteria = [];
		for (const entry of (searchParams.get('prov_criteria') ?? '').split(',')) {
			const separator = entry.indexOf(':');
			if (separator < 0) continue;
			const key = entry.slice(0, separator);
			const state = entry.slice(separator + 1);
			if (criteria.some((criterion) => criterion.key === key) && CRITERION_STATES.has(state)) {
				activeCriteria.push({ key, state });
			}
		}

		const states = [];
		for (const value of (searchParams.get('prov_state') ?? '').split(',')) {
			if (reviewStates.some((state) => state.value === value) && !states.includes(value)) {
				states.push(value);
			}
		}
		if (states.length === 0) {
			for (const value of LEGACY_VERDICT_STATES[searchParams.get('prov_verdict') ?? ''] ?? []) {
				if (reviewStates.some((state) => state.value === value) && !states.includes(value)) {
					states.push(value);
				}
			}
		}

		return {
			q: searchParams.get('prov_q') ?? '',
			criteria: activeCriteria,
			states,
			sort: searchParams.get('prov_sort') === 'oldest' ? 'oldest' : 'newest',
			page: view.page,
			pageSize: PAGE_SIZE,
			sortBy: '',
			sortOrder: 'desc'
		};
	},
	serialize(state) {
		const entries = [];
		if (state.page > 0) entries.push(['prov_page', String(state.page)]);
		if (state.q) entries.push(['prov_q', state.q]);
		const activeCriteria = state.criteria.map((entry) => `${entry.key}:${entry.state}`).join(',');
		if (activeCriteria) entries.push(['prov_criteria', activeCriteria]);
		if (state.states.length > 0) entries.push(['prov_state', state.states.join(',')]);
		if (state.sort === 'oldest') entries.push(['prov_sort', 'oldest']);
		return entries;
	}
};
