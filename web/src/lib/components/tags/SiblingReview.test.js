import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import SiblingReview from './SiblingReview.svelte';

const { siblings } = vi.hoisted(() => ({ siblings: vi.fn() }));

vi.mock('$app/state', () => ({
	page: { url: { searchParams: new URLSearchParams(), pathname: '/tags', hash: '' } }
}));
vi.mock('$app/navigation', () => ({ goto: vi.fn() }));
vi.mock('$app/paths', () => ({ resolve: (p) => p }));
vi.mock('#lib/api.js', () => ({ api: { tags: { siblings } } }));

function pair(overrides = {}) {
	return {
		a: 'broad topic',
		b: 'narrow topic',
		containment: 0.9,
		mass_a: 9,
		mass_b: 4,
		gray: false,
		...overrides
	};
}

describe('SiblingReview', () => {
	beforeEach(() => siblings.mockReset());

	it('renders the pair as container ⊇ contained with its overlap and masses', async () => {
		siblings.mockResolvedValue({ absent: false, run_id: 'run-1', total: 1, results: [pair()] });

		render(SiblingReview);

		expect(await screen.findByText('broad topic ⊇ narrow topic')).toBeTruthy();
		expect(screen.getByText('90%')).toBeTruthy();
		expect(screen.getByText('9 · 4')).toBeTruthy();
		expect(screen.getByText('For reference')).toBeTruthy();
	});

	it('marks the gray band for review and counts the run pairs', async () => {
		siblings.mockResolvedValue({
			absent: false,
			run_id: 'run-1',
			total: 2,
			results: [
				pair({ containment: 0.7, mass_a: 5, mass_b: 4, gray: true }),
				pair({ a: 'other broad', b: 'other narrow' })
			]
		});

		render(SiblingReview);

		expect(await screen.findByText('Needs review')).toBeTruthy();
		expect(screen.getByText('70%')).toBeTruthy();
		expect(screen.getByText(/overlapping pairs/)).toBeTruthy();
	});

	it('shows the zero-data state when no measurement run exists', async () => {
		siblings.mockResolvedValue({ absent: true, run_id: '', total: 0, results: [] });

		render(SiblingReview);

		expect(await screen.findByText(/No measurement run yet/)).toBeTruthy();
	});
});
