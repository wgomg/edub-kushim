import { describe, it, expect, vi, beforeEach } from 'vitest';
import { settingsStore } from './settingsStore.svelte.js';

const apiMocks = vi.hoisted(() => ({
	configGet: vi.fn(),
	configUpdate: vi.fn(),
	configStatus: vi.fn(),
	configLlmModels: vi.fn(),
	catalog: vi.fn(),
	addProvider: vi.fn(),
	reset: vi.fn()
}));

const toastMocks = vi.hoisted(() => ({
	success: vi.fn(),
	error: vi.fn(),
	info: vi.fn(),
	dismiss: vi.fn()
}));

vi.mock('$lib/api', () => ({
	api: {
		config: {
			get: apiMocks.configGet,
			update: apiMocks.configUpdate,
			status: apiMocks.configStatus,
			llmModels: apiMocks.configLlmModels
		},
		llm: {
			catalog: apiMocks.catalog,
			addProvider: apiMocks.addProvider,
			reset: apiMocks.reset
		}
	}
}));

vi.mock('$lib/stores/toastStore.svelte.js', () => ({ toastStore: toastMocks }));

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
		ocr: { engine: 'tesseract', languages: ['eng'], data_dir: '/tmp', timeout: 30, ocr_workers: 1 },
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
			dead_tag_grace_days: 14,
			provisional_grace_days: 30
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

const newProvider = {
	provider: 'newco',
	adapter: 'openai-compatible',
	official_url: 'https://newco.example/v1',
	session_header: ''
};

async function loadSettings(cfg) {
	apiMocks.configGet.mockResolvedValue(cfg);
	await settingsStore.load();
	return settingsStore.cfg;
}

beforeEach(() => {
	vi.clearAllMocks();
	settingsStore.reset();
	apiMocks.configUpdate.mockResolvedValue({ ok: true, status: 200, data: {} });
	apiMocks.configStatus.mockResolvedValue({ tools: [] });
	apiMocks.configLlmModels.mockResolvedValue({ adapters: {}, providers: {} });
	apiMocks.catalog.mockResolvedValue({
		ok: true,
		status: 200,
		data: { providers: [], models: [] }
	});
	apiMocks.addProvider.mockResolvedValue({ ok: true, status: 200, data: {} });
	apiMocks.reset.mockResolvedValue({ ok: true, status: 200, data: {} });
});

describe('settingsStore staged apply', () => {
	it('saves configuration before replaying catalog operations', async () => {
		const order = [];
		apiMocks.configUpdate.mockImplementation(async () => {
			order.push('config');
			return { ok: true, status: 200, data: {} };
		});
		apiMocks.addProvider.mockImplementation(async () => {
			order.push('provider');
			return { ok: true, status: 200, data: {} };
		});
		await loadSettings(fullCfg());

		settingsStore.cfg.server.port = 4000;
		settingsStore.addProvider(newProvider);
		await settingsStore.applyAll();

		expect(order).toEqual(['config', 'provider']);
		expect(apiMocks.configUpdate.mock.calls[0][0]).toMatchObject({ 'server.port': 4000 });
		expect(apiMocks.addProvider).toHaveBeenCalledTimes(1);
		expect(settingsStore.catalogOps).toHaveLength(0);
		expect(settingsStore.pendingCount).toBe(0);
		expect(toastMocks.success).toHaveBeenCalledWith('All changes applied.');
	});

	it('keeps a failed catalog operation staged and surfaces the server reason', async () => {
		await loadSettings(fullCfg());
		apiMocks.catalog.mockClear();
		apiMocks.addProvider.mockResolvedValue({
			ok: false,
			status: 409,
			data: { error: 'in use by the enricher LLM configuration' }
		});

		settingsStore.addProvider(newProvider);
		await settingsStore.applyAll();

		expect(apiMocks.configUpdate).not.toHaveBeenCalled();
		expect(settingsStore.catalogOps).toHaveLength(1);
		expect(settingsStore.pendingCount).toBe(1);
		expect(apiMocks.catalog).toHaveBeenCalled();
		expect(toastMocks.error).toHaveBeenCalledWith(
			expect.stringContaining('in use by the enricher LLM configuration')
		);
	});

	it('blocks a catalog reset while an apply is in flight', async () => {
		const cfg = fullCfg();
		await loadSettings(cfg);
		let resolveUpdate;
		apiMocks.configUpdate.mockReturnValue(
			new Promise((resolve) => {
				resolveUpdate = resolve;
			})
		);

		settingsStore.cfg.server.port = 4001;
		const applyPromise = settingsStore.applyAll();
		await vi.waitFor(() => expect(apiMocks.configUpdate).toHaveBeenCalled());

		expect(await settingsStore.resetCatalog()).toBe(false);
		expect(apiMocks.reset).not.toHaveBeenCalled();
		expect(toastMocks.error).toHaveBeenCalledWith(
			'Wait for the current operation to finish before restoring catalogs.'
		);

		resolveUpdate({ ok: true, status: 200, data: {} });
		await applyPromise;

		expect(settingsStore.busy).toBe(false);
		expect(await settingsStore.resetCatalog()).toBe(true);
		expect(apiMocks.reset).toHaveBeenCalledTimes(1);
	});
});

