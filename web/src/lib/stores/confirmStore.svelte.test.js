import { describe, it, expect, beforeEach } from 'vitest';
import { confirmStore } from './confirmStore.svelte.js';

describe('confirmStore', () => {
	beforeEach(() => {
		confirmStore.resolve(false);
	});

	it('confirm exposes the pending dialog state', () => {
		const promise = confirmStore.confirm({ title: 'Delete', message: 'Sure?', danger: true });
		expect(confirmStore.pending).toMatchObject({ title: 'Delete', message: 'Sure?', danger: true });
		expect(promise).toBeInstanceOf(Promise);
	});

	it('resolve settles the pending promise and clears state', async () => {
		const promise = confirmStore.confirm({ title: 'Delete', message: 'Sure?' });
		confirmStore.resolve(true);
		await expect(promise).resolves.toBe(true);
		expect(confirmStore.pending).toBeNull();
	});

	it('resolve(false) rejects the action', async () => {
		const promise = confirmStore.confirm({ title: 'Delete', message: 'Sure?' });
		confirmStore.resolve(false);
		await expect(promise).resolves.toBe(false);
	});

	it('a second confirm settles the previous one as cancelled', async () => {
		const first = confirmStore.confirm({ title: 'A', message: 'first' });
		const second = confirmStore.confirm({ title: 'B', message: 'second' });
		await expect(first).resolves.toBe(false);
		expect(confirmStore.pending.title).toBe('B');
		confirmStore.resolve(true);
		await expect(second).resolves.toBe(true);
	});
});
