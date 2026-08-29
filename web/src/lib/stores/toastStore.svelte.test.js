import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { toastStore } from './toastStore.svelte.js';

describe('toastStore', () => {
	beforeEach(() => {
		for (const t of toastStore.toasts) toastStore.dismiss(t.id);
	});

	afterEach(() => {
		vi.useRealTimers();
	});

	it('push appends a toast with an id and variant', () => {
		toastStore.push({ variant: 'info', message: 'hello' });
		expect(toastStore.toasts).toHaveLength(1);
		expect(toastStore.toasts[0]).toMatchObject({ variant: 'info', message: 'hello' });
		expect(toastStore.toasts[0].id).toBeGreaterThan(0);
	});

	it('caps the queue at three toasts, dropping the oldest', () => {
		for (let i = 1; i <= 4; i++) toastStore.push({ variant: 'info', message: `m${i}` });
		expect(toastStore.toasts).toHaveLength(3);
		expect(toastStore.toasts.map((t) => t.message)).toEqual(['m2', 'm3', 'm4']);
	});

	it('dismiss removes a toast by id', () => {
		toastStore.push({ variant: 'info', message: 'x' });
		const id = toastStore.toasts[0].id;
		toastStore.dismiss(id);
		expect(toastStore.toasts).toHaveLength(0);
	});

	it('auto-dismisses per variant timeout', () => {
		vi.useFakeTimers();
		toastStore.error('boom');
		toastStore.success('ok');
		expect(toastStore.toasts).toHaveLength(2);

		vi.advanceTimersByTime(4000);
		expect(toastStore.toasts.map((t) => t.message)).toEqual(['boom']);

		vi.advanceTimersByTime(2000);
		expect(toastStore.toasts).toHaveLength(0);
	});

	it('variant helpers push with the matching variant', () => {
		toastStore.error('e');
		toastStore.success('s');
		toastStore.warning('w');
		toastStore.info('i');
		expect(toastStore.toasts.map((t) => [t.variant, t.message])).toEqual([
			['success', 's'],
			['warning', 'w'],
			['info', 'i']
		]);
	});
});
