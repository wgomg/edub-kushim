import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import SplitTagModal from './SplitTagModal.svelte';

const { splitTag, toastMock } = vi.hoisted(() => ({
	splitTag: vi.fn(),
	toastMock: { success: vi.fn(), error: vi.fn() }
}));

vi.mock('#lib/api.js', () => ({
	api: { tags: { split: splitTag } }
}));
vi.mock('#lib/stores/toastStore.svelte.js', () => ({ toastStore: toastMock }));

const compound = { id: 7, name: 'machine learning', document_count: 3 };

function openModal(initialParts = undefined) {
	const onClose = vi.fn();
	const onsplit = vi.fn();
	const props = { open: true, tag: compound, onClose, onsplit };
	if (initialParts !== undefined) props.initialParts = initialParts;
	render(SplitTagModal, props);
	return { onClose, onsplit };
}

beforeEach(() => {
	splitTag.mockReset();
	toastMock.success.mockReset();
	toastMock.error.mockReset();
});

describe('SplitTagModal', () => {
	it('submits the prefilled components by name and reports the result', async () => {
		splitTag.mockResolvedValue({ ok: true, status: 204 });
		const { onClose, onsplit } = openModal(['judge ruled']);

		expect(screen.getByLabelText('Component 1').value).toBe('judge ruled');
		await fireEvent.click(screen.getByRole('button', { name: 'Split' }));

		await waitFor(() => expect(splitTag).toHaveBeenCalledWith('machine learning', ['judge ruled']));
		expect(onsplit).toHaveBeenCalledTimes(1);
		expect(onClose).toHaveBeenCalledTimes(1);
		expect(toastMock.success).toHaveBeenCalledTimes(1);
	});

	it('seeds one empty component when the caller passes none', () => {
		openModal();

		expect(screen.getByLabelText('Component 1').value).toBe('');
	});

	it('surfaces the server refusal instead of closing', async () => {
		splitTag.mockResolvedValue({
			ok: false,
			status: 409,
			data: { error: 'Seeded values cannot be split' }
		});
		const { onClose, onsplit } = openModal(['seeding']);

		await fireEvent.click(screen.getByRole('button', { name: 'Split' }));

		await waitFor(() => expect(splitTag).toHaveBeenCalledTimes(1));
		expect(await screen.findByText('Seeded values cannot be split')).toBeTruthy();
		expect(onClose).not.toHaveBeenCalled();
		expect(onsplit).not.toHaveBeenCalled();
	});

	it('requires at least one component', async () => {
		openModal();

		await fireEvent.click(screen.getByRole('button', { name: 'Split' }));

		expect(await screen.findByText('At least one component is required')).toBeTruthy();
		expect(splitTag).not.toHaveBeenCalled();
	});
});
