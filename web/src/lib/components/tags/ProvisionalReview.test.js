import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor, within } from '@testing-library/svelte';
import ProvisionalReview from './ProvisionalReview.svelte';

const {
	provisionalList,
	promoteTag,
	deleteTag,
	reevaluateIdentity,
	foldTag,
	compositionDecision,
	splitTag,
	retireTag,
	acceptTag,
	separateTag,
	rejudgeTag,
	updateTag,
	confirmMock,
	toastMock,
	lookupTerm,
	forgetTerm
} = vi.hoisted(() => ({
	provisionalList: vi.fn(),
	promoteTag: vi.fn(),
	deleteTag: vi.fn(),
	reevaluateIdentity: vi.fn(),
	foldTag: vi.fn(),
	compositionDecision: vi.fn(),
	splitTag: vi.fn(),
	retireTag: vi.fn(),
	acceptTag: vi.fn(),
	separateTag: vi.fn(),
	rejudgeTag: vi.fn(),
	updateTag: vi.fn(),
	confirmMock: vi.fn(),
	toastMock: { success: vi.fn(), error: vi.fn() },
	lookupTerm: vi.fn(),
	forgetTerm: vi.fn()
}));

const pageMock = vi.hoisted(() => {
	const state = { sp: null, hash: '' };
	state.url = {
		get searchParams() {
			return state.sp;
		},
		set searchParams(next) {
			for (const key of [...state.sp.keys()]) state.sp.delete(key);
			for (const [key, value] of next) state.sp.append(key, value);
		},
		get search() {
			const value = state.sp ? state.sp.toString() : '';
			return value ? `?${value}` : '';
		},
		pathname: '/tags',
		get hash() {
			return state.hash;
		},
		set hash(value) {
			state.hash = value;
		}
	};
	return state;
});

vi.mock('$app/state', async () => {
	const { SvelteURLSearchParams } = await import('svelte/reactivity');
	pageMock.sp = new SvelteURLSearchParams();
	return { page: { url: pageMock.url } };
});
vi.mock('$app/navigation', () => ({
	goto: vi.fn((target) => {
		const url = new URL(String(target), 'http://localhost');
		const sp = pageMock.sp;
		for (const key of [...sp.keys()]) sp.delete(key);
		for (const [key, value] of url.searchParams) sp.append(key, value);
		pageMock.hash = url.hash;
	})
}));
vi.mock('$app/paths', () => ({ resolve: (p) => p }));
vi.mock('#lib/api.js', async (importOriginal) => {
	const actual = await importOriginal();
	return {
		api: {
			tags: {
				provisionalList,
				promote: promoteTag,
				delete: deleteTag,
				reevaluateIdentity,
				fold: foldTag,
				compositionDecision,
				split: splitTag,
				retire: retireTag,
				accept: acceptTag,
				separate: separateTag,
				rejudge: rejudgeTag,
				update: updateTag
			}
		},
		encodeCriteria: actual.encodeCriteria
	};
});
vi.mock('#lib/stores/confirmStore.svelte.js', () => ({ confirmStore: { confirm: confirmMock } }));
vi.mock('#lib/stores/toastStore.svelte.js', () => ({ toastStore: toastMock }));
vi.mock('#lib/utils/termLookup.js', () => ({ forgetTerm, lookupTerm }));

import { page } from '$app/state';
import { goto } from '$app/navigation';

const rubric = {
	subject_matter: true,
	dimension: true,
	person_class: 'not_person',
	atomic: true,
	not_form_genre: true,
	defer: false
};

const ALL_STATES = [
	'no_verdict',
	'admitted',
	'deferred',
	'rejected',
	'accepted',
	'identity_pending',
	'identity_hold',
	'split_hold',
	'degraded',
	'demoted',
	'needs_decision',
	'unblocked'
];

