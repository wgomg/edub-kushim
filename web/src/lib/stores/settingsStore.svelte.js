import { api } from '$lib/api';
import { toastStore } from '$lib/stores/toastStore.svelte.js';

const ADVANCED_KEY = 'edub.settings.advanced';

let cfg = $state(null);
let snapshot = $state(null);
let snapshotJson = $state('');
let loading = $state(false);
let loaded = $state(false);
let loadError = $state('');
let saving = $state(false);
let resetting = $state(false);
let advanced = $state(false);

let toolStatus = $state([]);
let statusLoaded = $state(false);
let llmModels = $state({ adapters: {}, providers: {} });
let mimeTypeOptions = $state([]);

let catalog = $state({ providers: [], models: [] });
let catalogLoaded = $state(false);
let catalogError = $state('');
let catalogOps = $state([]);
let trayOpen = $state(false);

function openTray() {
	trayOpen = true;
}

function closeTray() {
	trayOpen = false;
}

function ensureAll() {
	ensureFallbacksArray();
	ensureThumbnailBlock();
	ensureMirrorBlock();
	ensureTagReconcileBlock();
	ensureTagMeasureBlock();
	ensureTagAuditBlock();
	ensureTagMaintenanceBlock();
	ensureTagAdjudicatorBlock();
	ensureTagQualityBlock();
	ensureContentAnalyzerTags();
	ensureTagMatcherTopN();
}

function ensureContentAnalyzerTags() {
	if (!cfg) return;
	const ca = cfg.enricher.contentanalyzer;
	if (!(ca.min_tags > 0)) ca.min_tags = 3;
}

function ensureTagMatcherTopN() {
	if (!cfg) return;
	if (!cfg.enricher.tagmatcher) cfg.enricher.tagmatcher = {};
	if (!(cfg.enricher.tagmatcher.top_n >= 3)) cfg.enricher.tagmatcher.top_n = 15;
}

function ensureFallbacksArray() {
	if (!cfg) return;
	const ca = cfg.enricher.contentanalyzer;
	if (!ca.fallbacks) ca.fallbacks = [];
}

function ensureThumbnailBlock() {
	if (!cfg) return;
	if (!cfg.consumer) cfg.consumer = {};
	if (cfg.consumer.thumbnail) return;
	cfg.consumer.thumbnail = {
		enabled: true,
		engine: 'mupdf',
		dpi: 72,
		max_width: 400,
		quality: 80,
		timeout: 30,
		workers: 1,
		backfill_interval: 0,
		backfill_time: '02:00'
	};
}

function ensureMirrorBlock() {
	if (!cfg) return;
	if (!cfg.mirror || !(cfg.mirror.interval > 0)) {
		cfg.mirror = {
			enabled: cfg.mirror?.enabled ?? false,
			path: cfg.mirror?.path ?? '',
			interval: 1,
			time: cfg.mirror?.time || '02:00'
		};
	}
}

function ensureTagReconcileBlock() {
	if (!cfg) return;
	if (!cfg.enricher) cfg.enricher = {};
	if (cfg.enricher.tag_reconcile) return;
	cfg.enricher.tag_reconcile = {
		enabled: false,
		report_only: true,
		interval: 7,
		time: '02:00',
		max_rejudge: 200,
		max_retag: 1000,
		dead_tag_grace_days: 7,
		provisional_grace_days: 30
	};
}

function ensureTagMeasureBlock() {
	if (!cfg) return;
	if (!cfg.enricher) cfg.enricher = {};
	if (cfg.enricher.tag_measure) return;
	cfg.enricher.tag_measure = { enabled: false, interval: 7, time: '03:00' };
}

function ensureTagAuditBlock() {
	if (!cfg) return;
	if (!cfg.enricher) cfg.enricher = {};
	if (cfg.enricher.tag_audit) return;
	cfg.enricher.tag_audit = { enabled: false, interval: 7, time: '04:00', sample_size: 150 };
}

