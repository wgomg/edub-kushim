import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import EnrichmentSection from '#lib/components/settings/EnrichmentSection.svelte';
import ModelsPage from './models/+page.svelte';
import { settingsStore } from '#lib/stores/settingsStore.svelte.js';

const apiMocks = vi.hoisted(() => ({
	configGet: vi.fn(),
	configUpdate: vi.fn(),
	configStatus: vi.fn(),
	configLlmModels: vi.fn(),
	catalog: vi.fn()
}));

vi.mock('$app/state', () => ({
	page: {
		url: new URL('http://localhost/settings/models'),
		state: {}
	}
}));

vi.mock('$app/navigation', () => ({ goto: vi.fn() }));

vi.mock('#lib/api.js', () => ({
	api: {
		config: {
			get: apiMocks.configGet,
			update: apiMocks.configUpdate,
			status: apiMocks.configStatus,
			llmModels: apiMocks.configLlmModels
		},
		llm: {
			catalog: apiMocks.catalog
		}
	}
}));

const fullCfg = () => ({
	server: {
		host: '0.0.0.0',
		port: 3000,
		max_upload_size: 100,
		max_download_files: 50,
		max_download_size_mb: 1024,
		max_concurrent_batches: 4,
		max_batch_delete: 10,
		auth_enabled: false
	},
	consumer: {
		workers: 2,
		max_files_per_batch: 100,
		supported_files: ['.pdf'],
		ocr: {
			engine: 'tesseract',
			languages: ['eng'],
			data_dir: '/tmp',
			timeout: 30,
			ocr_workers: 1
		},
		converter: { enabled: false, binary: 'soffice', timeout: 60 },
		polling: { enabled: true, interval: 60, windows: [] },
		reclaim: { enabled: true, max_retries: 3, stale_task_after: 600 },
		thumbnail: {
			enabled: true,
			engine: 'mupdf',
			dpi: 72,
			max_width: 400,
			quality: 80,
			timeout: 30,
			workers: 1,
			backfill_interval: 0,
			backfill_time: '02:00'
		},
		pdfoptimizer: { engine: 'qpdf', fallback: '', timeout: 60 },
		textextractor: { engine: 'pdftotext', timeout: 30 }
	},
	enricher: {
		workers: 2,
		textreducer: { engine: 'textrank', timeout: 30, target_words: 500 },
		contentanalyzer: {
			enabled: false,
			timeout: 60,
			prompt_template: '',
			pause_on_credit_error: false,
			doc_type_refinement: { enabled: false, head_words: 200, tail_words: 100 },
			llm: { adapter: '', provider: '', model: '', token: '', temperature: 0, request_delay: 0 },
			fallbacks: []
		},
		tagmatcher: {
			timeout: 30,
			reduce_target_words: 200,
			chunk_size: 0,
			hugot: { model: 'minishlab/potion-base-8M', backend: 'GO' }
		},
		tag_adjudicator: {
			enabled: false,
			timeout: 30,
			llm: { adapter: '', provider: '', model: '', token: '', request_delay: 0 }
		},
		tag_quality: {
			enabled: false,
			timeout: 0,
			llm: { adapter: '', provider: '', model: '', token: '', temperature: 0, request_delay: 0 }
		},
		tag_reconcile: {
			enabled: true,
			report_only: true,
			interval: 7,
			time: '03:30',
			max_rejudge: 250,
			max_retag: 2000,
			dead_tag_grace_days: 14
		}
	},
	storage: {
		consumption_dir: '/consumption',
		storage_dir: '/storage',
		migration_mode: 'copy',
		trash: { retention_days: 30 }
	},
	database: {
		host: 'localhost',
		port: 5432,
		user: 'edub',
		database: 'edub',
		sslmode: 'disable',
		runtime: 'host',
		container: ''
	},
	backup: { enabled: false, path: '', schedules: [] },
	mirror: { enabled: false, path: '', interval: 1, time: '02:00' },
	app: {
		log_level: 'info',
		logging: { max_size: 100, max_backups: 7, max_age: 30, compress: true }
	},
	available_file_types: [
		{ mime_type: 'application/pdf', extensions: ['.pdf'], required: true, label: 'PDF' }
	]
});

async function loadSection(cfg) {
	settingsStore.reset();
	apiMocks.configGet.mockResolvedValue(cfg);
	await settingsStore.load();
	return render(EnrichmentSection, {
		props: {
			cfg: settingsStore.cfg,
			llmModels: settingsStore.llmModels,
			toolStatus: [],
			statusKnown: true
		}
	});
}

