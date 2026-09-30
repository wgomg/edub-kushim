import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import ProvisionalReview from './ProvisionalReview.svelte';

const { provisionalList } = vi.hoisted(() => ({ provisionalList: vi.fn() }));

vi.mock('$app/paths', () => ({ resolve: (p) => p }));
vi.mock('$lib/api', () => ({
	api: {
		tags: {
			provisionalList,
			promote: vi.fn(),
			delete: vi.fn(),
			reevaluateIdentity: vi.fn()
		}
	}
}));

const rubric = {
	subject_matter: true,
	dimension: true,
	not_person_place_work: true,
	atomic: true,
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

describe('ProvisionalReview verdict labels', () => {
	beforeEach(() => {
		provisionalList.mockReset();
	});

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
});