function ensureTagMaintenanceBlock() {
	if (!cfg) return;
	if (!cfg.enricher) cfg.enricher = {};
	if (cfg.enricher.tag_maintenance) return;
	cfg.enricher.tag_maintenance = { enabled: false, interval: 7, time: '02:00' };
}

function ensureTagAdjudicatorBlock() {
	if (!cfg) return;
	if (!cfg.enricher) cfg.enricher = {};
	if (cfg.enricher.tag_adjudicator) return;
	cfg.enricher.tag_adjudicator = {
		enabled: false,
		timeout: 0,
		llm: { adapter: '', provider: '', model: '', token: '', temperature: 0, request_delay: 0 }
	};
}

function ensureTagQualityBlock() {
	if (!cfg) return;
	if (!cfg.enricher) cfg.enricher = {};
	if (cfg.enricher.tag_quality) return;
	cfg.enricher.tag_quality = {
		enabled: false,
		timeout: 0,
		llm: { adapter: '', provider: '', model: '', token: '', temperature: 0, request_delay: 0 }
	};
}

function syncMimeCheckboxes() {
	const types = cfg?.available_file_types;
	const exts = cfg?.consumer?.supported_files ?? [];
	if (!types) return;
	mimeTypeOptions = types.map((ft) => ({
		...ft,
		checked: ft.required || ft.extensions.some((e) => exts.includes(e))
	}));
}

