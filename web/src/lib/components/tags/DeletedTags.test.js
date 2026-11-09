import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import { tick } from 'svelte';

vi.mock('$app/navigation', () => ({ goto: vi.fn() }));
vi.mock('$app/paths', () => ({ resolve: (path) => path }));
vi.mock('$app/state', () => ({ page: { url: new URL('http://localhost/tags') } }));

const apiMocks = vi.hoisted(() => ({
	deletedList: vi.fn(),
	restoreDeleted: vi.fn()
}));

vi.mock('#lib/api.js', () => ({
	api: {
		tags: {
			deletedList: apiMocks.deletedList,
			restoreDeleted: apiMocks.restoreDeleted
		}
	}
}));

vi.mock('#lib/stores/authStore.svelte.js', () => ({
	authEnabled: () => true,
	isEditor: () => true
}));

import DeletedTags from './DeletedTags.svelte';

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

function deferred() {
	let resolve;
	const promise = new Promise((r) => {
		resolve = r;
	});
	return { promise, resolve };
}

async function typeFilter(value) {
	const input = screen.getByLabelText('Filter blocked names');
	await fireEvent.focus(input);
	await fireEvent.input(input, { target: { value } });
	return input;
}

beforeEach(() => {
	apiMocks.deletedList.mockReset().mockResolvedValue({ results: [], total: 0 });
	apiMocks.restoreDeleted.mockReset();
});

describe('DeletedTags list', () => {
	it('renders the blocked names the endpoint returns', async () => {
		apiMocks.deletedList.mockResolvedValue({
			results: [{ name: 'blocked-one', deleted_at: '2026-01-01T00:00:00Z' }],
			total: 1
		});
		render(DeletedTags);
		expect(await screen.findByText('blocked-one')).toBeTruthy();
		expect(apiMocks.deletedList).toHaveBeenCalledWith(
			expect.objectContaining({ q: '', offset: 0 })
		);
	});

	it('commits a typed filter after the debounce', async () => {
		render(DeletedTags);
		await typeFilter('zz');
		await waitFor(
			() => expect(apiMocks.deletedList).toHaveBeenCalledWith(expect.objectContaining({ q: 'zz' })),
			{
				timeout: 2000
			}
		);
		expect(apiMocks.deletedList.mock.calls[0][0]).toEqual(expect.objectContaining({ q: '' }));
	});

	it('blur commits the pending filter without waiting for the debounce', async () => {
		render(DeletedTags);
		const input = await typeFilter('yy');
		await fireEvent.blur(input);
		await tick();
		expect(apiMocks.deletedList).toHaveBeenCalledWith(expect.objectContaining({ q: 'yy' }));
	});

	it('unmounting cancels a pending commit', async () => {
		const { unmount } = render(DeletedTags);
		await waitFor(() => expect(apiMocks.deletedList).toHaveBeenCalledTimes(1));
		await typeFilter('zz');
		unmount();
		await sleep(400);
		expect(apiMocks.deletedList).toHaveBeenCalledTimes(1);
	});

	it('typing back to the committed value drops the pending write', async () => {
		render(DeletedTags);
		const input = await typeFilter('aa');
		await fireEvent.input(input, { target: { value: '' } });
		await sleep(400);
		expect(apiMocks.deletedList).toHaveBeenCalledTimes(1);
		expect(apiMocks.deletedList.mock.calls[0][0]).toEqual(expect.objectContaining({ q: '' }));
	});

	it('a stale response cannot overwrite a newer one', async () => {
		const first = deferred();
		const second = deferred();
		apiMocks.deletedList
			.mockResolvedValueOnce({ results: [{ name: 'initial' }], total: 1 })
			.mockImplementationOnce(() => first.promise)
			.mockImplementationOnce(() => second.promise);
		render(DeletedTags);
		await screen.findByText('initial');

		await typeFilter('x');
		await waitFor(() => expect(apiMocks.deletedList).toHaveBeenCalledTimes(2), { timeout: 2000 });
		await typeFilter('xy');
		await waitFor(() => expect(apiMocks.deletedList).toHaveBeenCalledTimes(3), { timeout: 2000 });

		second.resolve({ results: [{ name: 'newer' }], total: 1 });
		await screen.findByText('newer');
		first.resolve({ results: [{ name: 'stale' }], total: 1 });
		await sleep(100);

		expect(screen.getByText('newer')).toBeTruthy();
		expect(screen.queryByText('stale')).toBeNull();
	});
});