function tag(overrides = {}) {
	return {
		id: 1,
		name: 'sample tag',
		document_count: 1,
		created_at: '2026-09-01T00:00:00Z',
		pending_identity: 0,
		verdict: null,
		compound: true,
		...overrides
	};
}

function verdict(overrides = {}) {
	return {
		pass: false,
		criteria: rubric,
		model: 'judge-model',
		verdict_at: '2026-09-01T00:00:00Z',
		...overrides
	};
}

async function renderLabel(t, want) {
	provisionalList.mockResolvedValue({ ok: true, data: { results: [t], total: 1 } });
	render(ProvisionalReview);
	expect(await screen.findByText(want)).toBeTruthy();
}

beforeEach(() => {
	provisionalList.mockReset();
	promoteTag.mockReset();
	deleteTag.mockReset();
	reevaluateIdentity.mockReset();
	foldTag.mockReset();
	compositionDecision.mockReset();
	splitTag.mockReset();
	retireTag.mockReset();
	acceptTag.mockReset();
	separateTag.mockReset();
	rejudgeTag.mockReset();
	updateTag.mockReset();
	confirmMock.mockReset();
	confirmMock.mockResolvedValue(true);
	toastMock.success.mockReset();
	toastMock.error.mockReset();
	forgetTerm.mockReset();
	lookupTerm.mockReset();
	lookupTerm.mockResolvedValue({
		wikipedia: { status: 'not-found' },
		wiktionary: { status: 'not-found' },
		dictionary: { status: 'not-found' },
		suggestions: []
	});
	goto.mockClear();
	page.url.searchParams = new URLSearchParams();
	page.url.hash = '';
});

describe('ProvisionalReview verdict labels', () => {
	it('labels a recorded defer outcome as deferred to review', async () => {
		await renderLabel(tag({ verdict: verdict({ outcome: 'defer' }) }), 'deferred to review');
	});

	it('labels a recorded reject outcome as judge rejected', async () => {
		await renderLabel(tag({ verdict: verdict({ outcome: 'reject' }) }), 'judge rejected');
	});

	it('labels a recorded admit as awaiting the promotion floor', async () => {
		await renderLabel(
			tag({ verdict: verdict({ pass: true, outcome: 'admit' }) }),
			'awaiting promotion floor'
		);
	});

	it('falls back to the rubric abstention when no outcome was recorded', async () => {
		await renderLabel(
			tag({ verdict: verdict({ criteria: { ...rubric, defer: true } }) }),
			'deferred to review'
		);
	});

	it('falls back to judge rejected when a legacy row has no defer and no outcome', async () => {
		await renderLabel(tag({ verdict: verdict() }), 'judge rejected');
	});

	it('labels an unjudged tag as having no verdict', async () => {
		await renderLabel(tag(), 'no verdict');
	});

	it('shows the identity hold with its store anchor', async () => {
		await renderLabel(tag({ identity_hold: 'politics' }), 'identity hold → politics');
	});

	it('shows the split hold with its reason', async () => {
		await renderLabel(tag({ composition_hold: 'split_defer' }), 'split hold → split_defer');
	});

	it('shows the degraded composition reason without a hold', async () => {
		await renderLabel(
			tag({ composition_reason: 'judge_unavailable' }),
			'composition → judge_unavailable'
		);
	});

	it('hides the degraded composition pill for any other reason', async () => {
		provisionalList.mockResolvedValue({
			ok: true,
			data: { results: [tag({ composition_reason: 'named_unit' })], total: 1 }
		});
		render(ProvisionalReview);
		await screen.findByText('sample tag');

		expect(screen.queryByText(/composition →/)).toBeNull();
	});

	it('collapses the rubric to the failing criteria and the score', async () => {
		await renderLabel(
			tag({ verdict: verdict({ criteria: { ...rubric, dimension: false } }) }),
			'rubric 4/5'
		);
		expect(screen.getByText(/dimension\s*✗/)).toBeTruthy();
		expect(within(screen.getByRole('listitem')).queryByText(/not publishing format/)).toBeNull();
		expect(within(screen.getByRole('listitem')).queryByText(/defer\s*[✗—]/)).toBeNull();
	});

	it('renders the person class as its value, not a boolean mark', async () => {
		await renderLabel(
			tag({ verdict: verdict({ criteria: { ...rubric, person_class: 'real_individual' } }) }),
			'rubric 4/5'
		);
		expect(screen.getByText(/person class\s*real individual/)).toBeTruthy();
	});
});

