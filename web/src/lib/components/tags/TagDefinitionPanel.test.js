import { describe, it, expect, vi, beforeEach } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/svelte';
import TagDefinitionPanel from './TagDefinitionPanel.svelte';

const { forgetTerm, lookupTerm } = vi.hoisted(() => ({
	forgetTerm: vi.fn(),
	lookupTerm: vi.fn()
}));

vi.mock('#lib/utils/termLookup.js', () => ({ forgetTerm, lookupTerm }));

function result(overrides = {}) {
	return {
		wikipedia: { status: 'not-found' },
		wiktionary: { status: 'not-found' },
		dictionary: { status: 'not-found' },
		suggestions: [],
		...overrides
	};
}

describe('TagDefinitionPanel', () => {
	beforeEach(() => {
		forgetTerm.mockReset();
		lookupTerm.mockReset();
	});

	it('renders the Wikipedia extract with its attribution link', async () => {
		lookupTerm.mockResolvedValue(
			result({
				wikipedia: {
					status: 'ok',
					title: 'Sample term',
					description: 'A demonstration entry',
					extract: 'Sample term is a demonstration entry.',
					url: 'https://en.wikipedia.org/wiki/Sample_term',
					disambiguation: false
				}
			})
		);

		render(TagDefinitionPanel, { props: { term: 'sample term' } });

		expect(await screen.findByText('Sample term is a demonstration entry.')).toBeTruthy();
		const link = await screen.findByRole('link', { name: /Read on Wikipedia/ });
		expect(link.getAttribute('href')).toBe('https://en.wikipedia.org/wiki/Sample_term');
	});

	it('offers Wikipedia suggestions for a missing term and queries the picked title', async () => {
		lookupTerm
			.mockResolvedValueOnce(result({ suggestions: ['Blue Ridge Mountains', 'Blue water'] }))
			.mockResolvedValueOnce(
				result({
					wikipedia: {
						status: 'ok',
						title: 'Blue Ridge Mountains',
						extract: 'The Blue Ridge Mountains are a segment of the Appalachians.',
						url: 'https://en.wikipedia.org/wiki/Blue_Ridge_Mountains',
						disambiguation: false
					}
				})
			);

		render(TagDefinitionPanel, { props: { term: 'blue mountain' } });

		await screen.findByText(/No definition found in any source/);
		const suggestion = await screen.findByRole('button', { name: 'Blue Ridge Mountains' });
		await fireEvent.click(suggestion);

		expect(
			await screen.findByText('The Blue Ridge Mountains are a segment of the Appalachians.')
		).toBeTruthy();
		expect(lookupTerm).toHaveBeenLastCalledWith('Blue Ridge Mountains');
	});

	it('states explicitly when no source has a definition', async () => {
		lookupTerm.mockResolvedValue(result());

		render(TagDefinitionPanel, { props: { term: 'xylophone zither' } });

		expect(await screen.findByText(/No definition found in any source/)).toBeTruthy();
	});

	it('reports unreachable sources instead of claiming the term is undefined', async () => {
		lookupTerm.mockResolvedValue(
			result({
				wikipedia: { status: 'error' },
				wiktionary: { status: 'error' },
				dictionary: { status: 'error' }
			})
		);

		render(TagDefinitionPanel, { props: { term: 'offline term' } });

		expect(await screen.findByText(/The sources could not be reached/)).toBeTruthy();
		expect(screen.queryByText(/No definition found/)).toBeNull();
	});
});
