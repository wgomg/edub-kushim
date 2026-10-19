import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import ModelsSection from './ModelsSection.svelte';

const { catalog, removeProvider, confirm, toastSuccess, toastError, onChanged } = vi.hoisted(() => ({
	catalog: vi.fn(),
	removeProvider: vi.fn(),
	confirm: vi.fn(),
	toastSuccess: vi.fn(),
	toastError: vi.fn(),
	onChanged: vi.fn()
}));

vi.mock('$lib/api', () => ({
	api: { llm: { catalog, removeProvider } }
}));
vi.mock('$lib/stores/confirmStore.svelte.js', () => ({ confirmStore: { confirm } }));
vi.mock('$lib/stores/toastStore.svelte.js', () => ({
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

describe('ModelsSection', () => {
	beforeEach(() => {
		vi.clearAllMocks();
		confirm.mockResolvedValue(true);
	});

	it('renders cataloged providers with their models and capability summary', async () => {
		catalog.mockResolvedValue({
			ok: true,
			status: 200,
			data: { providers: [groq], models: [scout] }
		});

		render(ModelsSection, { props: { onChanged } });

		expect(await screen.findByText('groq')).toBeTruthy();
		expect(await screen.findByText('llama-4-scout')).toBeTruthy();
		expect(await screen.findByText(/temperature/)).toBeTruthy();
		expect(screen.getByText('https://api.groq.com/openai/v1')).toBeTruthy();
	});

	it('surfaces the load error instead of pretending the catalog is empty', async () => {
		catalog.mockResolvedValue({
			ok: false,
			status: 503,
			data: { error: 'the model catalog failed to load; restore the default catalogs to recover' }
		});

		render(ModelsSection, { props: { onChanged } });

		expect(await screen.findByText(/failed to load/)).toBeTruthy();
		expect(screen.queryByText('No providers cataloged.')).toBeNull();
	});

	it('confirms, calls the API and adopts the returned catalog when removing a provider', async () => {
		catalog.mockResolvedValue({
			ok: true,
			status: 200,
			data: { providers: [groq], models: [] }
		});
		removeProvider.mockResolvedValue({
			ok: true,
			status: 200,
			data: { providers: [], models: [] }
		});

		render(ModelsSection, { props: { onChanged } });

		const remove = await screen.findByRole('button', { name: 'Remove' });
		await fireEvent.click(remove);

		await waitFor(() => expect(confirm).toHaveBeenCalledTimes(1));
		await waitFor(() => expect(removeProvider).toHaveBeenCalledWith('groq', false));
		await waitFor(() => expect(onChanged).toHaveBeenCalledTimes(1));
		await waitFor(() => expect(screen.queryByText('groq')).toBeNull());
	});
});
