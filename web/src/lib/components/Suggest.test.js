import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import Suggest from './Suggest.svelte';

const fetcher = vi.fn();

function setup(props = {}) {
	const onselect = vi.fn();
	const onenter = vi.fn(() => false);
	render(Suggest, {
		id: 'case',
		label: 'Case',
		placeholder: 'Type to search…',
		fetch: fetcher,
		onselect,
		onenter,
		...props
	});
	return { onselect, onenter };
}

beforeEach(() => {
	fetcher.mockReset();
	vi.useRealTimers();
});

describe('Suggest', () => {
	it('fetches the first page once the query is typed and shows the load-more footer', async () => {
		vi.useFakeTimers();
		fetcher.mockResolvedValue({
			items: [{ name: 'alpha' }, { name: 'beta' }],
			total: 5
		});
		setup();

		await fireEvent.input(screen.getByLabelText('Case'), { target: { value: 'a' } });
		await vi.advanceTimersByTimeAsync(200);
		const option = await screen.findByRole('option', { name: 'alpha' });
		expect(option).toBeTruthy();
		expect(screen.getByRole('option', { name: 'beta' })).toBeTruthy();
		expect(screen.getByRole('button', { name: 'Load more (2 of 5)' })).toBeTruthy();
		expect(fetcher).toHaveBeenCalledTimes(1);
		expect(fetcher.mock.calls[0].slice(0, 2)).toEqual(['a', 0]);
	});

	it('appends the next page when the footer is clicked', async () => {
		vi.useFakeTimers();
		fetcher
			.mockResolvedValueOnce({
				items: [{ name: 'alpha' }, { name: 'beta' }],
				total: 3
			})
			.mockResolvedValueOnce({ items: [{ name: 'gamma' }], total: 3 });
		setup();

		await fireEvent.input(screen.getByLabelText('Case'), { target: { value: 'a' } });
		await vi.advanceTimersByTimeAsync(200);
		await screen.findByRole('option', { name: 'alpha' });

		await fireEvent.mouseDown(screen.getByRole('button', { name: /Load more/ }));
		await waitFor(() => expect(screen.getByRole('option', { name: 'gamma' })).toBeTruthy());
		expect(fetcher).toHaveBeenCalledTimes(2);
		expect(fetcher.mock.calls[1].slice(0, 2)).toEqual(['a', 2]);
	});

	it('hides the footer once every row is loaded', async () => {
		vi.useFakeTimers();
		fetcher.mockResolvedValue({
			items: [{ name: 'alpha' }, { name: 'beta' }],
			total: 2
		});
		setup();

		await fireEvent.input(screen.getByLabelText('Case'), { target: { value: 'a' } });
		await vi.advanceTimersByTimeAsync(200);
		await screen.findByRole('option', { name: 'alpha' });
		expect(screen.queryByRole('button', { name: /Load more/ })).toBeNull();
	});

	it('picks the highlighted row on Enter', async () => {
		vi.useFakeTimers();
		fetcher.mockResolvedValue({
			items: [{ name: 'alpha' }, { name: 'beta' }],
			total: 2
		});
		const { onselect } = setup();

		const input = screen.getByLabelText('Case');
		await fireEvent.input(input, { target: { value: 'a' } });
		await vi.advanceTimersByTimeAsync(200);
		await screen.findByRole('option', { name: 'alpha' });

		await fireEvent.keyDown(input, { key: 'ArrowDown' });
		await fireEvent.keyDown(input, { key: 'Enter' });
		await waitFor(() => expect(onselect).toHaveBeenCalledWith({ name: 'alpha' }));
	});

	it('delegates Enter without a highlight to onenter', async () => {
		vi.useFakeTimers();
		fetcher.mockResolvedValue({ items: [{ name: 'alpha' }], total: 1 });
		const { onenter, onselect } = setup();

		const input = screen.getByLabelText('Case');
		await fireEvent.input(input, { target: { value: 'a' } });
		await vi.advanceTimersByTimeAsync(200);
		await screen.findByRole('option', { name: 'alpha' });

		await fireEvent.keyDown(input, { key: 'Enter' });
		await waitFor(() => expect(onenter).toHaveBeenCalledWith('a', { name: 'alpha' }));
		expect(onselect).not.toHaveBeenCalled();
	});

	it('discards a stale in-flight response when the query changes', async () => {
		vi.useFakeTimers();
		let resolveFirst;
		fetcher
			.mockImplementationOnce(
				() =>
					new Promise((resolve) => {
						resolveFirst = resolve;
					})
			)
			.mockResolvedValueOnce({ items: [{ name: 'second' }], total: 1 });
		setup();

		const input = screen.getByLabelText('Case');
		await fireEvent.input(input, { target: { value: 'a' } });
		await vi.advanceTimersByTimeAsync(200);
		await fireEvent.input(input, { target: { value: 'ab' } });
		await vi.advanceTimersByTimeAsync(200);
		resolveFirst({ items: [{ name: 'stale' }], total: 1 });

		await screen.findByRole('option', { name: 'second' });
		expect(screen.queryByRole('option', { name: 'stale' })).toBeNull();
	});
});
