<script>
	import { onMount, tick } from 'svelte';
	import { replaceState } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { api } from '$lib/api';
	import { toastStore } from '$lib/stores/toastStore.svelte.js';
	import * as authStore from '$lib/stores/authStore.js';
	import AccessSection from '$lib/components/settings/AccessSection.svelte';
	import EnrichmentSection from '$lib/components/settings/EnrichmentSection.svelte';
	import IntakeSection from '$lib/components/settings/IntakeSection.svelte';
	import OverviewSection from '$lib/components/settings/OverviewSection.svelte';
	import ProcessingSection from '$lib/components/settings/ProcessingSection.svelte';
	import SafetySection from '$lib/components/settings/SafetySection.svelte';
	import SaveBar from '$lib/components/settings/SaveBar.svelte';
	import SettingsRail from '$lib/components/settings/SettingsRail.svelte';
	import SettingsSearch from '$lib/components/settings/SettingsSearch.svelte';
	import StorageSection from '$lib/components/settings/StorageSection.svelte';
	import SystemSection from '$lib/components/settings/SystemSection.svelte';

	let cfg = $state(null);
	let saving = $state(false);
	let toolStatus = $state([]);
	let statusLoaded = $state(false);
	let llmModels = $state({ adapters: {}, providers: {} });
	let mimeTypeOptions = $state([]);

	let snapshot = $state(null);
	let snapshotJson = $state('');

	let searchQuery = $state('');
	let searchEmpty = $state(false);
	let rootEl = $state(null);
	let matchCounts = $state({});
	let activeGroup = $state('overview');
	let scrollContainer = null;

	function ensureAll() {
		ensureFallbacksArray();
		ensureThumbnailBlock();
		ensureMirrorBlock();
		ensureTagReconcileBlock();
		ensureTagAdjudicatorBlock();
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
			max_retag: 1000
		};
	}

	function ensureTagAdjudicatorBlock() {
		if (!cfg) return;
		if (!cfg.enricher) cfg.enricher = {};
		if (cfg.enricher.tag_adjudicator) return;
		cfg.enricher.tag_adjudicator = {
			enabled: false,
			timeout: 0,
			llm: {
				adapter: '',
				provider: '',
				model: '',
				token: '',
				temperature: 0,
				request_delay: 0
			}
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
			'enricher.tagmatcher.reduce_target_words': Number(
				cfg.enricher.tagmatcher.reduce_target_words
			),
			'enricher.tagmatcher.chunk_size': Number(cfg.enricher.tagmatcher.chunk_size),
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
			'enricher.tag_reconcile.enabled': cfg.enricher.tag_reconcile.enabled,
			'enricher.tag_reconcile.report_only': cfg.enricher.tag_reconcile.report_only,
			'enricher.tag_reconcile.interval': Number(cfg.enricher.tag_reconcile.interval),
			'enricher.tag_reconcile.time': cfg.enricher.tag_reconcile.time,
			'enricher.tag_reconcile.max_rejudge': Number(cfg.enricher.tag_reconcile.max_rejudge),
			'enricher.tag_reconcile.max_retag': Number(cfg.enricher.tag_reconcile.max_retag),
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
	let dirtyCount = $derived(changedKeys.length);
	let dirtyGroups = $derived(new Set(changedKeys.map(groupOfKey)));

	let toolsSummary = $derived({
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

	function handleBeforeUnload(e) {
		if (!isConfigDirty()) return;
		e.preventDefault();
		e.returnValue = '';
	}

	async function checkStatus() {
		const status = await api.config.status();
		if (status) {
			toolStatus = status.tools ?? [];
			statusLoaded = true;
		}
	}

	async function save() {
		saving = true;
		try {
			const res = await api.config.update(bodyFromConfig());
			if (res?.message) {
				toastStore.success(res.message);
			} else {
				toastStore.success('Settings saved.');
			}
			const loaded = await api.config.get();
			if (loaded) {
				cfg = loaded;
				ensureAll();
				syncMimeCheckboxes();
				snapshotState();
			}
			checkStatus();
		} catch (e) {
			toastStore.error(e.message);
		} finally {
			saving = false;
		}
	}

	function revert() {
		if (!snapshot) return;
		cfg = structuredClone(snapshot);
		ensureAll();
		syncMimeCheckboxes();
		toastStore.info('Changes reverted.');
	}

	function onScroll() {
		if (!rootEl) return;
		const groups = Array.from(rootEl.querySelectorAll('.group')).filter((g) => !g.hidden);
		if (groups.length === 0) return;
		const containerTop =
			scrollContainer && scrollContainer !== document.scrollingElement
				? scrollContainer.getBoundingClientRect().top
				: 0;
		const threshold = containerTop + 140;
		let current = groups[0].id;
		for (const group of groups) {
			if (group.getBoundingClientRect().top <= threshold) current = group.id;
		}
		activeGroup = current;
	}

	function handleHashChange() {
		const id = window.location.hash.slice(1);
		if (id) activeGroup = id;
	}

	function handleLegacyTab() {
		const url = new URL(window.location.href);
		const tab = url.searchParams.get('tab');
		if (!tab) return;
		url.searchParams.delete('tab');
		const hash = tab.toLowerCase() === 'users' ? '#access' : '';
		replaceState(resolve(`${url.pathname}${url.search}${hash}`));
	}

	function scrollToHash() {
		const id = window.location.hash.slice(1);
		if (!id) return;
		document.getElementById(id)?.scrollIntoView({ block: 'start' });
	}

	$effect(() => {
		const query = searchQuery.trim().toLowerCase();
		const root = rootEl;
		if (!root) return;

		root.querySelectorAll('.field').forEach((field) => {
			const card = field.closest('.card');
			const cardHit = !!query && card?.dataset.name?.toLowerCase().includes(query);
			const haystack = field.textContent.toLowerCase();
			field.hidden = !!query && !haystack.includes(query) && !cardHit;
		});

		root.querySelectorAll('details.adv').forEach((details) => {
			if (query) {
				if (details.dataset.was === undefined) details.dataset.was = String(details.open);
				details.open = true;
			} else if (details.dataset.was !== undefined) {
				details.open = details.dataset.was === 'true';
				delete details.dataset.was;
			}
		});

		const counts = {};
		let matched = false;
		root.querySelectorAll('.card').forEach((card) => {
			const fields = Array.from(card.querySelectorAll('.field'));
			let visible = fields.some((field) => !field.hidden);
			const cardHit = !!query && card?.dataset.name?.toLowerCase().includes(query);
			if (cardHit && !visible) {
				fields.forEach((field) => (field.hidden = false));
				visible = true;
			}
			card.hidden = !!query && !visible;
			if (query && visible) {
				matched = true;
				const group = card.closest('.group');
				if (group) {
					counts[group.id] =
						(counts[group.id] ?? 0) + fields.filter((field) => !field.hidden).length;
				}
			}
		});

		root.querySelectorAll('.group').forEach((group) => {
			const anyCard = Array.from(group.querySelectorAll('.card')).some((card) => !card.hidden);
			group.hidden = !!query && !anyCard;
		});

		matchCounts = counts;
		searchEmpty = !!query && !matched;
		onScroll();
	});

	onMount(async () => {
		const loaded = await api.config.get();
		if (loaded) {
			cfg = loaded;
			ensureAll();
			syncMimeCheckboxes();
			snapshotState();
			checkStatus();
		}
		llmModels = await api.config.llmModels();

		handleLegacyTab();
		window.addEventListener('beforeunload', handleBeforeUnload);
		window.addEventListener('hashchange', handleHashChange);
		scrollContainer = document.getElementById('main-content') ?? document.scrollingElement;
		scrollContainer?.addEventListener('scroll', onScroll, { passive: true });
		await tick();
		scrollToHash();
		onScroll();

		return () => {
			window.removeEventListener('beforeunload', handleBeforeUnload);
			window.removeEventListener('hashchange', handleHashChange);
			scrollContainer?.removeEventListener('scroll', onScroll);
		};
	});
</script>

{#if authStore.authEnabled() && !authStore.isAdmin()}
	<p class="text-parchment-500">You do not have permission to view this page.</p>
{:else if !cfg}
	<div class="text-parchment-500">Loading settings…</div>
{:else}
	<div class="mx-auto max-w-[64rem] pb-24" bind:this={rootEl}>
		<div class="mb-7 max-w-3xl">
			<h1 class="text-[1.625rem] font-semibold tracking-[-0.01em] text-parchment-200">Settings</h1>
			<p class="mt-1.5 mb-4 text-parchment-500">
				Every knob of the pipeline, grouped the way a document travels through it.
			</p>
			<SettingsSearch bind:value={searchQuery} />
		</div>

		<div class="grid gap-10 lg:grid-cols-[13.75rem_minmax(0,1fr)] lg:items-start">
			<SettingsRail active={activeGroup} {dirtyGroups} counts={matchCounts} tools={toolsSummary} />
			<div class="max-w-[46rem] min-w-0">
				<OverviewSection tools={toolStatus} statusKnown={statusLoaded} />
				<IntakeSection {cfg} {mimeTypeOptions} />
				<ProcessingSection {cfg} {toolStatus} statusKnown={statusLoaded} />
				<EnrichmentSection {cfg} {llmModels} {toolStatus} statusKnown={statusLoaded} />
				<StorageSection {cfg} />
				<SafetySection {cfg} {toolStatus} statusKnown={statusLoaded} />
				<AccessSection {cfg} />
				<SystemSection {cfg} />
				{#if searchEmpty}
					<div class="py-12 text-center text-parchment-500">
						<p>No settings match "{searchQuery.trim()}".</p>
					</div>
				{/if}
			</div>
		</div>
	</div>
	<SaveBar count={dirtyCount} {saving} onSave={save} onRevert={revert} />
{/if}
