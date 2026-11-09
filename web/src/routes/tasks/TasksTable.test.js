import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/svelte';

vi.mock('$app/navigation', () => ({ goto: vi.fn() }));
vi.mock('$app/paths', () => ({ resolve: (path) => path }));
vi.mock('$app/state', () => ({ page: { url: new URL('http://localhost/tasks?batch=b-1') } }));

const apiMocks = vi.hoisted(() => ({
	tasksList: vi.fn(),
	batchesResume: vi.fn()
}));

vi.mock('#lib/api.js', () => ({
	api: {
		tasks: { list: apiMocks.tasksList },
		batches: { resume: apiMocks.batchesResume }
	}
}));

vi.mock('#lib/stores/authStore.svelte.js', () => ({
	authEnabled: () => true,
	isEditor: () => true
}));

import TasksTable from './TasksTable.svelte';

function task(overrides = {}) {
	return {
		task_id: 11,
		batch_id: 'b-1',
		task_type: 'consume',
		status: 'completed',
		created_at: '2026-01-01T00:00:00Z',
		started_at: '2026-01-01T00:00:05Z',
		completed_at: '2026-01-01T00:00:09Z',
		error: null,
		payload: null,
		...overrides
	};
}

beforeEach(() => {
	apiMocks.tasksList.mockReset();
	apiMocks.batchesResume.mockReset();
});

describe('TasksTable', () => {
	it('renders the tasks from the endpoint response', async () => {
		apiMocks.tasksList.mockResolvedValue({
			batch_id: 'b-1',
			tasks: [task(), task({ task_id: 12, task_type: 'enrich', status: 'failed', error: 'boom' })]
		});
		render(TasksTable);

		expect(await screen.findByText('consume')).toBeTruthy();
		expect(screen.getByText('enrich')).toBeTruthy();
		expect(screen.getByText('completed')).toBeTruthy();
		expect(screen.getByText('boom')).toBeTruthy();
		expect(screen.getByText('Batch: b-1')).toBeTruthy();
		expect(apiMocks.tasksList).toHaveBeenCalledWith('b-1', '', 25, 0, expect.anything());
	});

	it('shows the fetch failure instead of a table when the endpoint answers null', async () => {
		apiMocks.tasksList.mockResolvedValue(null);
		render(TasksTable);
		expect(await screen.findByText('Failed to load tasks')).toBeTruthy();
	});
});