beforeEach(() => {
	vi.clearAllMocks();
	settingsStore.reset();
	apiMocks.configStatus.mockResolvedValue({ tools: [] });
	apiMocks.configLlmModels.mockResolvedValue({ adapters: {}, providers: {} });
	apiMocks.catalog.mockResolvedValue({
		ok: true,
		status: 200,
		data: { providers: [], models: [] }
	});
});

describe('Enrichment settings', () => {
	it('renders the adjudicator card with the toggle and LLM fields', async () => {
		const cfg = fullCfg();
		cfg.enricher.tag_adjudicator.enabled = true;
		await loadSection(cfg);

		expect(await screen.findByRole('heading', { name: 'Tag adjudicator' })).toBeTruthy();

		const toggle = screen.getByRole('checkbox', { name: 'Enable tag adjudicator' });
		expect(toggle.checked).toBe(true);

		const timeout = document.getElementById('tag-adjudicator-timeout');
		expect(Number(timeout.value)).toBe(30);

		expect(document.getElementById('tag-adjudicator-adapter')).toBeTruthy();
		expect(document.getElementById('tag-adjudicator-provider')).toBeTruthy();
		expect(document.getElementById('tag-adjudicator-model')).toBeTruthy();
		expect(screen.getAllByRole('option', { name: 'Inherit from content analyzer' })).toHaveLength(
			3
		);
		expect(document.getElementById('tag-adjudicator-token')).toBeTruthy();
		expect(document.getElementById('tag-adjudicator-request-delay')).toBeTruthy();
	});

	it('renders the reconciliation card with the bound inputs', async () => {
		await loadSection(fullCfg());

		expect(await screen.findByRole('heading', { name: 'Tag reconciliation' })).toBeTruthy();

		expect(document.getElementById('tag-reconcile-enabled').checked).toBe(true);
		expect(document.getElementById('tag-reconcile-report-only').checked).toBe(true);
		expect(Number(document.getElementById('tag-reconcile-interval').value)).toBe(7);
		expect(document.getElementById('tag-reconcile-time').value).toBe('03:30');
		expect(Number(document.getElementById('tag-reconcile-max-rejudge').value)).toBe(250);
		expect(Number(document.getElementById('tag-reconcile-max-retag').value)).toBe(2000);
		expect(Number(document.getElementById('tag-reconcile-dead-tag-grace').value)).toBe(14);
	});

	it('fills the reconciliation defaults when the block is missing', async () => {
		const cfg = fullCfg();
		delete cfg.enricher.tag_reconcile;
		await loadSection(cfg);

		await screen.findByRole('heading', { name: 'Tag reconciliation' });

		expect(Number(document.getElementById('tag-reconcile-interval').value)).toBe(7);
		expect(document.getElementById('tag-reconcile-time').value).toBe('02:00');
		expect(Number(document.getElementById('tag-reconcile-max-rejudge').value)).toBe(200);
		expect(Number(document.getElementById('tag-reconcile-dead-tag-grace').value)).toBe(7);
	});

	it('fills the adjudicator defaults when the block is missing', async () => {
		const cfg = fullCfg();
		delete cfg.enricher.tag_adjudicator;
		await loadSection(cfg);

		await screen.findByRole('heading', { name: 'Tag adjudicator' });

		const toggle = screen.getByRole('checkbox', { name: 'Enable tag adjudicator' });
		expect(toggle.checked).toBe(false);
		await fireEvent.click(toggle);
		expect(Number(document.getElementById('tag-adjudicator-timeout').value)).toBe(0);
	});

	it('fills the quality defaults when the block is missing', async () => {
		const cfg = fullCfg();
		delete cfg.enricher.tag_quality;
		await loadSection(cfg);

		await screen.findByRole('heading', { name: 'Tag reconciliation' });

		const toggle = screen.getByRole('checkbox', { name: 'Enable tag quality judge' });
		expect(toggle.checked).toBe(false);
		await fireEvent.click(toggle);

		expect(Number(document.getElementById('tag-quality-timeout').value)).toBe(0);
		expect(document.getElementById('tag-quality-t-low')).toBeNull();
		expect(document.getElementById('tag-quality-no-anchor-similarity')).toBeNull();
	});

	it('fills the measurement defaults when the block is missing', async () => {
		const cfg = fullCfg();
		delete cfg.enricher.tag_measure;
		await loadSection(cfg);

		await screen.findByRole('heading', { name: 'Tag measurement' });

		expect(screen.getByRole('checkbox', { name: 'Schedule measurement runs' }).checked).toBe(false);
		expect(Number(document.getElementById('tag-measure-interval').value)).toBe(7);
		expect(document.getElementById('tag-measure-time').value).toBe('03:00');
	});

	it('carries the enricher block keys when applying', async () => {
		const cfg = fullCfg();
		apiMocks.configUpdate.mockResolvedValue({ ok: true, status: 200, data: {} });
		await loadSection(cfg);

		settingsStore.cfg.enricher.tag_reconcile.time = '04:15';
		await settingsStore.applyAll();

		await waitFor(() => expect(apiMocks.configUpdate).toHaveBeenCalledTimes(1));

		const body = apiMocks.configUpdate.mock.calls[0][0];
		expect(body['enricher.tag_adjudicator.enabled']).toBe(false);
		expect(body['enricher.tag_adjudicator.timeout']).toBe(30);
		expect(body['enricher.tag_adjudicator.llm.adapter']).toBe('');
		expect(body['enricher.tag_adjudicator.llm.provider']).toBe('');
		expect(body['enricher.tag_adjudicator.llm.model']).toBe('');
		expect(body['enricher.tag_adjudicator.llm.token']).toBe('');
		expect(body['enricher.tag_adjudicator.llm.request_delay']).toBe(0);
		expect(body['enricher.tag_reconcile.enabled']).toBe(true);
		expect(body['enricher.tag_reconcile.report_only']).toBe(true);
		expect(body['enricher.tag_reconcile.interval']).toBe(7);
		expect(body['enricher.tag_reconcile.time']).toBe('04:15');
		expect(body['enricher.tag_reconcile.max_rejudge']).toBe(250);
		expect(body['enricher.tag_reconcile.max_retag']).toBe(2000);
		expect(body['enricher.tag_reconcile.dead_tag_grace_days']).toBe(14);
		expect(body['enricher.tag_quality.enabled']).toBe(false);
		expect(body['enricher.tag_quality.timeout']).toBe(0);
		expect(body['enricher.tag_quality.llm.adapter']).toBe('');
		expect(body['enricher.tag_quality.llm.provider']).toBe('');
		expect(body['enricher.tag_quality.llm.model']).toBe('');
		expect(body['enricher.tag_quality.llm.token']).toBe('');
		expect(body['enricher.tag_quality.llm.request_delay']).toBe(0);
		expect(body['enricher.tag_measure.enabled']).toBe(false);
		expect(body['enricher.tag_measure.interval']).toBe(7);
		expect(body['enricher.tag_measure.time']).toBe('03:00');
	});

	it('shows the adjudicator dependency hint when enabled without the adjudicator', async () => {
		const cfg = fullCfg();
		cfg.enricher.tag_reconcile.enabled = true;
		cfg.enricher.tag_adjudicator.enabled = false;
		await loadSection(cfg);

		await screen.findByRole('heading', { name: 'Tag reconciliation' });
		expect(await screen.findByText(/Tag adjudicator not enabled/)).toBeTruthy();
		expect(await screen.findByText(/enable it on the Tag adjudicator card/)).toBeTruthy();
	});

	it('hides the adjudicator hint when the adjudicator is enabled', async () => {
		const cfg = fullCfg();
		cfg.enricher.tag_reconcile.enabled = true;
		cfg.enricher.tag_adjudicator.enabled = true;
		await loadSection(cfg);

		await screen.findByRole('heading', { name: 'Tag reconciliation' });
		await new Promise((resolve) => setTimeout(resolve, 0));
		expect(screen.queryByText(/Tag adjudicator not enabled/)).toBeNull();
	});
});

