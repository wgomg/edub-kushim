import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import ModelsSection from './ModelsSection.svelte';

const { confirm, toastSuccess, toastError, goto } = vi.hoisted(() => ({
	confirm: vi.fn(),
	toastSuccess: vi.fn(),
	toastError: vi.fn(),
	goto: vi.fn()
}));

vi.mock('$app/state', () => ({
	page: {
		url: new URL('http://localhost/settings/models'),
		state: {}
	}
}));

vi.mock('$app/navigation', () => ({ goto }));

vi.mock('#lib/stores/confirmStore.svelte.js', () => ({ confirmStore: { confirm } }));
vi.mock('#lib/stores/toastStore.svelte.js', () => ({
	toastStore: { success: toastSuccess, error: toastError }
}));

const groq = {
	provider: 'groq',
	adapter: 'openai-compatible',
	official_url: 'https://api.groq.com/openai/v1',
	session_header: ''
};

const scout = {
	provider: 'groq',
	model_id: 'llama-4-scout',
	display_name: 'Llama 4 Scout',
	max_input_tokens: 131072,
	max_output_tokens: 32768,
	disable_reasoning: false,
	reasoning_efforts: [],
	supports_structured_output: true,
	supports_temperature: true,
	supports_prompt_caching: false,
	uses_max_completion_tokens: false,
	uses_responses_endpoint: false
};

function renderSection(overrides = {}) {
	return render(ModelsSection, {
		props: {
			catalog: { providers: [groq], models: [scout] },
			loaded: true,
			usedBy: { 'groq/llama-4-scout': ['Analyzer'] },
			...overrides
		}
	});
}

beforeEach(() => {
	vi.clearAllMocks();
	confirm.mockResolvedValue(true);
});

describe('ModelsSection', () => {
	it('renders cataloged providers with their models and capability summary', async () => {
		renderSection();

		expect((await screen.findAllByText('groq')).length).toBeGreaterThan(0);
		expect(await screen.findByText('llama-4-scout')).toBeTruthy();
		expect(await screen.findByText('JSON')).toBeTruthy();
		expect(screen.getByText('https://api.groq.com/openai/v1')).toBeTruthy();
		expect(screen.getByText('Analyzer')).toBeTruthy();
	});

	it('surfaces the load error instead of pretending the catalog is empty', async () => {
		renderSection({
			catalog: { providers: [], models: [] },
			error: 'the model catalog failed to load; restore the default catalogs to recover'
		});

		expect(await screen.findByText(/failed to load/)).toBeTruthy();
		expect(screen.queryByText('No providers cataloged.')).toBeNull();
	});

	it('confirms and stages a provider removal through its callback', async () => {
		const onRemoveProvider = vi.fn();
		renderSection({ onRemoveProvider });

		const [remove] = await screen.findAllByRole('button', { name: 'Remove' });
		await fireEvent.click(remove);

		await waitFor(() => expect(confirm).toHaveBeenCalledTimes(1));
		await waitFor(() => expect(onRemoveProvider).toHaveBeenCalledWith('groq', true));
		expect(toastSuccess).toHaveBeenCalled();
	});

	it('uses the optional token for autofill without staging it in the catalog', async () => {
		const onAutofill = vi.fn().mockResolvedValue({
			ok: true,
			status: 200,
			data: {
				provider: { official_url: 'https://filled.test/v1', adapter: 'openai-compatible' },
				models: [],
				filled: []
			}
		});
		const onUpdateProvider = vi.fn();
		renderSection({ onAutofill, onUpdateProvider });

		const [edit] = await screen.findAllByRole('button', { name: 'Edit' });
		await fireEvent.click(edit);
		await fireEvent.input(await screen.findByLabelText(/API token/), {
			target: { value: 'secret-token' }
		});
		const [autofill] = await screen.findAllByRole('button', { name: 'Autofill' });
		await fireEvent.click(autofill);

		await waitFor(() =>
			expect(onAutofill).toHaveBeenCalledWith(
				expect.objectContaining({ provider: 'groq', token: 'secret-token' })
			)
		);
		await fireEvent.click(screen.getByRole('button', { name: 'Save Provider' }));
		await waitFor(() => expect(onUpdateProvider).toHaveBeenCalledTimes(1));
		expect(JSON.stringify(onUpdateProvider.mock.calls[0])).not.toContain('secret-token');
	});

	it('clears the reasoning request fields when the user empties them', async () => {
		const onUpdateModel = vi.fn();
		const withKnob = {
			...scout,
			reasoning_toggle: { field: 'thinking.type', on: 'enabled', off: 'disabled' },
			reasoning_effort_field: 'reasoning_effort'
		};
		renderSection({
			catalog: { providers: [groq], models: [withKnob] },
			usedBy: {},
			onUpdateModel
		});

		const edits = await screen.findAllByRole('button', { name: 'Edit' });
		await fireEvent.click(edits[1]);
		await fireEvent.input(await screen.findByLabelText('Toggle field'), { target: { value: '' } });
		await fireEvent.input(screen.getByLabelText('Effort field'), { target: { value: '' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Save Model' }));

		await waitFor(() => expect(onUpdateModel).toHaveBeenCalledTimes(1));
		const payload = onUpdateModel.mock.calls[0][2];
		expect(payload.reasoning_toggle).toBeNull();
		expect(payload.reasoning_effort_field).toBe('');
	});
});
