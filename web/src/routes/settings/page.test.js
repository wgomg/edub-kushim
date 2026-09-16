import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import Page from './+page.svelte';

const apiMocks = vi.hoisted(() => ({
	configGet: vi.fn(),
	configUpdate: vi.fn(),
	configStatus: vi.fn(),
	configLlmModels: vi.fn()
}));

vi.mock('$lib/api', () => ({
	api: {
		config: {
			get: apiMocks.configGet,
			update: apiMocks.configUpdate,
			status: apiMocks.configStatus,
			llmModels: apiMocks.configLlmModels
		},
		users: { list: vi.fn().mockResolvedValue({ users: [], total: 0 }) }
	}
}));

vi.mock('$lib/stores/authStore.js', () => ({
	authEnabled: () => false,
	isAdmin: () => true,
	roleBadgeClass: () => 'bg-gold-500/20 text-gold-500'
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
	],
	available_engines: {
		ocr: [{ value: 'tesseract', label: 'Tesseract' }],
		text_extractor: [{ value: 'pdftotext', label: 'pdftotext' }],
		pdf_optimizer: [{ value: 'qpdf', label: 'qpdf' }],
		text_reducer: [{ value: 'textrank', label: 'TextRank' }],
		thumbnail: [{ value: 'mupdf', label: 'MuPDF' }]
	}
});

beforeEach(() => {
	apiMocks.configGet.mockReset();
	apiMocks.configUpdate.mockReset();
	apiMocks.configStatus.mockReset();
	apiMocks.configLlmModels.mockReset();
	apiMocks.configStatus.mockResolvedValue({ missing_tools: [], tools: [] });
	apiMocks.configLlmModels.mockResolvedValue({ adapters: {}, providers: {} });
});

