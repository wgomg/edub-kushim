import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import UploadModal from './UploadModal.svelte';

const apiMocks = vi.hoisted(() => ({
	supportedMimeTypes: vi.fn(),
	upload: vi.fn()
}));

vi.mock('$lib/api', () => ({
	api: {
		consume: { upload: apiMocks.upload },
		supportedMimeTypes: apiMocks.supportedMimeTypes
	}
}));
vi.mock('$app/paths', () => ({ resolve: (p) => p }));

function selectFile() {
	const input = document.querySelector('input[type="file"]');
	const file = new File(['%PDF-1.4'], 'a.pdf', { type: 'application/pdf' });
	fireEvent.change(input, { target: { files: [file] } });
	return file;
}

describe('UploadModal', () => {
	beforeEach(() => {
		apiMocks.supportedMimeTypes
			.mockReset()
			.mockResolvedValue([{ extension: '.pdf' }, { extension: '.docx' }]);
		apiMocks.upload.mockReset();
	});

	it('queues the selected file and shows the success state', async () => {
		apiMocks.upload.mockResolvedValue({
			ok: true,
			status: 202,
			data: { accepted: 1, batch_id: 'b-1', rejected: [] }
		});
		render(UploadModal, { props: { open: true, onClose: vi.fn() } });

		const file = selectFile();
		fireEvent.click(screen.getByRole('button', { name: 'Upload' }));

		expect(await screen.findByText('1 file(s) queued')).toBeTruthy();
		expect(apiMocks.upload).toHaveBeenCalledWith([file]);
		expect(screen.getByRole('link', { name: 'View tasks' })).toBeTruthy();
	});

	it('shows the server error message when the upload is rejected', async () => {
		apiMocks.upload.mockResolvedValue({ ok: false, status: 413, data: { error: 'Too big' } });
		render(UploadModal, { props: { open: true, onClose: vi.fn() } });

		selectFile();
		fireEvent.click(screen.getByRole('button', { name: 'Upload' }));

		expect(await screen.findByText('Too big')).toBeTruthy();
	});
});