function bodyFromConfig() {
	return {
		'server.host': cfg.server.host,
		'server.port': Number(cfg.server.port),
		'server.max_upload_size': Number(cfg.server.max_upload_size),
		'server.max_download_files': Number(cfg.server.max_download_files),
		'server.max_download_size_mb': Number(cfg.server.max_download_size_mb),
		'server.max_concurrent_batches': Number(cfg.server.max_concurrent_batches),
		'server.max_batch_delete': Number(cfg.server.max_batch_delete),
		'server.auth_enabled': cfg.server.auth_enabled,
		'consumer.ocr.engine': cfg.consumer.ocr.engine,
		'consumer.ocr.languages': cfg.consumer.ocr.languages.filter(Boolean),
		'consumer.ocr.data_dir': cfg.consumer.ocr.data_dir,
		'consumer.ocr.timeout': Number(cfg.consumer.ocr.timeout),
		'consumer.ocr.ocr_workers': Number(cfg.consumer.ocr.ocr_workers),
		'consumer.workers': Number(cfg.consumer.workers),
		'consumer.max_files_per_batch': Number(cfg.consumer.max_files_per_batch),
		'consumer.supported_files':
			mimeTypeOptions.length > 0
				? mimeTypeOptions.filter((o) => o.checked).flatMap((o) => o.extensions)
				: (cfg.consumer.supported_files ?? []),
		'consumer.converter.enabled': cfg.consumer.converter.enabled,
		'consumer.converter.binary': cfg.consumer.converter.binary,
		'consumer.converter.timeout': Number(cfg.consumer.converter.timeout),
		'consumer.polling.enabled': cfg.consumer.polling.enabled,
		'consumer.polling.interval': Number(cfg.consumer.polling.interval),
		'consumer.polling.windows': cfg.consumer.polling.windows ?? [],
		'consumer.reclaim.enabled': cfg.consumer.reclaim.enabled,
		'consumer.reclaim.max_retries': Number(cfg.consumer.reclaim.max_retries),
		'consumer.reclaim.stale_task_after': Number(cfg.consumer.reclaim.stale_task_after),
		'consumer.thumbnail.enabled': cfg.consumer.thumbnail.enabled,
		'consumer.thumbnail.engine': cfg.consumer.thumbnail.engine,
		'consumer.thumbnail.dpi': Number(cfg.consumer.thumbnail.dpi),
		'consumer.thumbnail.max_width': Number(cfg.consumer.thumbnail.max_width),
		'consumer.thumbnail.quality': Number(cfg.consumer.thumbnail.quality),
		'consumer.thumbnail.timeout': Number(cfg.consumer.thumbnail.timeout),
		'consumer.thumbnail.workers': Number(cfg.consumer.thumbnail.workers),
		'consumer.thumbnail.backfill_interval': Number(cfg.consumer.thumbnail.backfill_interval),
		'consumer.thumbnail.backfill_time': cfg.consumer.thumbnail.backfill_time,
		'consumer.pdfoptimizer.engine': cfg.consumer.pdfoptimizer.engine,
		'consumer.pdfoptimizer.fallback': cfg.consumer.pdfoptimizer.fallback,
		'consumer.pdfoptimizer.timeout': Number(cfg.consumer.pdfoptimizer.timeout),
		'consumer.textextractor.engine': cfg.consumer.textextractor.engine,
		'consumer.textextractor.timeout': Number(cfg.consumer.textextractor.timeout),
		'enricher.workers': Number(cfg.enricher.workers),
		'enricher.textreducer.engine': cfg.enricher.textreducer.engine,
		'enricher.textreducer.timeout': Number(cfg.enricher.textreducer.timeout),
		'enricher.textreducer.target_words': Number(cfg.enricher.textreducer.target_words),
		'enricher.contentanalyzer.enabled': cfg.enricher.contentanalyzer.enabled,
		'enricher.contentanalyzer.timeout': Number(cfg.enricher.contentanalyzer.timeout),
		'enricher.contentanalyzer.min_tags': Number(cfg.enricher.contentanalyzer.min_tags),
		'enricher.contentanalyzer.prompt_template': cfg.enricher.contentanalyzer.prompt_template,
		'enricher.contentanalyzer.pause_on_credit_error':
			cfg.enricher.contentanalyzer.pause_on_credit_error,
		'enricher.contentanalyzer.doc_type_refinement.enabled':
			cfg.enricher.contentanalyzer.doc_type_refinement.enabled,
		'enricher.contentanalyzer.doc_type_refinement.head_words': Number(
			cfg.enricher.contentanalyzer.doc_type_refinement.head_words
		),
		'enricher.contentanalyzer.doc_type_refinement.tail_words': Number(
			cfg.enricher.contentanalyzer.doc_type_refinement.tail_words
		),
		'enricher.contentanalyzer.llm.adapter': cfg.enricher.contentanalyzer.llm.adapter,
		'enricher.contentanalyzer.llm.provider': cfg.enricher.contentanalyzer.llm.provider,
		'enricher.contentanalyzer.llm.model': cfg.enricher.contentanalyzer.llm.model,
		'enricher.contentanalyzer.llm.token': cfg.enricher.contentanalyzer.llm.token,
		'enricher.contentanalyzer.llm.temperature': Number(
			cfg.enricher.contentanalyzer.llm.temperature
		),
		'enricher.contentanalyzer.llm.request_delay': Number(
			cfg.enricher.contentanalyzer.llm.request_delay
		),
		'enricher.contentanalyzer.fallbacks': cfg.enricher.contentanalyzer.fallbacks ?? [],
		'enricher.tagmatcher.timeout': Number(cfg.enricher.tagmatcher.timeout),
		'enricher.tagmatcher.reduce_target_words': Number(cfg.enricher.tagmatcher.reduce_target_words),
		'enricher.tagmatcher.chunk_size': Number(cfg.enricher.tagmatcher.chunk_size),
		'enricher.tagmatcher.top_n': Number(cfg.enricher.tagmatcher.top_n),
		'enricher.tagmatcher.hugot.model': cfg.enricher.tagmatcher.hugot.model,
		'enricher.tagmatcher.hugot.backend': cfg.enricher.tagmatcher.hugot.backend,
		'enricher.tag_adjudicator.enabled': cfg.enricher.tag_adjudicator.enabled,
		'enricher.tag_adjudicator.timeout': Number(cfg.enricher.tag_adjudicator.timeout),
		'enricher.tag_adjudicator.llm.adapter': cfg.enricher.tag_adjudicator.llm.adapter,
		'enricher.tag_adjudicator.llm.provider': cfg.enricher.tag_adjudicator.llm.provider,
		'enricher.tag_adjudicator.llm.model': cfg.enricher.tag_adjudicator.llm.model,
		'enricher.tag_adjudicator.llm.token': cfg.enricher.tag_adjudicator.llm.token,
		'enricher.tag_adjudicator.llm.request_delay': Number(
			cfg.enricher.tag_adjudicator.llm.request_delay
		),
		'enricher.tag_quality.enabled': cfg.enricher.tag_quality.enabled,
		'enricher.tag_quality.timeout': Number(cfg.enricher.tag_quality.timeout),
		'enricher.tag_quality.llm.adapter': cfg.enricher.tag_quality.llm.adapter,
		'enricher.tag_quality.llm.provider': cfg.enricher.tag_quality.llm.provider,
		'enricher.tag_quality.llm.model': cfg.enricher.tag_quality.llm.model,
		'enricher.tag_quality.llm.token': cfg.enricher.tag_quality.llm.token,
		'enricher.tag_quality.llm.request_delay': Number(cfg.enricher.tag_quality.llm.request_delay),
		'enricher.tag_reconcile.enabled': cfg.enricher.tag_reconcile.enabled,
		'enricher.tag_reconcile.report_only': cfg.enricher.tag_reconcile.report_only,
		'enricher.tag_reconcile.interval': Number(cfg.enricher.tag_reconcile.interval),
		'enricher.tag_reconcile.time': cfg.enricher.tag_reconcile.time,
		'enricher.tag_reconcile.max_rejudge': Number(cfg.enricher.tag_reconcile.max_rejudge),
		'enricher.tag_reconcile.max_retag': Number(cfg.enricher.tag_reconcile.max_retag),
		'enricher.tag_reconcile.dead_tag_grace_days': Number(
			cfg.enricher.tag_reconcile.dead_tag_grace_days
		),
		'enricher.tag_reconcile.provisional_grace_days': Number(
			cfg.enricher.tag_reconcile.provisional_grace_days
		),
		'enricher.tag_measure.enabled': cfg.enricher.tag_measure.enabled,
		'enricher.tag_measure.interval': Number(cfg.enricher.tag_measure.interval),
		'enricher.tag_measure.time': cfg.enricher.tag_measure.time,
		'enricher.tag_audit.enabled': cfg.enricher.tag_audit.enabled,
		'enricher.tag_audit.interval': Number(cfg.enricher.tag_audit.interval),
		'enricher.tag_audit.time': cfg.enricher.tag_audit.time,
		'enricher.tag_audit.sample_size': Number(cfg.enricher.tag_audit.sample_size),
		'enricher.tag_maintenance.enabled': cfg.enricher.tag_maintenance.enabled,
		'enricher.tag_maintenance.interval': Number(cfg.enricher.tag_maintenance.interval),
		'enricher.tag_maintenance.time': cfg.enricher.tag_maintenance.time,
		'storage.consumption_dir': cfg.storage.consumption_dir,
		'storage.storage_dir': cfg.storage.storage_dir,
		'storage.migration_mode': cfg.storage.migration_mode,
		'storage.trash.retention_days': Number(cfg.storage.trash.retention_days),
		'database.host': cfg.database.host,
		'database.port': Number(cfg.database.port),
		'database.user': cfg.database.user,
		'database.database': cfg.database.database,
		'database.sslmode': cfg.database.sslmode,
		'database.runtime': cfg.database.runtime,
		'database.container': cfg.database.container,
		'backup.enabled': cfg.backup.enabled,
		'backup.path': cfg.backup.path,
		'backup.schedules': cfg.backup.schedules ?? [],
		'mirror.enabled': cfg.mirror.enabled,
		'mirror.path': cfg.mirror.path,
		'mirror.interval': Number(cfg.mirror.interval),
		'mirror.time': cfg.mirror.time,
		'app.log_level': cfg.app.log_level,
		'app.logging.max_size': Number(cfg.app.logging.max_size),
		'app.logging.max_backups': Number(cfg.app.logging.max_backups),
		'app.logging.max_age': Number(cfg.app.logging.max_age),
		'app.logging.compress': cfg.app.logging.compress
	};
}