describe('ProvisionalReview filters and paging', () => {
	it('sends an active criterion to the API and syncs it to the URL', async () => {
		page.url.hash = '#provisional';
		provisionalList.mockResolvedValue({ ok: true, data: { results: [], total: 0 } });
		render(ProvisionalReview);
		await waitFor(() => expect(provisionalList).toHaveBeenCalledTimes(1));

		await fireEvent.click(screen.getByRole('button', { name: 'Rubric' }));
		await fireEvent.click(screen.getByRole('button', { name: 'atomic: fail' }));

		await waitFor(() =>
			expect(provisionalList).toHaveBeenLastCalledWith(
				50,
				0,
				expect.objectContaining({
					criteria: [{ key: 'atomic', state: 'fail' }],
					states: [],
					q: '',
					sort: 'newest',
					facets: false
				})
			)
		);
		expect(goto).toHaveBeenLastCalledWith('/tags?prov_criteria=atomic%3Afail#provisional', {
			shallow: true,
			replace: true
		});
		expect(await screen.findByText('No tags match the current filters.')).toBeTruthy();
	});

	it('sends a person class as its class value and syncs it to the URL', async () => {
		page.url.hash = '#provisional';
		provisionalList.mockResolvedValue({ ok: true, data: { results: [], total: 0 } });
		render(ProvisionalReview);
		await waitFor(() => expect(provisionalList).toHaveBeenCalledTimes(1));

		await fireEvent.click(screen.getByRole('button', { name: 'Rubric' }));
		await fireEvent.click(screen.getByRole('button', { name: 'person class: real_individual' }));

		await waitFor(() =>
			expect(provisionalList).toHaveBeenLastCalledWith(
				50,
				0,
				expect.objectContaining({
					criteria: [{ key: 'person_class', state: 'real_individual' }],
					states: [],
					q: '',
					sort: 'newest',
					facets: false
				})
			)
		);
		expect(goto).toHaveBeenLastCalledWith('/tags?prov_criteria=person_class%3Areal_individual#provisional', {
			shallow: true,
			replace: true
		});
	});

	it('sends a review state to the API and syncs it to the URL', async () => {
		provisionalList.mockResolvedValue({ ok: true, data: { results: [], total: 0 } });
		render(ProvisionalReview);
		await waitFor(() => expect(provisionalList).toHaveBeenCalledTimes(1));

		await fireEvent.click(screen.getByRole('button', { name: 'Review state' }));
		await fireEvent.click(screen.getByRole('checkbox', { name: /no verdict/ }));

		await waitFor(() =>
			expect(provisionalList).toHaveBeenLastCalledWith(
				50,
				0,
				expect.objectContaining({
					criteria: [],
					states: ['no_verdict'],
					q: '',
					sort: 'newest',
					facets: true
				})
			)
		);
		expect(goto).toHaveBeenLastCalledWith('/tags?prov_state=no_verdict', {
			shallow: true,
			replace: true
		});
	});

	it('requests facet counts only while the review state menu is open', async () => {
		provisionalList.mockResolvedValue({
			ok: true,
			data: {
				results: [tag()],
				total: 1,
				state_values: ALL_STATES,
				state_facets: { identity_hold: 7, deferred: 2 }
			}
		});
		render(ProvisionalReview);
		await screen.findByText('sample tag');

		await fireEvent.click(screen.getByRole('button', { name: 'Review state' }));
		await waitFor(() =>
			expect(provisionalList).toHaveBeenLastCalledWith(
				50,
				0,
				expect.objectContaining({
					criteria: [],
					states: [],
					q: '',
					sort: 'newest',
					facets: true
				})
			)
		);
		await waitFor(() => {
			const box = screen.getByRole('checkbox', { name: /identity hold/ });
			expect(box.parentElement.textContent).toContain('7');
		});

		await fireEvent.keyDown(document, { key: 'Escape' });
		await fireEvent.click(screen.getByRole('button', { name: 'Rubric' }));
		await fireEvent.click(screen.getByRole('button', { name: 'atomic: fail' }));
		await waitFor(() =>
			expect(provisionalList).toHaveBeenLastCalledWith(
				50,
				0,
				expect.objectContaining({
					criteria: [{ key: 'atomic', state: 'fail' }],
					states: [],
					q: '',
					sort: 'newest',
					facets: false
				})
			)
		);
	});

	it('translates a legacy admit verdict link into both states', async () => {
		page.url.searchParams = new URLSearchParams('prov_verdict=admit');
		provisionalList.mockResolvedValue({
			ok: true,
			data: { results: [], total: 0, state_values: ALL_STATES }
		});
		render(ProvisionalReview);

		await waitFor(() =>
			expect(provisionalList).toHaveBeenLastCalledWith(
				50,
				0,
				expect.objectContaining({
					criteria: [],
					states: ['admitted', 'accepted'],
					q: '',
					sort: 'newest',
					facets: false
				})
			)
		);
		expect(
			await screen.findByRole('button', { name: 'Remove awaiting promotion floor filter' })
		).toBeTruthy();
		expect(screen.getByRole('button', { name: 'Remove accepted deferral filter' })).toBeTruthy();
	});

	it('offers only the states the server reports', async () => {
		provisionalList.mockResolvedValue({
			ok: true,
			data: { results: [tag()], total: 1, state_values: ['no_verdict', 'identity_hold'] }
		});
		render(ProvisionalReview);
		await screen.findByText('sample tag');

		await fireEvent.click(screen.getByRole('button', { name: 'Review state' }));
		expect(await screen.findByRole('checkbox', { name: 'no verdict' })).toBeTruthy();
		expect(screen.getByRole('checkbox', { name: 'identity hold' })).toBeTruthy();
		expect(screen.queryByRole('checkbox', { name: /^demoted/ })).toBeNull();
		expect(screen.queryByRole('checkbox', { name: /^split hold/ })).toBeNull();
	});

	it('sends the name query to the API after the debounce and resets paging', async () => {
		page.url.searchParams = new URLSearchParams('prov_page=1');
		provisionalList.mockResolvedValue({ ok: true, data: { results: [tag()], total: 51 } });
		render(ProvisionalReview);
		await waitFor(() => expect(provisionalList).toHaveBeenCalledWith(50, 50, expect.anything()));

		await fireEvent.input(screen.getByLabelText('Filter new tags by name'), {
			target: { value: 'politic' }
		});

		await waitFor(() =>
			expect(provisionalList).toHaveBeenLastCalledWith(
				50,
				0,
				expect.objectContaining({
					criteria: [],
					states: [],
					q: 'politic',
					sort: 'newest',
					facets: false
				})
			)
		);
		expect(goto).toHaveBeenLastCalledWith('/tags?prov_q=politic', {
			shallow: true,
			replace: true
		});
	});

	it('restores filters and page from the URL, dropping invalid entries', async () => {
		page.url.searchParams = new URLSearchParams(
			'prov_page=2&prov_criteria=atomic%3Afail,bogus%3Ax&prov_verdict=none'
		);
		provisionalList.mockResolvedValue({ ok: true, data: { results: [tag()], total: 120 } });
		render(ProvisionalReview);
		await screen.findByText('sample tag');

		expect(provisionalList).toHaveBeenLastCalledWith(
			50,
			100,
			expect.objectContaining({
				criteria: [{ key: 'atomic', state: 'fail' }],
				states: ['no_verdict'],
				q: '',
				sort: 'newest',
				facets: false
			})
		);
		await waitFor(() => {
			expect(screen.getByRole('button', { name: 'Remove atomic filter' })).toBeTruthy();
			expect(screen.getByRole('button', { name: 'Remove no verdict filter' })).toBeTruthy();
		});
	});

	it('disables both pager ends on the only page', async () => {
		provisionalList.mockResolvedValue({ ok: true, data: { results: [tag()], total: 1 } });
		render(ProvisionalReview);
		await screen.findByText('sample tag');

		expect(screen.getByRole('button', { name: 'Prev' }).disabled).toBe(true);
		expect(screen.getByRole('button', { name: 'Next' }).disabled).toBe(true);
	});

	it('pages forward and back with the pager buttons', async () => {
		provisionalList
			.mockResolvedValueOnce({ ok: true, data: { results: [tag()], total: 120 } })
			.mockResolvedValueOnce({
				ok: true,
				data: { results: [tag({ id: 2, name: 'second page tag' })], total: 120 }
			});
		render(ProvisionalReview);
		await screen.findByText('sample tag');

		expect(screen.getByRole('button', { name: 'Prev' }).disabled).toBe(true);
		const next = screen.getByRole('button', { name: 'Next' });
		expect(next.disabled).toBe(false);

		await fireEvent.click(next);
		await screen.findByText('second page tag');
		expect(provisionalList).toHaveBeenLastCalledWith(
			50,
			50,
			expect.objectContaining({
				criteria: [],
				states: [],
				q: '',
				sort: 'newest',
				facets: false
			})
		);
		expect(screen.getByRole('button', { name: 'Prev' }).disabled).toBe(false);
		expect(goto).toHaveBeenLastCalledWith('/tags?prov_page=1', { shallow: true, replace: true });
	});

	it('clamps to the last page when a reject empties the current page', async () => {
		page.url.searchParams = new URLSearchParams('prov_page=1');
		confirmMock.mockResolvedValue(true);
		deleteTag.mockResolvedValue({ ok: true, status: 200 });
		provisionalList
			.mockResolvedValueOnce({
				ok: true,
				data: { results: [tag({ id: 7, name: 'deep page tag' })], total: 51 }
			})
			.mockResolvedValueOnce({ ok: true, data: { results: [], total: 50 } })
			.mockResolvedValueOnce({ ok: true, data: { results: [tag({ id: 1 })], total: 50 } });
		render(ProvisionalReview);
		await screen.findByText('deep page tag');

		await fireEvent.click(screen.getByRole('button', { name: 'Reject' }));

		await waitFor(() => expect(provisionalList).toHaveBeenCalledTimes(3));
		expect(deleteTag).toHaveBeenCalledWith(7);
		expect(provisionalList.mock.calls[1][1]).toBe(50);
		expect(provisionalList.mock.calls[2][1]).toBe(0);
		await screen.findByText('sample tag');
		expect(screen.queryByText('deep page tag')).toBeNull();
		const lastUrl = goto.mock.calls.at(-1)[0];
		expect(lastUrl).not.toContain('prov_page');
	});

	it('shows the reevaluate button from the queue-level pending total', async () => {
		provisionalList.mockResolvedValue({
			ok: true,
			data: { results: [tag({ pending_identity: 0 })], total: 1, pending_identity_total: 3 }
		});
		render(ProvisionalReview);
		await screen.findByText('sample tag');

		expect(screen.getByRole('button', { name: /Reevaluate identity/ }).disabled).toBe(false);
	});

	it('hides the reevaluate button when nothing is pending', async () => {
		provisionalList.mockResolvedValue({
			ok: true,
			data: { results: [tag({ pending_identity: 0 })], total: 1, pending_identity_total: 0 }
		});
		render(ProvisionalReview);
		await screen.findByText('sample tag');

		expect(screen.queryByRole('button', { name: /Reevaluate identity/ })).toBeNull();
	});
});

