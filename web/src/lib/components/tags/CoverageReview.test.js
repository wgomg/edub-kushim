import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import CoverageReview from './CoverageReview.svelte';

const { auditFlags } = vi.hoisted(() => ({ auditFlags: vi.fn() }));

vi.mock('$app/state', () => ({
	page: { url: { searchParams: new URLSearchParams(), pathname: '/tags', hash: '' } }
}));
vi.mock('$app/navigation', () => ({ goto: vi.fn() }));
vi.mock('$app/paths', () => ({
	resolve: (path) => (String(path).startsWith('/') ? path : `/${path}`)
}));
vi.mock('#lib/api.js', () => ({
	api: {
		tags: {
			auditFlags,
			reevaluateAudit: vi.fn(),
			applyAuditFlag: vi.fn(),
			dismissAuditFlag: vi.fn(),
			createValue: vi.fn()
		}
	}
}));
vi.mock('#lib/stores/confirmStore.svelte.js', () => ({ confirmStore: { confirm: vi.fn() } }));
vi.mock('#lib/stores/toastStore.svelte.js', () => ({
	toastStore: { success: vi.fn(), error: vi.fn() }
}));

function flag(overrides = {}) {
	return {
		id: 1,
		document_id: 42,
		document_uuid: 'doc-uuid-1234',
		document_title: 'Coverage doc',
		tag_name: 'missing topic',
		class: 'missing',
		source: 'trigger',
		provisional: false,
		created_at: '2026-09-01T00:00:00Z',
		...overrides
	};
}

describe('CoverageReview', () => {
	beforeEach(() => auditFlags.mockReset());

	it('links the flagged document title to its detail page by public uuid', async () => {
		auditFlags.mockResolvedValue({ results: [flag()], total: 1 });

		render(CoverageReview);

		const link = await screen.findByRole('link', { name: 'Coverage doc' });
		expect(link.getAttribute('href')).toBe('/documents/doc-uuid-1234');
	});
});