const PROCESSING_PREFIXES = [
	'consumer.ocr.',
	'consumer.textextractor.',
	'consumer.pdfoptimizer.',
	'consumer.thumbnail.',
	'consumer.workers',
	'consumer.max_files_per_batch',
	'consumer.reclaim.'
];

function groupOfKey(key) {
	if (key === 'server.auth_enabled') return 'access';
	if (key.startsWith('enricher.')) return 'enrichment';
	if (key.startsWith('storage.') || key.startsWith('database.')) return 'storage';
	if (key.startsWith('backup.') || key.startsWith('mirror.')) return 'safety';
	if (key.startsWith('app.')) return 'system';
	if (PROCESSING_PREFIXES.some((prefix) => key.startsWith(prefix))) return 'processing';
	return 'intake';
}

let changedKeys = $derived.by(() => {
	if (!cfg || !snapshotJson) return [];
	const snap = JSON.parse(snapshotJson);
	const current = bodyFromConfig();
	return Object.keys(current).filter(
		(key) => JSON.stringify(current[key]) !== JSON.stringify(snap[key])
	);
});

let configChanges = $derived.by(() => {
	if (!snapshotJson) return [];
	const snap = JSON.parse(snapshotJson);
	const current = bodyFromConfig();
	return changedKeys.map((key) => {
		const secret = key.endsWith('.token');
		return {
			key,
			label: key,
			from: secret ? null : snap[key],
			to: secret ? null : current[key],
			secret,
			group: groupOfKey(key)
		};
	});
});