describe('ProvisionalReview review actions', () => {
	async function renderEntry(overrides = {}) {
		provisionalList.mockResolvedValue({
			ok: true,
			data: { results: [tag(overrides)], total: 1 }
		});
		render(ProvisionalReview);
		await screen.findByText('sample tag');
	}

	it('offers both fold directions for an identity hold', async () => {
		await renderEntry({ identity_hold: 'politics' });

		expect(await screen.findByRole('button', { name: /Fold into “politics”/ })).toBeTruthy();
		expect(screen.getByRole('button', { name: 'Make canonical' })).toBeTruthy();
	});

	it('folds the tag into the held value after confirming', async () => {
		foldTag.mockResolvedValue({ ok: true, status: 200 });
		await renderEntry({ identity_hold: 'politics' });

		await fireEvent.click(screen.getByRole('button', { name: /Fold into “politics”/ }));

		await waitFor(() => expect(confirmMock).toHaveBeenCalledTimes(1));
		await waitFor(() => expect(foldTag).toHaveBeenCalledWith('sample tag', 'politics'));
	});

	it('offers keep separate for an identity hold', async () => {
		await renderEntry({ identity_hold: 'politics' });

		expect(await screen.findByRole('button', { name: 'Keep separate' })).toBeTruthy();
	});

	it('hides keep separate without an identity hold', async () => {
		await renderEntry();

		await screen.findByText('sample tag');
		expect(screen.queryByRole('button', { name: 'Keep separate' })).toBeNull();
	});

	it('keeps the values separate after confirming', async () => {
		separateTag.mockResolvedValue({ ok: true, status: 204 });
		await renderEntry({ identity_hold: 'politics' });

		await fireEvent.click(await screen.findByRole('button', { name: 'Keep separate' }));

		await waitFor(() => expect(confirmMock).toHaveBeenCalledTimes(1));
		await waitFor(() => expect(separateTag).toHaveBeenCalledWith(1));
	});

	it('offers the manual fold on every entry and opens the target picker', async () => {
		await renderEntry();

		await fireEvent.click(await screen.findByRole('button', { name: 'Fold into…' }));

		expect(await screen.findByRole('dialog', { name: 'Fold tag into another' })).toBeTruthy();
		expect(screen.getByLabelText('Target tag')).toBeTruthy();
	});

	it('offers split and keep for an undecided split hold', async () => {
		await renderEntry({ composition_hold: 'split_defer', composition_action: 'undecided' });

		expect(await screen.findByRole('button', { name: /Split now/ })).toBeTruthy();
		expect(screen.getByRole('button', { name: 'Keep whole' })).toBeTruthy();
	});

	it('opens the parts editor instead of splitting immediately', async () => {
		await renderEntry({ composition_hold: 'split_defer', composition_action: 'undecided' });

		await fireEvent.click(await screen.findByRole('button', { name: /Split now/ }));

		expect(await screen.findByRole('dialog', { name: 'Split compound' })).toBeTruthy();
		expect(compositionDecision).not.toHaveBeenCalled();
	});

	it('offers the manual split on every compound entry and opens the parts editor', async () => {
		await renderEntry();

		await fireEvent.click(await screen.findByRole('button', { name: 'Split…' }));

		expect(await screen.findByRole('dialog', { name: 'Split compound' })).toBeTruthy();
		expect(compositionDecision).not.toHaveBeenCalled();
		expect(splitTag).not.toHaveBeenCalled();
	});

	it('hides the manual split for a single-word tag', async () => {
		provisionalList.mockResolvedValue({
			ok: true,
			data: { results: [tag({ name: 'history', compound: false })], total: 1 }
		});
		render(ProvisionalReview);
		await screen.findByText('history');

		expect(screen.queryByRole('button', { name: 'Split…' })).toBeNull();
	});

	it('posts a split decision for a compound the judge kept whole', async () => {
		splitTag.mockResolvedValue({ ok: true, status: 204 });
		await renderEntry({
			composition_hold: 'named_unit_reconstructible',
			composition_action: 'keep',
			composition_parts: ['part a']
		});

		await fireEvent.click(await screen.findByRole('button', { name: 'Split…' }));
		await screen.findByRole('dialog', { name: 'Split compound' });
		expect(screen.getByLabelText('Component 1').value).toBe('part a');
		await fireEvent.click(screen.getByRole('button', { name: /^Split$/ }));

		await waitFor(() => expect(splitTag).toHaveBeenCalledWith('sample tag', ['part a']));
		expect(compositionDecision).not.toHaveBeenCalled();
	});

	it('reorders the queue oldest-first and records it in the url', async () => {
		await renderEntry();

		await fireEvent.click(screen.getByRole('button', { name: 'Oldest' }));

		await waitFor(() =>
			expect(provisionalList).toHaveBeenLastCalledWith(
				50,
				0,
				expect.objectContaining({ sort: 'oldest' })
			)
		);
		expect(goto.mock.calls.at(-1)[0]).toContain('prov_sort=oldest');
	});

	it('offers apply-parts alongside keep only when a keep hold carries parts', async () => {
		await renderEntry({
			composition_hold: 'named_unit_reconstructible',
			composition_action: 'keep',
			composition_parts: ['part a']
		});

		expect(await screen.findByRole('button', { name: 'Apply parts alongside' })).toBeTruthy();
		expect(screen.getByRole('button', { name: 'Keep whole' })).toBeTruthy();
	});

	it('offers no inline composition action for a decided drop', async () => {
		await renderEntry({ composition_hold: 'no_dimension', composition_action: 'drop' });

		expect(await screen.findByRole('button', { name: 'Retire' })).toBeTruthy();
		expect(screen.queryByRole('button', { name: /Split now/ })).toBeNull();
		expect(screen.queryByRole('button', { name: 'Keep whole' })).toBeNull();
	});

	it('queues a composition re-judge for a degraded hold', async () => {
		rejudgeTag.mockResolvedValue({ ok: true, status: 200 });
		await renderEntry({ composition_hold: 'judge_unavailable', composition_action: 'degraded' });

		await fireEvent.click(await screen.findByRole('button', { name: 'Re-judge' }));

		await waitFor(() => expect(rejudgeTag).toHaveBeenCalledWith(1, 'composition'));
	});

	it('always offers approve, reject, retire and rename', async () => {
		await renderEntry();

		expect(await screen.findByRole('button', { name: 'Approve' })).toBeTruthy();
		expect(screen.getByRole('button', { name: 'Reject' })).toBeTruthy();
		expect(screen.getByRole('button', { name: 'Retire' })).toBeTruthy();
		expect(screen.getByRole('button', { name: 'Rename' })).toBeTruthy();
	});

	it('offers a labelled help button in the queue header', async () => {
		await renderEntry();

		expect(screen.getByRole('button', { name: 'Help' })).toBeTruthy();
	});

	it('offers judge now only while the tag carries no verdict', async () => {
		await renderEntry();
		expect(await screen.findByRole('button', { name: 'Judge now' })).toBeTruthy();
	});

	it('hides judge now once a verdict exists', async () => {
		await renderEntry({ verdict: verdict() });

		expect(await screen.findByRole('button', { name: 'judge rejected' })).toBeTruthy();
		expect(screen.queryByRole('button', { name: 'Judge now' })).toBeNull();
	});

	it('promotes the tag when approved', async () => {
		promoteTag.mockResolvedValue({ ok: true, status: 200 });
		await renderEntry();

		await fireEvent.click(screen.getByRole('button', { name: 'Approve' }));

		await waitFor(() => expect(promoteTag).toHaveBeenCalledWith(1));
	});

	it('retires the tag after confirming', async () => {
		retireTag.mockResolvedValue({ ok: true, status: 200 });
		await renderEntry();

		await fireEvent.click(screen.getByRole('button', { name: 'Retire' }));

		await waitFor(() => expect(confirmMock).toHaveBeenCalledTimes(1));
		await waitFor(() => expect(retireTag).toHaveBeenCalledWith(1));
	});

	it('offers accept as provisional for a bare deferral', async () => {
		await renderEntry({ verdict: verdict({ outcome: 'defer' }) });

		expect(await screen.findByRole('button', { name: 'Accept as provisional' })).toBeTruthy();
	});

	it('hides accept while another review concern holds the tag', async () => {
		await renderEntry({ verdict: verdict({ outcome: 'defer' }), identity_hold: 'politics' });

		expect(await screen.findByRole('button', { name: /Fold into “politics”/ })).toBeTruthy();
		expect(screen.queryByRole('button', { name: 'Accept as provisional' })).toBeNull();
	});

	it('shows the accepted pill and no accept action once accepted', async () => {
		await renderEntry({
			verdict: verdict({ outcome: 'defer' }),
			accepted_at: '2026-09-02T00:00:00Z'
		});

		expect(await screen.findByText('accepted — awaiting promotion floor')).toBeTruthy();
		expect(screen.queryByRole('button', { name: 'Accept as provisional' })).toBeNull();
	});

	it('accepts the tag after confirming', async () => {
		acceptTag.mockResolvedValue({ ok: true, status: 204 });
		await renderEntry({ verdict: verdict({ outcome: 'defer' }) });

		await fireEvent.click(await screen.findByRole('button', { name: 'Accept as provisional' }));

		await waitFor(() => expect(confirmMock).toHaveBeenCalledTimes(1));
		await waitFor(() => expect(acceptTag).toHaveBeenCalledWith(1));
	});

	it('renames the tag from the queue', async () => {
		updateTag.mockResolvedValue({ ok: true, status: 200 });
		await renderEntry();

		await fireEvent.click(screen.getByRole('button', { name: 'Rename' }));
		const dialog = await screen.findByRole('dialog', { name: 'Rename tag' });
		expect(dialog).toBeTruthy();

		await fireEvent.input(screen.getByLabelText('Name'), { target: { value: 'renamed tag' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Save' }));

		await waitFor(() => expect(updateTag).toHaveBeenCalledWith(1, 'renamed tag'));
	});

	// skipped: TagReviewHelp.svelte's $effect calls target.scrollIntoView, which jsdom does not
	// implement, so opening the drawer throws under the test environment (reported for a decision
	// on the component call or a setup polyfill)
	it.skip('opens the tag review guide from a badge', async () => {
		await renderEntry({ identity_hold: 'politics' });

		await fireEvent.click(await screen.findByRole('button', { name: /identity hold/ }));

		expect(await screen.findByRole('dialog', { name: 'Tag review guide' })).toBeTruthy();
	});

	it.skip('opens the tag review guide from the queue header', async () => {
		await renderEntry();

		await fireEvent.click(screen.getByRole('button', { name: 'Help' }));

		expect(await screen.findByRole('dialog', { name: 'Tag review guide' })).toBeTruthy();
	});
});

describe('ProvisionalReview definition lookup', () => {
	it('opens the definition panel from the tag name', async () => {
		provisionalList.mockResolvedValue({ ok: true, data: { results: [tag()], total: 1 } });
		render(ProvisionalReview);
		await screen.findByText('sample tag');

		await fireEvent.click(screen.getByRole('button', { name: 'sample tag' }));

		expect(await screen.findByRole('dialog', { name: 'Tag definition' })).toBeTruthy();
		expect(lookupTerm).toHaveBeenCalledWith('sample tag');
	});
});