describe('AI providers & models page', () => {
	it('reports disabled fallbacks and inherited provider references as in use', async () => {
		const cfg = fullCfg();
		cfg.enricher.tag_adjudicator.llm.provider = 'groq';
		cfg.enricher.tag_adjudicator.llm.model = '';
		cfg.enricher.contentanalyzer.fallbacks = [
			{
				enabled: false,
				llm: {
					adapter: 'openai-compatible',
					provider: 'groq',
					model: 'llama-4-scout',
					token: '',
					temperature: 0,
					request_delay: 0
				}
			}
		];
		apiMocks.configGet.mockResolvedValue(cfg);
		apiMocks.catalog.mockResolvedValue({
			ok: true,
			status: 200,
			data: {
				providers: [
					{
						provider: 'groq',
						adapter: 'openai-compatible',
						official_url: 'https://api.groq.com/openai/v1',
						session_header: ''
					}
				],
				models: [
					{
						provider: 'groq',
						model_id: 'llama-4-scout',
						display_name: 'Llama 4 Scout',
						max_input_tokens: 131072,
						max_output_tokens: 32768
					}
				]
			}
		});
		await settingsStore.load();

		render(ModelsPage);

		expect(await screen.findByText('Fallback 1')).toBeTruthy();
		expect(await screen.findByText(/provider used by Adjudicator/)).toBeTruthy();
	});
});