let pendingCount = $derived(changedKeys.length + catalogOps.length);
let busy = $derived(saving || resetting);

let groupCounts = $derived.by(() => {
	const counts = {};
	for (const key of changedKeys) {
		const group = groupOfKey(key);
		counts[group] = (counts[group] ?? 0) + 1;
	}
	if (catalogOps.length > 0) counts.models = (counts.models ?? 0) + catalogOps.length;
	return counts;
});

let providerCount = $derived(catalog.providers.length);
let modelCount = $derived(catalog.models.length);

let toolSummary = $derived({
	known: statusLoaded,
	total: toolStatus.length,
	available: toolStatus.filter((t) => t.available).length
});

function snapshotState() {
	snapshot = $state.snapshot(cfg);
	snapshotJson = JSON.stringify(bodyFromConfig());
}

function isConfigDirty() {
	if (!cfg || !snapshotJson) return false;
	return JSON.stringify(bodyFromConfig()) !== snapshotJson;
}

function isDirty() {
	return isConfigDirty() || catalogOps.length > 0;
}

async function checkStatus() {
	const status = await api.config.status();
	if (status) {
		toolStatus = status.tools ?? [];
		statusLoaded = true;
	}
}

async function refreshLlmModels() {
	const models = await api.config.llmModels();
	if (models) llmModels = models;
}

async function loadCatalog() {
	const res = await api.llm.catalog();
	if (res.ok) {
		catalog = res.data ?? { providers: [], models: [] };
		catalogError = '';
	} else {
		catalogError = res.data?.error ?? `Failed to load the catalog (${res.status}).`;
	}
	catalogLoaded = true;
}

async function load() {
	if (loading || loaded) return;
	loading = true;
	loadError = '';
	try {
		const loadedCfg = await api.config.get();
		if (!loadedCfg) {
			loadError = 'Settings could not be loaded.';
			return;
		}
		cfg = loadedCfg;
		ensureAll();
		syncMimeCheckboxes();
		snapshotState();
		loaded = true;
		checkStatus();
		refreshLlmModels();
		loadCatalog();
	} finally {
		loading = false;
	}
}

function restoreAdvanced() {
	try {
		advanced = localStorage.getItem(ADVANCED_KEY) === 'true';
	} catch {
		advanced = false;
	}
}

function setAdvanced(value) {
	advanced = value;
	try {
		localStorage.setItem(ADVANCED_KEY, String(value));
	} catch {
		return;
	}
}