describe('Settings — Tag reconciliation section', () => {
	it('renders the adjudicator card with the toggle and LLM fields', async () => {
		const cfg = fullCfg();
		cfg.enricher.tag_adjudicator.enabled = true;
		apiMocks.configGet.mockResolvedValue(cfg);
		render(Page);

		expect(await screen.findByRole('heading', { name: 'Tag adjudicator' })).toBeTruthy();

		const toggle = screen.getByRole('checkbox', { name: 'Enable tag adjudicator' });
		expect(toggle.checked).toBe(true);

		const timeout = document.getElementById('tag-adjudicator-timeout');
		expect(Number(timeout.value)).toBe(30);

		const adapter = document.getElementById('tag-adjudicator-adapter');
		expect(adapter).toBeTruthy();
		const provider = document.getElementById('tag-adjudicator-provider');
		expect(provider).toBeTruthy();
		const model = document.getElementById('tag-adjudicator-model');
		expect(model).toBeTruthy();
		// Each select must offer the inherit-from-analyzer escape hatch so the
		// adjudicator can be enabled without pinning an explicit LLM.
		expect(screen.getAllByRole('option', { name: 'Inherit from content analyzer' })).toHaveLength(
			3
		);
		const token = document.getElementById('tag-adjudicator-token');
		expect(token).toBeTruthy();
		const requestDelay = document.getElementById('tag-adjudicator-request-delay');
		expect(requestDelay).toBeTruthy();
	});

	it('renders the card with the seven bound inputs', async () => {
		apiMocks.configGet.mockResolvedValue(fullCfg());
		render(Page);

		expect(await screen.findByRole('heading', { name: 'Tag reconciliation' })).toBeTruthy();

		const enabled = document.getElementById('tag-reconcile-enabled');
		expect(enabled.checked).toBe(true);

		const reportOnly = document.getElementById('tag-reconcile-report-only');
		expect(reportOnly.checked).toBe(true);

		const interval = document.getElementById('tag-reconcile-interval');
		expect(interval).toBeTruthy();
		expect(Number(interval.value)).toBe(7);

		const time = document.getElementById('tag-reconcile-time');
		expect(time.value).toBe('03:30');

		const maxRejudge = document.getElementById('tag-reconcile-max-rejudge');
		expect(Number(maxRejudge.value)).toBe(250);

		const maxRetag = document.getElementById('tag-reconcile-max-retag');
		expect(Number(maxRetag.value)).toBe(2000);

		const deadTagGrace = document.getElementById('tag-reconcile-dead-tag-grace');
		expect(Number(deadTagGrace.value)).toBe(14);
	});

	it('ensureTagReconcileBlock fills the default block when missing', async () => {
		const cfg = fullCfg();
		delete cfg.enricher.tag_reconcile;
		apiMocks.configGet.mockResolvedValue(cfg);
		apiMocks.configUpdate.mockResolvedValue({ message: 'Settings saved.' });

		render(Page);

		await screen.findByRole('heading', { name: 'Tag reconciliation' });

		const interval = document.getElementById('tag-reconcile-interval');
		expect(Number(interval.value)).toBe(7);
		const time = document.getElementById('tag-reconcile-time');
		expect(time.value).toBe('02:00');
		const maxRejudge = document.getElementById('tag-reconcile-max-rejudge');
		expect(Number(maxRejudge.value)).toBe(200);
		const deadTagGrace = document.getElementById('tag-reconcile-dead-tag-grace');
		expect(Number(deadTagGrace.value)).toBe(7);
	});

	it('ensureTagAdjudicatorBlock fills the default block when missing', async () => {
		const cfg = fullCfg();
		delete cfg.enricher.tag_adjudicator;
		apiMocks.configGet.mockResolvedValue(cfg);

		render(Page);

		await screen.findByRole('heading', { name: 'Tag adjudicator' });

		const toggle = screen.getByRole('checkbox', { name: 'Enable tag adjudicator' });
		expect(toggle.checked).toBe(false);
		// Body is gated on enabled — the timeout field renders once toggled on.
		fireEvent.click(toggle);
		const timeout = document.getElementById('tag-adjudicator-timeout');
		expect(Number(timeout.value)).toBe(0);
	});

	it('ensureTagQualityBlock fills the default block when missing', async () => {
		const cfg = fullCfg();
		delete cfg.enricher.tag_quality;
		apiMocks.configGet.mockResolvedValue(cfg);

		render(Page);

		await screen.findByRole('heading', { name: 'Tag reconciliation' });

		const toggle = screen.getByRole('checkbox', { name: 'Enable tag quality judge' });
		expect(toggle.checked).toBe(false);
		fireEvent.click(toggle);

		const timeout = document.getElementById('tag-quality-timeout');
		expect(Number(timeout.value)).toBe(0);
		// The reject-band thresholds are internal constants, not operator knobs.
		expect(document.getElementById('tag-quality-t-low')).toBeNull();
		expect(document.getElementById('tag-quality-no-anchor-similarity')).toBeNull();
	});

	it('bodyFromConfig carries the tag_reconcile, tag_adjudicator, and tag_quality keys on save', async () => {
		apiMocks.configGet.mockResolvedValue(fullCfg());
		apiMocks.configUpdate.mockResolvedValue({ message: 'Settings saved.' });

		render(Page);

		await screen.findByRole('heading', { name: 'Tag reconciliation' });

		fireEvent.click(screen.getByRole('button', { name: 'Save changes' }));

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
		expect(body['enricher.tag_reconcile.time']).toBe('03:30');
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
	});

	it('shows the adjudicator dependency hint when enabled without the adjudicator', async () => {
		const cfg = fullCfg();
		cfg.enricher.tag_reconcile.enabled = true;
		cfg.enricher.tag_adjudicator.enabled = false;
		apiMocks.configGet.mockResolvedValue(cfg);

		render(Page);

		await screen.findByRole('heading', { name: 'Tag reconciliation' });
		expect(await screen.findByText(/Tag adjudicator not enabled/)).toBeTruthy();
		expect(await screen.findByText(/enable it on the Tag adjudicator card/)).toBeTruthy();
	});

	it('hides the adjudicator hint when the adjudicator is enabled', async () => {
		const cfg = fullCfg();
		cfg.enricher.tag_reconcile.enabled = true;
		cfg.enricher.tag_adjudicator.enabled = true;
		apiMocks.configGet.mockResolvedValue(cfg);

		render(Page);

		await screen.findByRole('heading', { name: 'Tag reconciliation' });
		// Wait a tick for the conditional to resolve.
		await new Promise((r) => setTimeout(r, 0));
		expect(screen.queryByText(/Tag adjudicator not enabled/)).toBeNull();
	});
});