describe('settingsStore staged changes', () => {
	it('discarding a change to an omitted key clears it completely', async () => {
		const cfg = fullCfg();
		delete cfg.enricher.contentanalyzer.prompt_template;
		await loadSettings(cfg);

		settingsStore.cfg.enricher.contentanalyzer.prompt_template = 'draft prompt';
		expect(settingsStore.pendingCount).toBe(1);

		settingsStore.discardConfigChange('enricher.contentanalyzer.prompt_template');

		expect(settingsStore.pendingCount).toBe(0);
	});

	it('redacts token values and labels changes with the full config key', async () => {
		await loadSettings(fullCfg());

		settingsStore.cfg.enricher.contentanalyzer.llm.token = 'sk-live-secret';
		const changes = settingsStore.configChanges;
		const tokenChange = changes.find(
			(change) => change.key === 'enricher.contentanalyzer.llm.token'
		);

		expect(tokenChange).toMatchObject({
			label: 'enricher.contentanalyzer.llm.token',
			secret: true,
			from: null,
			to: null
		});
		expect(JSON.stringify(changes)).not.toContain('sk-live-secret');
	});
});

describe('settingsStore model list refresh', () => {
	it('keeps the last model list when a refresh fails', async () => {
		const seed = { adapters: { openai: [] }, providers: { openai: [] } };
		apiMocks.configLlmModels.mockResolvedValueOnce(seed).mockResolvedValueOnce(null);
		await loadSettings(fullCfg());

		settingsStore.addProvider(newProvider);
		await settingsStore.applyAll();

		expect(settingsStore.llmModels).toEqual(seed);
	});
});

describe('settingsStore tag matcher and audit keys', () => {
	it('heals a missing top_n to the built-in default', async () => {
		await loadSettings(fullCfg());

		expect(settingsStore.cfg.enricher.tagmatcher.top_n).toBe(15);
	});

	it('saves top_n and tag_audit sample_size and never max_tags', async () => {
		await loadSettings(fullCfg());

		settingsStore.cfg.enricher.tagmatcher.top_n = 42;
		settingsStore.cfg.enricher.tag_audit.sample_size = 75;
		await settingsStore.applyAll();

		const body = apiMocks.configUpdate.mock.calls[0][0];
		expect(body['enricher.tagmatcher.top_n']).toBe(42);
		expect(body['enricher.tag_audit.sample_size']).toBe(75);
		expect(body).not.toHaveProperty('enricher.contentanalyzer.max_tags');
	});
});