function setByPath(target, path, value) {
	const parts = path.split('.');
	let cursor = target;
	for (let i = 0; i < parts.length - 1; i++) {
		if (cursor[parts[i]] === undefined) cursor[parts[i]] = {};
		cursor = cursor[parts[i]];
	}
	cursor[parts[parts.length - 1]] = value;
}

function deleteByPath(target, path) {
	const parts = path.split('.');
	let cursor = target;
	for (let i = 0; i < parts.length - 1; i++) {
		cursor = cursor[parts[i]];
		if (cursor === undefined) return;
	}
	delete cursor[parts[parts.length - 1]];
}

function discardConfigChange(key) {
	if (!snapshot) return;
	const snap = JSON.parse(snapshotJson);
	const original = snap[key];
	if (original === undefined) deleteByPath(cfg, key);
	else setByPath(cfg, key, structuredClone(original));
	ensureAll();
	syncMimeCheckboxes();
}

async function runCatalogOp(op) {
	switch (op.kind) {
		case 'provider-add':
			return api.llm.addProvider(op.payload);
		case 'provider-update':
			return api.llm.setProvider(op.provider, op.payload);
		case 'provider-remove':
			return api.llm.removeProvider(op.provider, op.cascade);
		case 'model-add':
			return api.llm.addModel(op.payload);
		case 'model-update':
			return api.llm.setModel(op.provider, op.modelId, op.payload);
		case 'model-remove':
			return api.llm.removeModel(op.provider, op.modelId);
		default:
			return { ok: false, status: 0, data: { error: 'Unknown catalog operation.' } };
	}
}

async function applyAll() {
	if (busy || !isDirty()) return;
	const configDirty = isConfigDirty();
	const hadCatalogOps = catalogOps.length > 0;
	const failures = [];
	try {
		saving = true;
		if (configDirty) {
			const res = await api.config.update(bodyFromConfig());
			if (!res.ok) {
				throw new Error(res.data?.error ?? `Saving settings failed (${res.status}).`);
			}
			const reloaded = await api.config.get();
			if (reloaded) {
				cfg = reloaded;
				ensureAll();
				syncMimeCheckboxes();
			}
			snapshotState();
		}

		const remaining = [];
		for (const op of catalogOps) {
			const res = await runCatalogOp(op);
			if (!res.ok) {
				remaining.push(op);
				failures.push({
					label: op.label,
					error: res.data?.error ?? `HTTP ${res.status}`
				});
			}
		}
		catalogOps = remaining;

		if (hadCatalogOps) {
			await loadCatalog();
			await refreshLlmModels();
		}
		if (configDirty) checkStatus();

		if (failures.length > 0) {
			const detail = failures.map((failure) => `${failure.label} (${failure.error})`).join('; ');
			toastStore.error(`Some catalog changes could not be applied: ${detail}. They remain staged.`);
		} else {
			toastStore.success(pendingCount === 0 ? 'All changes applied.' : 'Changes applied.');
		}
	} catch (error) {
		toastStore.error(error.message);
	} finally {
		saving = false;
	}
}

async function discardAll() {
	if (busy || !snapshot) return;
	cfg = $state.snapshot(snapshot);
	ensureAll();
	syncMimeCheckboxes();
	catalogOps = [];
	await loadCatalog();
	toastStore.info('Staged changes discarded.');
}

function nextOpId() {
	return `op-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`;
}

function addProvider(payload) {
	catalog.providers = [...catalog.providers, { ...payload }];
	catalogOps = [
		...catalogOps,
		{
			id: nextOpId(),
			kind: 'provider-add',
			label: `Provider "${payload.provider}" added`,
			payload
		}
	];
}

function updateProvider(name, payload) {
	catalog.providers = catalog.providers.map((provider) =>
		provider.provider === name ? { ...provider, ...payload } : provider
	);
	catalogOps = [
		...catalogOps,
		{
			id: nextOpId(),
			kind: 'provider-update',
			provider: name,
			label: `Provider "${name}" updated`,
			payload
		}
	];
}

