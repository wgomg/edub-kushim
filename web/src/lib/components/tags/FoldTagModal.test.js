import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import FoldTagModal from './FoldTagModal.svelte';

const { autocompleteTags, foldTag, toastMock } = vi.hoisted(() => ({
	autocompleteTags: vi.fn(),
	foldTag: vi.fn(),
	toastMock: { success: vi.fn(), error: vi.fn() }
}));

vi.mock('#lib/api.js', () => ({
	api: {
		autocomplete: { tags: autocompleteTags },
		tags: { fold: foldTag }
	}
}));
vi.mock('#lib/stores/toastStore.svelte.js', () => ({ toastStore: toastMock }));

const loser = { id: 7, name: 'anarchist', document_count: 3 };

function openModal() {
	const onClose = vi.fn();
	const onfolded = vi.fn();
	render(FoldTagModal, { open: true, tag: loser, onClose, onfolded });
	return { onClose, onfolded };
}

beforeEach(() => {
	autocompleteTags.mockReset();
	foldTag.mockReset();
	toastMock.success.mockReset();
	toastMock.error.mockReset();
});

describe('FoldTagModal', () => {
	it('folds the picked target and reports the result', async () => {
		autocompleteTags.mockResolvedValue([
			{ name: 'anarchist', document_count: 3 },
			{ name: 'anarchism', document_count: 2 }
		]);
		foldTag.mockResolvedValue({ ok: true, status: 204 });
		const { onClose, onfolded } = openModal();

		const submit = screen.getByRole('button', { name: 'Fold' });
		expect(submit.disabled).toBe(true);

		await fireEvent.input(screen.getByLabelText('Target tag'), { target: { value: 'anarch' } });
		expect(await screen.findByRole('option', { name: 'anarchism' })).toBeTruthy();
		expect(screen.queryByRole('option', { name: 'anarchist' })).toBeNull();
		await fireEvent.mouseDown(screen.getByRole('option', { name: 'anarchism' }));

		expect(submit.disabled).toBe(false);
		await fireEvent.click(submit);

		await waitFor(() => expect(foldTag).toHaveBeenCalledWith('anarchist', 'anarchism'));
		expect(onfolded).toHaveBeenCalledTimes(1);
		expect(onClose).toHaveBeenCalledTimes(1);
		expect(toastMock.success).toHaveBeenCalledTimes(1);
	});

	it('surfaces the server refusal instead of closing', async () => {
		autocompleteTags.mockResolvedValue([{ name: 'anarchism', document_count: 2 }]);
		foldTag.mockResolvedValue({
			ok: false,
			status: 409,
			data: { error: 'Seeded values cannot be folded' }
		});
		const { onClose, onfolded } = openModal();

		await fireEvent.input(screen.getByLabelText('Target tag'), { target: { value: 'anarch' } });
		await fireEvent.mouseDown(await screen.findByRole('option', { name: 'anarchism' }));
		await fireEvent.click(screen.getByRole('button', { name: 'Fold' }));

		await waitFor(() => expect(foldTag).toHaveBeenCalledTimes(1));
		expect(await screen.findByText('Seeded values cannot be folded')).toBeTruthy();
		expect(onClose).not.toHaveBeenCalled();
		expect(onfolded).not.toHaveBeenCalled();
	});
});
