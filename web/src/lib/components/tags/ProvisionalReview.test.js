import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import ProvisionalReview from './ProvisionalReview.svelte';

const { provisionalList, promoteTag, deleteTag, reevaluateIdentity, confirmMock, toastMock } =
	vi.hoisted(() => ({
		provisionalList: vi.fn(),
		promoteTag: vi.fn(),
		deleteTag: vi.fn(),
		reevaluateIdentity: vi.fn(),
		confirmMock: vi.fn(),
		toastMock: { success: vi.fn(), error: vi.fn() }
	}));

vi.mock('$app/state', () => ({
	page: { url: { searchParams: new URLSearchParams(), pathname: '/tags', hash: '' } }
}));
vi.mock('$app/navigation', () => ({ goto: vi.fn() }));
vi.mock('$app/paths', () => ({ resolve: (p) => p }));
vi.mock('#lib/api.js', async (importOriginal) => {
	const actual = await importOriginal();
	return {
		api: {
			tags: {
				provisionalList,
				promote: promoteTag,
				delete: deleteTag,
				reevaluateIdentity
			}
		},
		encodeCriteria: actual.encodeCriteria
	};
});
vi.mock('#lib/stores/confirmStore.svelte.js', () => ({ confirmStore: { confirm: confirmMock } }));
vi.mock('#lib/stores/toastStore.svelte.js', () => ({ toastStore: toastMock }));

import { page } from '$app/state';
import { goto } from '$app/navigation';

const rubric = {
	subject_matter: true,
	dimension: true,
	not_person: true,
	atomic: true,
	not_form_genre: true,
	defer: false
};

function tag(overrides = {}) {
	return {
		id: 1,
		name: 'sample tag',
		document_count: 1,
		created_at: '2026-09-01T00:00:00Z',
		pending_identity: 0,
		verdict: null,
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
	confirmMock.mockReset();
	toastMock.success.mockReset();
	toastMock.error.mockReset();
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

	it('renders the not publishing format criterion when a verdict is present', async () => {
		await renderLabel(tag({ verdict: verdict() }), /not publishing format/);
	});
});

describe('ProvisionalReview filters and paging', () => {
	it('sends an active criterion to the API and syncs it to the URL', async () => {
		page.url.hash = '#provisional';
		provisionalList.mockResolvedValue({ ok: true, data: { results: [], total: 0 } });
		render(ProvisionalReview);
		await waitFor(() => expect(provisionalList).toHaveBeenCalledTimes(1));

		await fireEvent.change(screen.getByRole('combobox', { name: /atomic/ }), {
			target: { value: 'fail' }
		});

		await waitFor(() =>
			expect(provisionalList).toHaveBeenLastCalledWith(50, 0, {
				criteria: [{ key: 'atomic', state: 'fail' }],
				verdict: ''
			})
		);
		expect(goto).toHaveBeenLastCalledWith('/tags?prov_criteria=atomic%3Afail#provisional', {
			shallow: true,
			replace: true
		});
		expect(await screen.findByText('No tags match the current filters.')).toBeTruthy();
	});

	it('sends the verdict filter to the API and syncs it to the URL', async () => {
		provisionalList.mockResolvedValue({ ok: true, data: { results: [], total: 0 } });
		render(ProvisionalReview);
		await waitFor(() => expect(provisionalList).toHaveBeenCalledTimes(1));

		await fireEvent.change(screen.getByRole('combobox', { name: /verdict/ }), {
			target: { value: 'none' }
		});

		await waitFor(() =>
			expect(provisionalList).toHaveBeenLastCalledWith(50, 0, {
				criteria: [],
				verdict: 'none'
			})
		);
		expect(goto).toHaveBeenLastCalledWith('/tags?prov_verdict=none', {
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

		expect(provisionalList).toHaveBeenLastCalledWith(50, 100, {
			criteria: [{ key: 'atomic', state: 'fail' }],
			verdict: 'none'
		});
		await waitFor(() => {
			expect(screen.getByRole('combobox', { name: /atomic/ }).value).toBe('fail');
			expect(screen.getByRole('combobox', { name: /verdict/ }).value).toBe('none');
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
		expect(provisionalList).toHaveBeenLastCalledWith(50, 50, { criteria: [], verdict: '' });
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