function removeProvider(name, cascade) {
	catalog.providers = catalog.providers.filter((provider) => provider.provider !== name);
	catalog.models = catalog.models.filter((model) => model.provider !== name);
	catalogOps = [
		...catalogOps,
		{
			id: nextOpId(),
			kind: 'provider-remove',
			provider: name,
			cascade,
			label: `Provider "${name}" removed`
		}
	];
}

function addModel(payload) {
	catalog.models = [...catalog.models, { ...payload }];
	catalogOps = [
		...catalogOps,
		{
			id: nextOpId(),
			kind: 'model-add',
			label: `Model "${payload.provider}/${payload.model_id}" added`,
			payload
		}
	];
}

function updateModel(provider, modelId, payload) {
	catalog.models = catalog.models.map((model) =>
		model.provider === provider && model.model_id === modelId ? { ...model, ...payload } : model
	);
	catalogOps = [
		...catalogOps,
		{
			id: nextOpId(),
			kind: 'model-update',
			provider,
			modelId,
			label: `Model "${provider}/${modelId}" updated`,
			payload
		}
	];
}

function removeModel(provider, modelId) {
	catalog.models = catalog.models.filter(
		(model) => !(model.provider === provider && model.model_id === modelId)
	);
	catalogOps = [
		...catalogOps,
		{
			id: nextOpId(),
			kind: 'model-remove',
			provider,
			modelId,
			label: `Model "${provider}/${modelId}" removed`
		}
	];
}

async function resetCatalog() {
	if (busy) {
		toastStore.error('Wait for the current operation to finish before restoring catalogs.');
		return false;
	}
	resetting = true;
	try {
		const res = await api.llm.reset({ providers: true, models: true });
		if (!res.ok) {
			toastStore.error(res.data?.error ?? `Restoring catalogs failed (${res.status}).`);
			return false;
		}
		catalogOps = [];
		await loadCatalog();
		await refreshLlmModels();
		toastStore.success('Catalogs restored to defaults.');
		return true;
	} finally {
		resetting = false;
	}
}

function reset() {
	cfg = null;
	snapshot = null;
	snapshotJson = '';
	loading = false;
	loaded = false;
	loadError = '';
	saving = false;
	resetting = false;
	toolStatus = [];
	statusLoaded = false;
	llmModels = { adapters: {}, providers: {} };
	mimeTypeOptions = [];
	catalog = { providers: [], models: [] };
	catalogLoaded = false;
	catalogError = '';
	catalogOps = [];
	trayOpen = false;
}

export const settingsStore = {
	get cfg() {
		return cfg;
	},
	get loaded() {
		return loaded;
	},
	get loadError() {
		return loadError;
	},
	get saving() {
		return saving;
	},
	get busy() {
		return busy;
	},
	get advanced() {
		return advanced;
	},
	get toolStatus() {
		return toolStatus;
	},
	get statusLoaded() {
		return statusLoaded;
	},
	get toolSummary() {
		return toolSummary;
	},
	get llmModels() {
		return llmModels;
	},
	get mimeTypeOptions() {
		return mimeTypeOptions;
	},
	get catalog() {
		return catalog;
	},
	get catalogLoaded() {
		return catalogLoaded;
	},
	get catalogError() {
		return catalogError;
	},
	get catalogOps() {
		return catalogOps;
	},
	get trayOpen() {
		return trayOpen;
	},
	get providerCount() {
		return providerCount;
	},
	get modelCount() {
		return modelCount;
	},
	get configChanges() {
		return configChanges;
	},
	get groupCounts() {
		return groupCounts;
	},
	get pendingCount() {
		return pendingCount;
	},
	load,
	reset,
	restoreAdvanced,
	setAdvanced,
	openTray,
	closeTray,
	isDirty,
	applyAll,
	discardAll,
	discardConfigChange,
	addProvider,
	updateProvider,
	removeProvider,
	addModel,
	updateModel,
	removeModel,
	resetCatalog
};
