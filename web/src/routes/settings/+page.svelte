<script>
	import { onMount } from 'svelte';
	import { api } from '$lib/api';
	import { hintsForEngine } from '$lib/tools.js';
	import { toastStore } from '$lib/stores/toastStore.svelte.js';
	import DataTable from '$lib/components/DataTable.svelte';
	import Modal from '$lib/components/Modal.svelte';
	import { EDIT_ICON, DELETE_ICON, actionButton } from '$lib/icons.js';
	import { escapeHtml } from '$lib/utils/html.js';
	import { confirmStore } from '$lib/stores/confirmStore.svelte.js';
	import * as authStore from '$lib/stores/authStore.js';

	let cfg = $state(null);
	let saving = $state(false);
	let pendingTasks = $state(0);
	let missingTools = $state([]);
	let toolStatus = $state([]);
	let pollInterval;
	let showToken = $state(false);
	let showFallbackToken = $state(false);

	let activeTab = $state(
		typeof window !== 'undefined'
			? new URL(window.location.href).searchParams.get('tab') || 'Configuration'
			: 'Configuration'
	);

	function switchTab(tab) {
		activeTab = tab;
		const url = new URL(window.location.href);
		url.searchParams.set('tab', tab);
		history.replaceState(null, '', url.pathname + url.search);
	}

	const TABS = ['Configuration', 'Users'];

	function handleTabKeydown(e) {
		const idx = TABS.indexOf(activeTab);
		let next = -1;
		if (e.key === 'ArrowRight') next = (idx + 1) % TABS.length;
		else if (e.key === 'ArrowLeft') next = (idx - 1 + TABS.length) % TABS.length;
		else if (e.key === 'Home') next = 0;
		else if (e.key === 'End') next = TABS.length - 1;
		if (next >= 0) {
			e.preventDefault();
			switchTab(TABS[next]);
		}
	}

	let showUserModal = $state(false);
	let editingUser = $state(null);
	let formUsername = $state('');
	let formPassword = $state('');
	let formRole = $state('viewer');
	let userError = $state('');
	let savingUser = $state(false);
	let refreshKey = $state(0);

	let llmModels = $state({ adapters: {}, providers: {} });

	let mimeTypeOptions = $state([]);

	let originalCfg = '';
	let originalMimeOptions = '';

	function snapshotState() {
		originalCfg = JSON.stringify(cfg);
		originalMimeOptions = JSON.stringify(mimeTypeOptions);
	}

	function isConfigDirty() {
		if (!cfg) return false;
		return (
			JSON.stringify(cfg) !== originalCfg || JSON.stringify(mimeTypeOptions) !== originalMimeOptions
		);
	}

	function handleBeforeUnload(e) {
		if (!isConfigDirty()) return;
		e.preventDefault();
		e.returnValue = '';
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

	let selectedAdapterProviders = $derived(
		llmModels.adapters[cfg?.enricher?.contentanalyzer?.llm?.adapter] ?? []
	);
	let selectedProviderModels = $derived(
		llmModels.providers[cfg?.enricher?.contentanalyzer?.llm?.provider] ?? []
	);
	let fallbackAdapterProviders = $derived(
		llmModels.adapters[cfg?.enricher?.contentanalyzer?.fallback?.llm?.adapter] ?? []
	);
	let fallbackProviderModels = $derived(
		llmModels.providers[cfg?.enricher?.contentanalyzer?.fallback?.llm?.provider] ?? []
	);

	const userColumns = [
		{
			key: 'username',
			label: 'Username',
			sortable: true,
			width: '100%'
		},
		{
			key: 'role',
			label: 'Role',
			sortable: true,
			cell: (_v, row) => {
				const cls = authStore.roleBadgeClass(row.role);
				return `<span class="inline-block rounded-full px-2 py-0.5 text-xs font-medium ${cls}">${escapeHtml(row.role)}</span>`;
			}
		},
		{
			key: 'created_at',
			label: 'Created At',
			sortable: true,
			cell: (_v, row) => {
				if (!row.created_at) return '-';
				const d = new Date(row.created_at);
				return d.toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' });
			}
		},
		{
			key: 'actions',
			label: 'Actions',
			sortable: false,
			cellClass: 'whitespace-nowrap',
			cell: (_v, row) => {
				const safeName = escapeHtml(row.username);
				const safeRole = escapeHtml(row.role || 'viewer');
				return `${actionButton(EDIT_ICON, 'Edit', 'text-parchment-400 hover:text-gold-500', { 'data-edit-user': row.id, 'data-user-name': safeName, 'data-user-role': safeRole })}
${actionButton(DELETE_ICON, 'Delete', 'text-parchment-400 hover:text-terracotta-500', { 'data-delete-user': row.id, 'data-user-name': safeName })}`;
			}
		}
	];

	function ensureFallbackBlock() {
		if (!cfg) return;
		const ca = cfg.enricher.contentanalyzer;
		if (ca.fallback) return;
		ca.fallback = {
			enabled: false,
			llm: { adapter: '', provider: '', model: '', token: '', temperature: 0, request_delay: 0 }
		};
	}

	onMount(async () => {
		const loaded = await api.config.get();
		if (loaded) {
			cfg = loaded;
			ensureFallbackBlock();
			syncMimeCheckboxes();
			checkStatus();
			snapshotState();
		}
		llmModels = await api.config.llmModels();
		window.addEventListener('beforeunload', handleBeforeUnload);
		return () => {
			window.removeEventListener('beforeunload', handleBeforeUnload);
			if (pollInterval) clearInterval(pollInterval);
		};
	});

	async function checkStatus() {
		const status = await api.config.status();
		if (status) {
			pendingTasks = status.pending_tasks;
			missingTools = status.missing_tools ?? [];
			toolStatus = status.tools ?? [];
		}
	}

	function startPolling() {
		if (pollInterval) clearInterval(pollInterval);
		pollInterval = setInterval(async () => {
			await checkStatus();
			if (pendingTasks === 0) {
				clearInterval(pollInterval);
				pollInterval = null;
			}
		}, 3000);
	}

	function addLanguage() {
		cfg.consumer.ocr.languages = [...cfg.consumer.ocr.languages, ''];
	}

	function removeLanguage(index) {
		cfg.consumer.ocr.languages = cfg.consumer.ocr.languages.filter((_, i) => i !== index);
	}

	function updateLanguage(index, value) {
		cfg.consumer.ocr.languages = cfg.consumer.ocr.languages.map((lang, i) =>
			i === index ? value : lang
		);
	}

	function addWindow() {
		if (!cfg.consumer.polling.windows) cfg.consumer.polling.windows = [];
		cfg.consumer.polling.windows = [...cfg.consumer.polling.windows, { start: '', end: '' }];
	}
	function removeWindow(index) {
		cfg.consumer.polling.windows = cfg.consumer.polling.windows.filter((_, i) => i !== index);
	}

	function addSchedule() {
		if (!cfg.backup.schedules) cfg.backup.schedules = [];
		cfg.backup.schedules = [
			...cfg.backup.schedules,
			{ mode: 'full', interval: 1, time: '02:00', keep: 7, path: '' }
		];
	}
	function removeSchedule(index) {
		cfg.backup.schedules = cfg.backup.schedules.filter((_, i) => i !== index);
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
			'enricher.contentanalyzer.fallback.enabled':
				cfg.enricher.contentanalyzer.fallback?.enabled ?? false,
			'enricher.contentanalyzer.fallback.llm.adapter':
				cfg.enricher.contentanalyzer.fallback?.llm?.adapter ?? '',
			'enricher.contentanalyzer.fallback.llm.provider':
				cfg.enricher.contentanalyzer.fallback?.llm?.provider ?? '',
			'enricher.contentanalyzer.fallback.llm.model':
				cfg.enricher.contentanalyzer.fallback?.llm?.model ?? '',
			'enricher.contentanalyzer.fallback.llm.token':
				cfg.enricher.contentanalyzer.fallback?.llm?.token ?? '',
			'enricher.contentanalyzer.fallback.llm.temperature': Number(
				cfg.enricher.contentanalyzer.fallback?.llm?.temperature ?? 0
			),
			'enricher.contentanalyzer.fallback.llm.request_delay': Number(
				cfg.enricher.contentanalyzer.fallback?.llm?.request_delay ?? 0
			),
			'enricher.tagmatcher.timeout': Number(cfg.enricher.tagmatcher.timeout),
			'enricher.tagmatcher.reduce_target_words': Number(
				cfg.enricher.tagmatcher.reduce_target_words
			),
			'enricher.tagmatcher.chunk_size': Number(cfg.enricher.tagmatcher.chunk_size),
			'enricher.tagmatcher.hugot.model': cfg.enricher.tagmatcher.hugot.model,
			'enricher.tagmatcher.hugot.backend': cfg.enricher.tagmatcher.hugot.backend,
			'storage.consumption_dir': cfg.storage.consumption_dir,
			'storage.storage_dir': cfg.storage.storage_dir,
			'storage.migration_mode': cfg.storage.migration_mode,
			'storage.trash.retention_days': Number(cfg.storage.trash.retention_days),
			'database.host': cfg.database.host,
			'database.port': Number(cfg.database.port),
			'database.user': cfg.database.user,
			'database.database': cfg.database.database,
			'database.sslmode': cfg.database.sslmode,
			'backup.enabled': cfg.backup.enabled,
			'backup.path': cfg.backup.path,
			'backup.schedules': cfg.backup.schedules ?? [],
			'app.log_level': cfg.app.log_level,
			'app.logging.max_size': Number(cfg.app.logging.max_size),
			'app.logging.max_backups': Number(cfg.app.logging.max_backups),
			'app.logging.max_age': Number(cfg.app.logging.max_age),
			'app.logging.compress': cfg.app.logging.compress
		};
	}

	async function save() {
		saving = true;
		try {
			const res = await api.config.update(bodyFromConfig());
			if (res && 'pending_tasks' in res && res.pending_tasks > 0) {
				pendingTasks = res.pending_tasks;
				toastStore.success(res.message || 'Settings saved. Downloads in progress…');
				startPolling();
			} else {
				toastStore.success('Settings saved.');
			}
			if (res && 'missing_tools' in res) {
				missingTools = res.missing_tools;
			}
			const loaded = await api.config.get();
			if (loaded) {
				cfg = loaded;
				ensureFallbackBlock();
				syncMimeCheckboxes();
				snapshotState();
			}
		} catch (e) {
			toastStore.error(e.message);
		} finally {
			saving = false;
		}
	}

	async function fetchUsers({ limit, offset }) {
		return await api.users.list(limit, offset);
	}

	function openNewUser() {
		editingUser = null;
		formUsername = '';
		formPassword = '';
		formRole = 'viewer';
		userError = '';
		showUserModal = true;
	}

	function openEditUser(userId, userName, userRole) {
		editingUser = { id: userId, username: userName };
		formUsername = userName;
		formPassword = '';
		formRole = userRole || 'viewer';
		userError = '';
		showUserModal = true;
	}

	function handleUserPageClick(e) {
		const editBtn = e.target.closest('[data-edit-user]');
		if (editBtn) {
			const id = parseInt(editBtn.getAttribute('data-edit-user'));
			const name = editBtn.getAttribute('data-user-name');
			const role = editBtn.getAttribute('data-user-role') || 'viewer';
			openEditUser(id, name, role);
			return;
		}
		const deleteBtn = e.target.closest('[data-delete-user]');
		if (deleteBtn) {
			const id = parseInt(deleteBtn.getAttribute('data-delete-user'));
			const name = deleteBtn.getAttribute('data-user-name');
			handleDeleteUser(id, name);
		}
	}

	async function saveUser() {
		userError = '';
		const username = formUsername.trim();
		if (!username) {
			userError = 'Username is required';
			return;
		}
		const password = formPassword.trim();

		savingUser = true;
		let result;
		if (editingUser) {
			const body = { username, role: formRole };
			if (password) body.password = password;
			result = await api.users.update(editingUser.id, body);
		} else {
			if (!password) {
				savingUser = false;
				userError = 'Password is required';
				return;
			}
			result = await api.users.create({ username, password, role: formRole });
		}
		savingUser = false;

		if (result.ok) {
			showUserModal = false;
			refreshKey++;
		} else if (result.status === 409) {
			userError = 'Username already exists';
		} else {
			toastStore.error('Failed to save user');
		}
	}

	async function handleDeleteUser(userId, userName) {
		const ok = await confirmStore.confirm({
			title: 'Delete user',
			message: `Delete user "${userName}"? This action cannot be undone.`,
			danger: true
		});
		if (!ok) return;
		await api.users.delete(userId);
		refreshKey++;
	}
</script>

{#if authStore.authEnabled() && !authStore.isAdmin()}
	<p class="text-parchment-500">You do not have permission to view this page.</p>
{:else if !cfg}
	<div class="text-parchment-500">Loading settings…</div>
{:else}
	<div class="mx-auto max-w-3xl space-y-6">
		<div class="flex items-center justify-between">
			<h1 class="text-2xl font-bold text-balance text-parchment-200">Settings</h1>
			{#if pendingTasks > 0}
				<div class="flex items-center gap-2 text-sm text-gold-500" aria-live="polite">
					<div
						aria-hidden="true"
						class="h-4 w-4 animate-spin rounded-full border-2 border-clay-800 border-t-gold-500 motion-reduce:animate-none"
					></div>
					{pendingTasks} task(s) pending
				</div>
			{/if}
		</div>

		<div class="flex gap-1 border-b border-clay-800" role="tablist" aria-label="Settings sections">
			<button
				type="button"
				role="tab"
				id="tab-configuration"
				aria-selected={activeTab === 'Configuration'}
				aria-controls="panel-configuration"
				tabindex={activeTab === 'Configuration' ? 0 : -1}
				onclick={() => switchTab('Configuration')}
				onkeydown={handleTabKeydown}
				class="rounded-t-lg px-4 py-2 text-sm font-medium transition-colors focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none {activeTab ===
				'Configuration'
					? 'border-b-2 border-gold-500 text-gold-500'
					: 'text-parchment-400 hover:text-parchment-200'}"
			>
				Configuration
			</button>
			<button
				type="button"
				role="tab"
				id="tab-users"
				aria-selected={activeTab === 'Users'}
				aria-controls="panel-users"
				tabindex={activeTab === 'Users' ? 0 : -1}
				onclick={() => switchTab('Users')}
				onkeydown={handleTabKeydown}
				class="rounded-t-lg px-4 py-2 text-sm font-medium transition-colors focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none {activeTab ===
				'Users'
					? 'border-b-2 border-gold-500 text-gold-500'
					: 'text-parchment-400 hover:text-parchment-200'}"
			>
				Users
			</button>
		</div>

		{#if activeTab === 'Configuration'}
			<div role="tabpanel" id="panel-configuration" aria-labelledby="tab-configuration">
				{#if missingTools?.find((t) => t.engine === 'curl')}
					<div
						class="rounded-lg border border-terracotta-600 bg-terracotta-500/10 p-3 text-sm text-terracotta-500"
					>
						<p class="font-medium">“curl” not installed (required for downloads)</p>
						<p class="mt-1 text-parchment-400">
							Model and language file downloads will fail without curl.
						</p>
						{#each Object.entries(hintsForEngine('curl')) as [system, cmd], i (i)}
							<pre class="mt-1 overflow-x-auto text-xs text-parchment-300">{system}: {cmd}</pre>
						{/each}
					</div>
				{/if}

				<section class="mb-3 rounded-xl border border-clay-800 bg-clay-900 p-5">
					<h2 class="mb-4 text-lg font-semibold text-parchment-200">Server</h2>
					<div class="grid gap-4 sm:grid-cols-2">
						<div>
							<label for="server-host" class="mb-1 block text-sm font-medium text-parchment-200"
								>Host</label
							>
							<input
								id="server-host"
								name="server-host"
								autocomplete="off"
								type="text"
								bind:value={cfg.server.host}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							/>
						</div>
						<div>
							<label for="server-port" class="mb-1 block text-sm font-medium text-parchment-200"
								>Port</label
							>
							<input
								id="server-port"
								name="server-port"
								autocomplete="off"
								type="number"
								inputmode="numeric"
								min="1"
								max="65535"
								bind:value={cfg.server.port}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							/>
						</div>
						<div>
							<label
								for="server-max-upload"
								class="mb-1 block text-sm font-medium text-parchment-200"
								>Max upload size (MB)</label
							>
							<input
								id="server-max-upload"
								name="server-max-upload"
								autocomplete="off"
								type="number"
								inputmode="numeric"
								min="1"
								bind:value={cfg.server.max_upload_size}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							/>
						</div>
						<div>
							<label
								for="server-max-download-files"
								class="mb-1 block text-sm font-medium text-parchment-200">Max download files</label
							>
							<input
								id="server-max-download-files"
								name="server-max-download-files"
								autocomplete="off"
								type="number"
								inputmode="numeric"
								min="1"
								bind:value={cfg.server.max_download_files}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							/>
						</div>
						<div>
							<label
								for="server-max-download-size"
								class="mb-1 block text-sm font-medium text-parchment-200"
								>Max download size (MB)</label
							>
							<input
								id="server-max-download-size"
								name="server-max-download-size"
								autocomplete="off"
								type="number"
								inputmode="numeric"
								min="0"
								bind:value={cfg.server.max_download_size_mb}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							/>
						</div>
						<div>
							<label
								for="max-concurrent-batches"
								class="mb-1 block text-sm font-medium text-parchment-200"
								>Max concurrent batches</label
							>
							<input
								id="max-concurrent-batches"
								name="max-concurrent-batches"
								autocomplete="off"
								type="number"
								inputmode="numeric"
								min="1"
								bind:value={cfg.server.max_concurrent_batches}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							/>
						</div>
						<div>
							<label
								for="server-max-batch-delete"
								class="mb-1 block text-sm font-medium text-parchment-200">Max batch delete</label
							>
							<input
								id="server-max-batch-delete"
								name="server-max-batch-delete"
								autocomplete="off"
								type="number"
								inputmode="numeric"
								min="1"
								bind:value={cfg.server.max_batch_delete}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							/>
						</div>
					</div>
					<div class="mt-4">
						<label
							for="server-auth-enabled"
							class="mb-1 block text-sm font-medium text-parchment-200"
							>Authentication enabled</label
						>
						<div class="flex items-center gap-2">
							<input
								id="server-auth-enabled"
								name="server-auth-enabled"
								autocomplete="off"
								type="checkbox"
								bind:checked={cfg.server.auth_enabled}
								class="h-5 w-5 rounded border-clay-800 bg-clay-950 text-gold-500 focus:ring-gold-500"
							/>
							<!-- Status text deliberately outside the label hit target: making it
								clickable would toggle the control. -->
							<span class="text-sm text-parchment-400">
								{cfg.server.auth_enabled ? 'Enabled' : 'Disabled'}
							</span>
						</div>
					</div>
				</section>

				<section class="mb-3 rounded-xl border border-clay-800 bg-clay-900 p-5">
					<h2 class="mb-4 text-lg font-semibold text-parchment-200">Storage</h2>
					<div class="grid gap-4 sm:grid-cols-2">
						<div>
							<label
								for="consumption-dir"
								class="mb-1 block text-sm font-medium text-parchment-200"
							>
								Consumption directory (inbox)
							</label>
							<input
								id="consumption-dir"
								name="consumption-dir"
								autocomplete="off"
								type="text"
								bind:value={cfg.storage.consumption_dir}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							/>
						</div>
						<div>
							<label for="storage-dir" class="mb-1 block text-sm font-medium text-parchment-200">
								Storage directory
							</label>
							<input
								id="storage-dir"
								name="storage-dir"
								autocomplete="off"
								type="text"
								bind:value={cfg.storage.storage_dir}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							/>
						</div>
					</div>
					<div class="mt-4">
						<label for="migration-mode" class="mb-1 block text-sm font-medium text-parchment-200"
							>Migration mode</label
						>
						<select
							id="migration-mode"
							name="migration-mode"
							bind:value={cfg.storage.migration_mode}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
						>
							<option value="copy">Copy, then delete — safer, needs extra disk space</option>
							<option value="move">Move directly — faster, risk of data loss if interrupted</option>
						</select>
						{#if cfg.storage.migration_mode === 'move'}
							<p class="mt-1 text-xs text-terracotta-500">
								Files are renamed into place. If the process dies mid-migration, files can be left
								behind in the old location. Copy mode is recommended.
							</p>
						{:else}
							<p class="mt-1 text-xs text-parchment-500">
								Used when the storage or consumption directory changes.
							</p>
						{/if}
					</div>
				</section>

				<section class="mb-3 rounded-xl border border-clay-800 bg-clay-900 p-5">
					<h2 class="mb-4 text-lg font-semibold text-parchment-200">Trash (Soft Delete)</h2>
					<div class="grid gap-4 sm:grid-cols-2">
						<div>
							<label
								for="trash-retention-days"
								class="mb-1 block text-sm font-medium text-parchment-200"
							>
								Retention period (days)
							</label>
							<input
								id="trash-retention-days"
								name="trash-retention-days"
								autocomplete="off"
								type="number"
								inputmode="numeric"
								min="1"
								bind:value={cfg.storage.trash.retention_days}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							/>
							<p class="mt-1 text-xs text-parchment-500">
								Soft-deleted documents are permanently purged after this many days.
							</p>
						</div>
					</div>
				</section>

				<section class="mb-3 rounded-xl border border-clay-800 bg-clay-900 p-5">
					<h2 class="mb-4 text-lg font-semibold text-parchment-200">Database</h2>
					<div class="grid gap-4 sm:grid-cols-2">
						<div>
							<label for="db-host" class="mb-1 block text-sm font-medium text-parchment-200">
								Database host
							</label>
							<input
								id="db-host"
								name="db-host"
								autocomplete="off"
								type="text"
								bind:value={cfg.database.host}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							/>
						</div>
						<div>
							<label for="db-port" class="mb-1 block text-sm font-medium text-parchment-200">
								Database port
							</label>
							<input
								id="db-port"
								name="db-port"
								autocomplete="off"
								type="number"
								inputmode="numeric"
								min="1"
								max="65535"
								bind:value={cfg.database.port}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							/>
						</div>
						<div>
							<label for="db-user" class="mb-1 block text-sm font-medium text-parchment-200">
								Database user
							</label>
							<input
								id="db-user"
								name="db-user"
								autocomplete="off"
								type="text"
								bind:value={cfg.database.user}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							/>
						</div>
						<div>
							<label for="db-name" class="mb-1 block text-sm font-medium text-parchment-200">
								Database name
							</label>
							<input
								id="db-name"
								name="db-name"
								autocomplete="off"
								type="text"
								bind:value={cfg.database.database}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							/>
						</div>
						<div>
							<label for="db-sslmode" class="mb-1 block text-sm font-medium text-parchment-200">
								SSL mode
							</label>
							<select
								id="db-sslmode"
								name="db-sslmode"
								bind:value={cfg.database.sslmode}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							>
								<option value="disable">disable</option>
								<option value="allow">allow</option>
								<option value="prefer">prefer</option>
								<option value="require">require</option>
								<option value="verify-ca">verify-ca</option>
								<option value="verify-full">verify-full</option>
							</select>
						</div>
					</div>
				</section>

				<section class="mb-3 rounded-xl border border-clay-800 bg-clay-900 p-5">
					<h2 class="mb-4 text-lg font-semibold text-parchment-200">OCR</h2>
					<div class="grid gap-4 sm:grid-cols-2">
						<div>
							<label for="ocr-engine" class="mb-1 block text-sm font-medium text-parchment-200"
								>Engine</label
							>
							<select
								id="ocr-engine"
								name="ocr-engine"
								bind:value={cfg.consumer.ocr.engine}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							>
								{#each cfg.available_engines.ocr as opt (opt.value)}
									<option value={opt.value}>{opt.label}</option>
								{/each}
							</select>
						</div>
						<div>
							<label for="ocr-timeout" class="mb-1 block text-sm font-medium text-parchment-200"
								>Timeout (s)</label
							>
							<input
								id="ocr-timeout"
								name="ocr-timeout"
								autocomplete="off"
								type="number"
								inputmode="numeric"
								min="0"
								bind:value={cfg.consumer.ocr.timeout}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							/>
						</div>
						<div>
							<label for="ocr-workers" class="mb-1 block text-sm font-medium text-parchment-200"
								>Workers</label
							>
							<input
								id="ocr-workers"
								name="ocr-workers"
								autocomplete="off"
								type="number"
								inputmode="numeric"
								min="0"
								bind:value={cfg.consumer.ocr.ocr_workers}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							/>
							<p class="mt-1 text-xs text-parchment-500">0 = auto (CPU count)</p>
						</div>
					</div>

					{#if toolStatus?.find((t) => t.category === 'ocr' && !t.available)}
						<div
							class="mt-4 rounded-lg border border-terracotta-600 bg-terracotta-500/10 p-3 text-sm text-terracotta-500"
						>
							<p class="font-medium">“{cfg.consumer.ocr.engine}” is not installed</p>
							<p class="mt-1 text-parchment-400">
								Documents won't process until it is available. Install it, e.g.:
							</p>
							{#each Object.entries(hintsForEngine(cfg.consumer.ocr.engine)) as [system, cmd], i (i)}
								<pre class="mt-1 text-xs text-parchment-300">{system}: {cmd}</pre>
							{/each}
						</div>
					{/if}
					{#if cfg.consumer.ocr.engine === 'ocrmypdf'}
						{@const ocrTool = toolStatus?.find((t) => t.engine === 'ocrmypdf')}
						{#if ocrTool?.lang_hints?.length}
							<div
								class="mt-4 rounded-lg border border-lapis-500/30 bg-lapis-500/10 p-3 text-sm text-parchment-200"
							>
								<p class="font-medium">Tesseract language packs required</p>
								<p class="mt-1 text-parchment-300">
									Install the packs for your configured languages ({ocrTool.languages.join(', ')}):
								</p>
								{#each Object.entries(ocrTool.lang_hints[0].install_hints) as [system, cmd], i (i)}
									<pre class="mt-1 text-xs text-parchment-300">{system}: {cmd}</pre>
								{/each}
							</div>
						{/if}
						{#if ocrTool?.companions?.length}
							<div class="mt-4 space-y-2 text-sm">
								{#each ocrTool.companions as c (c.command)}
									{#if !c.available && c.required}
										<div
											class="rounded-lg border border-terracotta-600 bg-terracotta-500/10 p-3 text-terracotta-500"
										>
											<p class="font-medium">“{c.command}” not installed (required)</p>
											<p class="mt-1 text-parchment-400">{c.purpose}</p>
											{#each Object.entries(c.install_hints) as [system, cmd], i (i)}
												<pre class="mt-1 text-xs text-parchment-300">{system}: {cmd}</pre>
											{/each}
										</div>
									{:else if !c.available}
										<div
											class="rounded-lg border border-lapis-500/30 bg-lapis-500/10 p-3 text-parchment-300"
										>
											<p class="font-medium text-parchment-200">
												“{c.command}” not installed (optional)
											</p>
											<p class="mt-1">{c.purpose}. ocrmypdf will skip this feature without it.</p>
											{#each Object.entries(c.install_hints) as [system, cmd], i (i)}
												<pre class="mt-1 text-xs text-parchment-300">{system}: {cmd}</pre>
											{/each}
										</div>
									{/if}
								{/each}
							</div>
						{/if}
					{/if}

					<div class="mt-4 grid gap-4 sm:grid-cols-2">
						<div class="sm:col-span-2">
							<label for="ocr-data-dir" class="mb-1 block text-sm font-medium text-parchment-200"
								>Data directory</label
							>
							<input
								id="ocr-data-dir"
								name="ocr-data-dir"
								autocomplete="off"
								type="text"
								bind:value={cfg.consumer.ocr.data_dir}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							/>
						</div>
					</div>
					<div class="mt-4">
						<label for="ocr-lang-0" class="mb-3 block text-sm font-medium text-parchment-200"
							>Languages</label
						>
						{#each cfg.consumer.ocr.languages as lang, i (i)}
							<div class="mb-3 flex gap-2">
								<input
									id="ocr-lang-{i}"
									name="ocr-lang-{i}"
									autocomplete="off"
									spellcheck="false"
									type="text"
									value={lang}
									oninput={(e) => updateLanguage(i, e.currentTarget.value)}
									placeholder="eng"
									class="flex-1 rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 placeholder-parchment-500 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
								/>
								{#if cfg.consumer.ocr.languages.length > 1}
									<button
										type="button"
										onclick={() => removeLanguage(i)}
										class="rounded-lg border border-clay-800 px-3 text-sm text-parchment-400 hover:bg-clay-800 hover:text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
									>
										Remove
									</button>
								{/if}
							</div>
						{/each}
						<button
							type="button"
							onclick={addLanguage}
							class="text-sm text-gold-500 hover:text-gold-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
						>
							+ Add language
						</button>
					</div>
				</section>

				<section class="mb-3 rounded-xl border border-clay-800 bg-clay-900 p-5">
					<h2 class="mb-4 text-lg font-semibold text-parchment-200">Consumer</h2>
					<div class="grid gap-4 sm:grid-cols-2">
						<div>
							<label
								for="consumer-workers"
								class="mb-1 block text-sm font-medium text-parchment-200">Workers</label
							>
							<input
								id="consumer-workers"
								name="consumer-workers"
								autocomplete="off"
								type="number"
								inputmode="numeric"
								min="1"
								bind:value={cfg.consumer.workers}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							/>
						</div>
						<div>
							<label
								for="consumer-max-files-per-batch"
								class="mb-1 block text-sm font-medium text-parchment-200">Max files per batch</label
							>
							<input
								id="consumer-max-files-per-batch"
								name="consumer-max-files-per-batch"
								autocomplete="off"
								type="number"
								inputmode="numeric"
								min="0"
								bind:value={cfg.consumer.max_files_per_batch}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							/>
						</div>
					</div>

					<div class="mt-4">
						<h3 class="mb-3 text-sm font-medium text-parchment-200">Supported file types</h3>
						<div class="flex flex-wrap gap-3">
							{#each mimeTypeOptions as opt (opt.mime_type)}
								<label class="flex items-center gap-1.5 text-sm text-parchment-300">
									<input
										type="checkbox"
										bind:checked={opt.checked}
										disabled={opt.required}
										class="rounded border-clay-800 bg-clay-950 accent-gold-500 disabled:opacity-50"
									/>
									{opt.label}
								</label>
							{/each}
						</div>
					</div>

					<div class="mt-4 border-t border-clay-800 pt-4">
						<h3 class="mb-3 text-sm font-medium text-parchment-200">DOCX/ODT Converter</h3>
						<p class="mb-3 text-xs text-parchment-400">
							Converts DOCX and ODT files to PDF via LibreOffice before text extraction.
						</p>
						<div class="grid gap-4 sm:grid-cols-3">
							<div class="flex items-center gap-2">
								<input
									id="converter-enabled"
									name="converter-enabled"
									autocomplete="off"
									type="checkbox"
									bind:checked={cfg.consumer.converter.enabled}
									class="rounded border-clay-800 bg-clay-950 accent-gold-500"
								/>
								<label for="converter-enabled" class="text-sm font-medium text-parchment-200"
									>Enabled</label
								>
							</div>
							<div>
								<label
									for="converter-binary"
									class="mb-1 block text-sm font-medium text-parchment-200">Binary path</label
								>
								<input
									id="converter-binary"
									name="converter-binary"
									type="text"
									bind:value={cfg.consumer.converter.binary}
									autocomplete="off"
									class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
								/>
							</div>
							<div>
								<label
									for="converter-timeout"
									class="mb-1 block text-sm font-medium text-parchment-200">Timeout (s)</label
								>
								<input
									id="converter-timeout"
									name="converter-timeout"
									type="number"
									inputmode="numeric"
									min="1"
									bind:value={cfg.consumer.converter.timeout}
									autocomplete="off"
									class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
								/>
							</div>
						</div>
					</div>
				</section>

				<section class="mb-3 rounded-xl border border-clay-800 bg-clay-900 p-5">
					<h2 class="mb-4 text-lg font-semibold text-parchment-200">Polling</h2>
					<div class="grid gap-4 sm:grid-cols-2">
						<div>
							<label for="polling-enabled" class="mb-1 block text-sm font-medium text-parchment-200"
								>Enabled</label
							>
							<div class="flex items-center gap-2">
								<input
									id="polling-enabled"
									name="polling-enabled"
									autocomplete="off"
									type="checkbox"
									bind:checked={cfg.consumer.polling.enabled}
									class="h-5 w-5 rounded border-clay-800 bg-clay-950 text-gold-500 focus:ring-gold-500"
								/>
								<!-- Runtime process states read Active/Inactive; configuration toggles use
									Enabled/Disabled — intentional, don't standardize. -->
								<span class="text-sm text-parchment-400">
									{cfg.consumer.polling.enabled ? 'Active' : 'Inactive'}
								</span>
							</div>
						</div>
						<div>
							<label
								for="polling-interval"
								class="mb-1 block text-sm font-medium text-parchment-200">Interval (minutes)</label
							>
							<input
								id="polling-interval"
								name="polling-interval"
								autocomplete="off"
								type="number"
								inputmode="numeric"
								min="1"
								bind:value={cfg.consumer.polling.interval}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							/>
						</div>
					</div>

					<div class="mt-4">
						<!-- Not a heading/legend on purpose: it labels a repeatable input group, and a
							heading would add an unrequested entry to the accessibility outline. -->
						<span class="mb-3 block text-sm font-medium text-parchment-200"
							>Active windows (optional)</span
						>
						<!-- Deliberately text + pattern, not type="time": the end-of-day sentinel
							24:00 can't be entered in a native time input, and a picker would change
							the interaction for existing users. -->
						{#each cfg.consumer.polling.windows as w, i (i)}
							<div class="mb-3 flex items-center gap-2">
								<input
									type="text"
									name="polling-window-start-{i}"
									bind:value={w.start}
									aria-label="Start time"
									pattern="([01][0-9]|2[0-3]):[0-5][0-9]"
									placeholder="HH:MM…"
									minlength="5"
									maxlength="5"
									class="w-36 rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
								/>
								<span class="text-parchment-400">to</span>
								<input
									type="text"
									name="polling-window-end-{i}"
									bind:value={w.end}
									aria-label="End time"
									pattern="([01][0-9]|2[0-3]):[0-5][0-9]|24:00"
									placeholder="HH:MM…"
									minlength="5"
									maxlength="5"
									class="w-36 rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
								/>
								<button
									type="button"
									onclick={() => removeWindow(i)}
									class="rounded-lg border border-clay-800 px-3 text-sm text-parchment-400 hover:bg-clay-800 hover:text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
								>
									Remove
								</button>
							</div>
						{/each}
						<button
							type="button"
							onclick={addWindow}
							class="text-sm text-gold-500 hover:text-gold-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
						>
							+ Add window
						</button>
					</div>
				</section>

				<section class="mb-3 rounded-xl border border-clay-800 bg-clay-900 p-5">
					<h2 class="mb-4 text-lg font-semibold text-parchment-200">Reclaim</h2>
					<div class="grid gap-4 sm:grid-cols-2">
						<div>
							<label for="reclaim-enabled" class="mb-1 block text-sm font-medium text-parchment-200"
								>Auto-resume interrupted batches</label
							>
							<div class="flex items-center gap-2">
								<input
									id="reclaim-enabled"
									name="reclaim-enabled"
									autocomplete="off"
									type="checkbox"
									bind:checked={cfg.consumer.reclaim.enabled}
									class="h-5 w-5 rounded border-clay-800 bg-clay-950 text-gold-500 focus:ring-gold-500"
								/>
								<span class="text-sm text-parchment-400">
									{cfg.consumer.reclaim.enabled ? 'Active' : 'Inactive'}
								</span>
							</div>
						</div>
						<div>
							<label
								for="reclaim-max-retries"
								class="mb-1 block text-sm font-medium text-parchment-200"
								>Max retries per task</label
							>
							<input
								id="reclaim-max-retries"
								name="reclaim-max-retries"
								autocomplete="off"
								type="number"
								inputmode="numeric"
								min="1"
								max="10"
								class="w-24 rounded-lg border border-clay-700 bg-clay-950 px-3 py-2 text-parchment-200 focus:border-gold-500 focus-visible:ring-1 focus-visible:ring-gold-500 focus-visible:outline-none"
								bind:value={cfg.consumer.reclaim.max_retries}
							/>
						</div>
						<div>
							<label
								for="reclaim-stale-task-after"
								class="mb-1 block text-sm font-medium text-parchment-200"
								>Stale task after (s)</label
							>
							<input
								id="reclaim-stale-task-after"
								name="reclaim-stale-task-after"
								autocomplete="off"
								type="number"
								inputmode="numeric"
								min="60"
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
								bind:value={cfg.consumer.reclaim.stale_task_after}
							/>
						</div>
					</div>
				</section>

				<section class="mb-3 rounded-xl border border-clay-800 bg-clay-900 p-5">
					<h2 class="mb-4 text-lg font-semibold text-parchment-200">Text extractor</h2>
					<div class="grid gap-4 sm:grid-cols-2">
						<div>
							<label
								for="text-extractor-engine"
								class="mb-1 block text-sm font-medium text-parchment-200">Engine</label
							>
							<select
								id="text-extractor-engine"
								name="text-extractor-engine"
								bind:value={cfg.consumer.textextractor.engine}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							>
								{#each cfg.available_engines.text_extractor as opt (opt.value)}
									<option value={opt.value}>{opt.label}</option>
								{/each}
							</select>
						</div>
						<div>
							<label
								for="text-extractor-timeout"
								class="mb-1 block text-sm font-medium text-parchment-200">Timeout (s)</label
							>
							<input
								id="text-extractor-timeout"
								name="text-extractor-timeout"
								autocomplete="off"
								type="number"
								inputmode="numeric"
								min="0"
								bind:value={cfg.consumer.textextractor.timeout}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							/>
						</div>
					</div>
					{#if toolStatus?.find((t) => t.category === 'textextractor' && !t.available)}
						<div
							class="mt-4 rounded-lg border border-terracotta-600 bg-terracotta-500/10 p-3 text-sm text-terracotta-500"
						>
							<p class="font-medium">“{cfg.consumer.textextractor.engine}” is not installed</p>
							<p class="mt-1 text-parchment-400">
								Documents won't process until it is available. Install it, e.g.:
							</p>
							{#each Object.entries(hintsForEngine(cfg.consumer.textextractor.engine)) as [system, cmd], i (i)}
								<pre class="mt-1 text-xs text-parchment-300">{system}: {cmd}</pre>
							{/each}
						</div>
					{/if}
				</section>

				<section class="mb-3 rounded-xl border border-clay-800 bg-clay-900 p-5">
					<h2 class="mb-4 text-lg font-semibold text-parchment-200">PDF optimizer</h2>
					<div class="grid gap-4 sm:grid-cols-2">
						<div>
							<label for="pdf-engine" class="mb-1 block text-sm font-medium text-parchment-200"
								>Engine</label
							>
							<select
								id="pdf-engine"
								name="pdf-engine"
								bind:value={cfg.consumer.pdfoptimizer.engine}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							>
								{#each cfg.available_engines.pdf_optimizer as opt (opt.value)}
									<option value={opt.value}>{opt.label}</option>
								{/each}
							</select>
						</div>
						<div>
							<label for="pdf-fallback" class="mb-1 block text-sm font-medium text-parchment-200"
								>Fallback (optional)</label
							>
							<input
								id="pdf-fallback"
								name="pdf-fallback"
								autocomplete="off"
								type="text"
								bind:value={cfg.consumer.pdfoptimizer.fallback}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 placeholder-parchment-500 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							/>
						</div>
					</div>
					{#if toolStatus?.find((t) => t.category === 'pdfoptimizer' && !t.available)}
						<div
							class="mt-4 rounded-lg border border-terracotta-600 bg-terracotta-500/10 p-3 text-sm text-terracotta-500"
						>
							<p class="font-medium">“{cfg.consumer.pdfoptimizer.engine}” is not installed</p>
							<p class="mt-1 text-parchment-400">
								Documents won't process until it is available. Install it, e.g.:
							</p>
							{#each Object.entries(hintsForEngine(cfg.consumer.pdfoptimizer.engine)) as [system, cmd], i (i)}
								<pre class="mt-1 text-xs text-parchment-300">{system}: {cmd}</pre>
							{/each}
						</div>
					{/if}
					<div class="mt-4 grid gap-4 sm:grid-cols-2">
						<div class="sm:col-span-2">
							<label for="pdf-timeout" class="mb-1 block text-sm font-medium text-parchment-200"
								>Timeout (s)</label
							>
							<input
								id="pdf-timeout"
								name="pdf-timeout"
								autocomplete="off"
								type="number"
								inputmode="numeric"
								min="0"
								bind:value={cfg.consumer.pdfoptimizer.timeout}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							/>
						</div>
					</div>
				</section>

				<section class="mb-3 rounded-xl border border-clay-800 bg-clay-900 p-5">
					<h2 class="mb-4 text-lg font-semibold text-parchment-200">Enricher</h2>
					<div class="grid gap-4 sm:grid-cols-2">
						<div>
							<label
								for="enricher-workers"
								class="mb-1 block text-sm font-medium text-parchment-200">Workers</label
							>
							<input
								id="enricher-workers"
								name="enricher-workers"
								autocomplete="off"
								type="number"
								inputmode="numeric"
								min="1"
								bind:value={cfg.enricher.workers}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							/>
						</div>
					</div>
				</section>

				<section class="mb-3 rounded-xl border border-clay-800 bg-clay-900 p-5">
					<div class="mb-4 flex items-center justify-between">
						<h2 class="text-lg font-semibold text-parchment-200">Content analyzer (LLM)</h2>
						<label class="relative inline-flex cursor-pointer items-center">
							<input
								type="checkbox"
								aria-label="Enable content analyzer"
								bind:checked={cfg.enricher.contentanalyzer.enabled}
								class="peer sr-only"
							/>
							<div
								class="h-6 w-11 rounded-full border border-clay-700 bg-clay-800 peer-checked:border-gold-500 peer-checked:bg-gold-600 peer-focus-visible:ring-2 peer-focus-visible:ring-gold-500 peer-focus-visible:ring-offset-2 after:absolute after:top-0.5 after:left-0.5 after:h-5 after:w-5 after:rounded-full after:bg-parchment-400 after:transition-transform peer-checked:after:translate-x-full peer-checked:after:bg-white"
							></div>
							<span class="ml-2 text-sm text-parchment-300">Enabled</span>
						</label>
					</div>
					{#if cfg.enricher.contentanalyzer.enabled}
						<div class="grid gap-4 sm:grid-cols-2">
							<div>
								<label for="llm-adapter" class="mb-1 block text-sm font-medium text-parchment-200">
									Adapter
								</label>
								<select
									id="llm-adapter"
									name="llm-adapter"
									bind:value={cfg.enricher.contentanalyzer.llm.adapter}
									class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
								>
									{#each Object.keys(llmModels.adapters) as adapter (adapter)}
										<option value={adapter}>{adapter}</option>
									{/each}
								</select>
							</div>

							<div>
								<label for="llm-provider" class="mb-1 block text-sm font-medium text-parchment-200">
									Provider
								</label>
								<select
									id="llm-provider"
									name="llm-provider"
									bind:value={cfg.enricher.contentanalyzer.llm.provider}
									class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
								>
									{#each selectedAdapterProviders as provider (provider)}
										<option value={provider}>{provider}</option>
									{/each}
								</select>
							</div>

							<div>
								<label for="llm-model" class="mb-1 block text-sm font-medium text-parchment-200">
									Model
								</label>
								<select
									id="llm-model"
									name="llm-model"
									bind:value={cfg.enricher.contentanalyzer.llm.model}
									class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
								>
									{#each selectedProviderModels as m (m.id)}
										<option value={m.id}>{m.id}</option>
									{/each}
								</select>
							</div>

							<div>
								<label for="llm-token" class="mb-1 block text-sm font-medium text-parchment-200">
									Token
								</label>
								<div class="flex gap-2">
									<input
										id="llm-token"
										type={showToken ? 'text' : 'password'}
										bind:value={cfg.enricher.contentanalyzer.llm.token}
										placeholder="sk-…"
										autocomplete="off"
										class="flex-1 rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 placeholder-parchment-500 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
									/>
									<button
										type="button"
										onclick={() => (showToken = !showToken)}
										class="rounded-lg border border-clay-800 px-3 text-sm text-parchment-400 hover:bg-clay-800 hover:text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
									>
										{showToken ? 'Hide' : 'Show'}
									</button>
								</div>
							</div>
						</div>

						<div class="mt-4 grid gap-4 sm:grid-cols-2">
							<div>
								<label
									for="llm-temperature"
									class="mb-1 block text-sm font-medium text-parchment-200"
								>
									Temperature
								</label>
								<input
									id="llm-temperature"
									name="llm-temperature"
									autocomplete="off"
									type="number"
									inputmode="numeric"
									min="0"
									max="2"
									step="0.1"
									bind:value={cfg.enricher.contentanalyzer.llm.temperature}
									class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
								/>
							</div>
							<div>
								<label
									for="content-analyzer-timeout"
									class="mb-1 block text-sm font-medium text-parchment-200"
								>
									Timeout (s)
								</label>
								<input
									id="content-analyzer-timeout"
									name="content-analyzer-timeout"
									autocomplete="off"
									type="number"
									inputmode="numeric"
									min="1"
									bind:value={cfg.enricher.contentanalyzer.timeout}
									class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
								/>
							</div>
							<div>
								<label
									for="content-analyzer-pause-credit"
									class="mb-1 block text-sm font-medium text-parchment-200"
								>
									Pause on credit error
								</label>
								<div class="mt-2 flex items-center gap-2">
									<input
										id="content-analyzer-pause-credit"
										name="content-analyzer-pause-credit"
										autocomplete="off"
										type="checkbox"
										bind:checked={cfg.enricher.contentanalyzer.pause_on_credit_error}
										class="h-5 w-5 rounded border-clay-800 bg-clay-950 text-gold-500 focus:ring-gold-500"
									/>
									<span class="text-sm text-parchment-400">
										{cfg.enricher.contentanalyzer.pause_on_credit_error ? 'Enabled' : 'Disabled'}
									</span>
								</div>
							</div>
							<div>
								<label
									for="llm-request-delay"
									class="mb-1 block text-sm font-medium text-parchment-200"
								>
									Request delay (s)
								</label>
								<input
									id="llm-request-delay"
									name="llm-request-delay"
									autocomplete="off"
									type="number"
									inputmode="numeric"
									min="0"
									max="60"
									step="0.1"
									bind:value={cfg.enricher.contentanalyzer.llm.request_delay}
									class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
								/>
								<p class="mt-1 text-xs text-parchment-500">
									Seconds to sleep after each LLM request; 0 = off, max 60. Use for rate-limited
									providers.
								</p>
							</div>
						</div>

						<div class="mt-4">
							<label
								for="content-analyzer-prompt-template"
								class="mb-1 block text-sm font-medium text-parchment-200"
							>
								Prompt template (advanced)
							</label>
							<p class="mb-3 text-xs text-parchment-500">
								Leave empty for default. Available placeholders: {`{{.DocTypePrompt}}`}, {`{{.TagsPrompt}}`},
								{`{{.PeoplePrompt}}`}, {`{{.Text}}`} (required)
							</p>
							<textarea
								id="content-analyzer-prompt-template"
								rows="8"
								bind:value={cfg.enricher.contentanalyzer.prompt_template}
								spellcheck="false"
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 font-mono text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							></textarea>
						</div>

						<div class="mt-6 rounded-lg border border-clay-800 bg-clay-950 p-4">
							<h3 class="mb-3 text-sm font-semibold text-parchment-200">Doc type refinement</h3>
							<div class="grid gap-4 sm:grid-cols-3">
								<div>
									<label
										for="doc-type-refinement-enabled"
										class="mb-1 block text-sm font-medium text-parchment-200">Enabled</label
									>
									<div class="mt-2 flex items-center gap-2">
										<input
											id="doc-type-refinement-enabled"
											name="doc-type-refinement-enabled"
											autocomplete="off"
											type="checkbox"
											bind:checked={cfg.enricher.contentanalyzer.doc_type_refinement.enabled}
											class="h-5 w-5 rounded border-clay-800 bg-clay-950 text-gold-500 focus:ring-gold-500"
										/>
										<span class="text-sm text-parchment-400">
											{cfg.enricher.contentanalyzer.doc_type_refinement.enabled
												? 'Active'
												: 'Inactive'}
										</span>
									</div>
								</div>
								<div>
									<label
										for="doc-type-refinement-head-words"
										class="mb-1 block text-sm font-medium text-parchment-200">Head words</label
									>
									<input
										id="doc-type-refinement-head-words"
										name="doc-type-refinement-head-words"
										autocomplete="off"
										type="number"
										inputmode="numeric"
										min="0"
										bind:value={cfg.enricher.contentanalyzer.doc_type_refinement.head_words}
										class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
									/>
								</div>
								<div>
									<label
										for="doc-type-refinement-tail-words"
										class="mb-1 block text-sm font-medium text-parchment-200">Tail words</label
									>
									<input
										id="doc-type-refinement-tail-words"
										name="doc-type-refinement-tail-words"
										autocomplete="off"
										type="number"
										inputmode="numeric"
										min="0"
										bind:value={cfg.enricher.contentanalyzer.doc_type_refinement.tail_words}
										class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
									/>
								</div>
							</div>
						</div>

						<div class="mt-6 rounded-lg border border-clay-800 bg-clay-950 p-4">
							<div class="mb-3 flex items-center justify-between">
								<h3 class="text-sm font-semibold text-parchment-200">Fallback LLM</h3>
								<label class="relative inline-flex cursor-pointer items-center">
									<input
										type="checkbox"
										aria-label="Enable fallback LLM"
										bind:checked={cfg.enricher.contentanalyzer.fallback.enabled}
										class="peer sr-only"
									/>
									<div
										class="h-6 w-11 rounded-full border border-clay-700 bg-clay-800 peer-checked:border-gold-500 peer-checked:bg-gold-600 peer-focus-visible:ring-2 peer-focus-visible:ring-gold-500 peer-focus-visible:ring-offset-2 after:absolute after:top-0.5 after:left-0.5 after:h-5 after:w-5 after:rounded-full after:bg-parchment-400 after:transition-transform peer-checked:after:translate-x-full peer-checked:after:bg-white"
									></div>
									<span class="ml-2 text-sm text-parchment-300">Enabled</span>
								</label>
							</div>
							{#if cfg.enricher.contentanalyzer.fallback.enabled}
								<div class="grid gap-4 sm:grid-cols-3">
									<div>
										<label
											for="llm-fallback-adapter"
											class="mb-1 block text-sm font-medium text-parchment-200"
										>
											Adapter
										</label>
										<select
											id="llm-fallback-adapter"
											name="llm-fallback-adapter"
											bind:value={cfg.enricher.contentanalyzer.fallback.llm.adapter}
											class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
										>
											{#each Object.keys(llmModels.adapters) as adapter (adapter)}
												<option value={adapter}>{adapter}</option>
											{/each}
										</select>
									</div>

									<div>
										<label
											for="llm-fallback-provider"
											class="mb-1 block text-sm font-medium text-parchment-200"
										>
											Provider
										</label>
										<select
											id="llm-fallback-provider"
											name="llm-fallback-provider"
											bind:value={cfg.enricher.contentanalyzer.fallback.llm.provider}
											class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
										>
											{#each fallbackAdapterProviders as provider (provider)}
												<option value={provider}>{provider}</option>
											{/each}
										</select>
									</div>

									<div>
										<label
											for="llm-fallback-model"
											class="mb-1 block text-sm font-medium text-parchment-200"
										>
											Model
										</label>
										<select
											id="llm-fallback-model"
											name="llm-fallback-model"
											bind:value={cfg.enricher.contentanalyzer.fallback.llm.model}
											class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
										>
											{#each fallbackProviderModels as m (m.id)}
												<option value={m.id}>{m.id}</option>
											{/each}
										</select>
									</div>
								</div>

								<div class="mt-4 grid gap-4 sm:grid-cols-3">
									<div>
										<label
											for="llm-fallback-token"
											class="mb-1 block text-sm font-medium text-parchment-200"
										>
											Token
										</label>
										<div class="flex gap-2">
											<input
												id="llm-fallback-token"
												type={showFallbackToken ? 'text' : 'password'}
												bind:value={cfg.enricher.contentanalyzer.fallback.llm.token}
												placeholder="sk-…"
												autocomplete="off"
												class="flex-1 rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 placeholder-parchment-500 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
											/>
											<button
												type="button"
												onclick={() => (showFallbackToken = !showFallbackToken)}
												class="rounded-lg border border-clay-800 px-3 text-sm text-parchment-400 hover:bg-clay-800 hover:text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
											>
												{showFallbackToken ? 'Hide' : 'Show'}
											</button>
										</div>
									</div>
									<div>
										<label
											for="llm-fallback-temperature"
											class="mb-1 block text-sm font-medium text-parchment-200"
										>
											Temperature
										</label>
										<input
											id="llm-fallback-temperature"
											name="llm-fallback-temperature"
											autocomplete="off"
											type="number"
											inputmode="numeric"
											min="0"
											max="2"
											step="0.1"
											bind:value={cfg.enricher.contentanalyzer.fallback.llm.temperature}
											class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
										/>
									</div>
									<div>
										<label
											for="llm-fallback-request-delay"
											class="mb-1 block text-sm font-medium text-parchment-200"
										>
											Request delay (s)
										</label>
										<input
											id="llm-fallback-request-delay"
											name="llm-fallback-request-delay"
											autocomplete="off"
											type="number"
											inputmode="numeric"
											min="0"
											max="60"
											step="0.1"
											bind:value={cfg.enricher.contentanalyzer.fallback.llm.request_delay}
											class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
										/>
										<p class="mt-1 text-xs text-parchment-500">
											Seconds to sleep after each fallback request; 0 = off, max 60.
										</p>
									</div>
								</div>
							{/if}
						</div>
					{/if}
				</section>

				<section class="mb-3 rounded-xl border border-clay-800 bg-clay-900 p-5">
					<h2 class="mb-4 text-lg font-semibold text-parchment-200">Tag matcher</h2>
					<div class="grid gap-4 sm:grid-cols-2">
						<div>
							<label
								for="tag-matcher-timeout"
								class="mb-1 block text-sm font-medium text-parchment-200">Timeout (s)</label
							>
							<input
								id="tag-matcher-timeout"
								name="tag-matcher-timeout"
								autocomplete="off"
								type="number"
								inputmode="numeric"
								min="0"
								bind:value={cfg.enricher.tagmatcher.timeout}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							/>
						</div>
						<div>
							<label
								for="tag-matcher-reduce-target"
								class="mb-1 block text-sm font-medium text-parchment-200">Reduce target words</label
							>
							<input
								id="tag-matcher-reduce-target"
								name="tag-matcher-reduce-target"
								autocomplete="off"
								type="number"
								inputmode="numeric"
								min="0"
								bind:value={cfg.enricher.tagmatcher.reduce_target_words}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							/>
						</div>
						<div>
							<label
								for="tag-matcher-chunk-size"
								class="mb-1 block text-sm font-medium text-parchment-200">Chunk size</label
							>
							<input
								id="tag-matcher-chunk-size"
								name="tag-matcher-chunk-size"
								autocomplete="off"
								type="number"
								inputmode="numeric"
								min="0"
								bind:value={cfg.enricher.tagmatcher.chunk_size}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							/>
						</div>
						<div>
							<label
								for="tag-matcher-hugot-model"
								class="mb-1 block text-sm font-medium text-parchment-200">Hugot model</label
							>
							<input
								id="tag-matcher-hugot-model"
								name="tag-matcher-hugot-model"
								autocomplete="off"
								type="text"
								bind:value={cfg.enricher.tagmatcher.hugot.model}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							/>
						</div>
						<div>
							<label
								for="tag-matcher-hugot-backend"
								class="mb-1 block text-sm font-medium text-parchment-200">Hugot backend</label
							>
							<select
								id="tag-matcher-hugot-backend"
								name="tag-matcher-hugot-backend"
								bind:value={cfg.enricher.tagmatcher.hugot.backend}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							>
								<option value="ort">ort</option>
								<option value="GO">GO</option>
							</select>
						</div>
					</div>
				</section>

				<section class="mb-3 rounded-xl border border-clay-800 bg-clay-900 p-5">
					<h2 class="mb-4 text-lg font-semibold text-parchment-200">Text reducer</h2>
					<div class="grid gap-4 sm:grid-cols-2">
						<div>
							<label
								for="text-reducer-engine"
								class="mb-1 block text-sm font-medium text-parchment-200">Engine</label
							>
							<select
								id="text-reducer-engine"
								name="text-reducer-engine"
								bind:value={cfg.enricher.textreducer.engine}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							>
								{#each cfg.available_engines.text_reducer as opt (opt.value)}
									<option value={opt.value}>{opt.label}</option>
								{/each}
							</select>
						</div>
						<div>
							<label
								for="text-reducer-timeout"
								class="mb-1 block text-sm font-medium text-parchment-200">Timeout (s)</label
							>
							<input
								id="text-reducer-timeout"
								name="text-reducer-timeout"
								autocomplete="off"
								type="number"
								inputmode="numeric"
								min="0"
								bind:value={cfg.enricher.textreducer.timeout}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							/>
						</div>
						<div class="sm:col-span-2">
							<label
								for="text-reducer-target-words"
								class="mb-1 block text-sm font-medium text-parchment-200">Target words</label
							>
							<input
								id="text-reducer-target-words"
								name="text-reducer-target-words"
								autocomplete="off"
								type="number"
								inputmode="numeric"
								min="1"
								bind:value={cfg.enricher.textreducer.target_words}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							/>
						</div>
					</div>
				</section>

				<section class="mb-3 rounded-xl border border-clay-800 bg-clay-900 p-5">
					<h2 class="mb-4 text-lg font-semibold text-parchment-200">Backup</h2>
					<div class="grid gap-4 sm:grid-cols-2">
						<div>
							<label for="backup-enabled" class="mb-1 block text-sm font-medium text-parchment-200">
								Enabled
							</label>
							<input
								id="backup-enabled"
								name="backup-enabled"
								autocomplete="off"
								type="checkbox"
								bind:checked={cfg.backup.enabled}
								class="mt-2 h-5 w-5 rounded border-clay-800 bg-clay-950 text-gold-500 focus:ring-gold-500"
							/>
						</div>
						<div>
							<label for="backup-path" class="mb-1 block text-sm font-medium text-parchment-200">
								Fallback output directory
							</label>
							<input
								id="backup-path"
								name="backup-path"
								autocomplete="off"
								type="text"
								bind:value={cfg.backup.path}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							/>
						</div>
					</div>

					<div class="mt-4">
						<span class="mb-3 block text-sm font-medium text-parchment-200">Schedules</span>
						{#each cfg.backup.schedules as s, i (i)}
							<div class="mb-3 rounded-lg border border-clay-800 bg-clay-950 p-3">
								<div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-6">
									<div>
										<label
											for="backup-schedule-{i}-mode"
											class="mb-1 block text-xs font-medium text-parchment-200">Mode</label
										>
										<select
											id="backup-schedule-{i}-mode"
											name="backup-schedule-{i}-mode"
											autocomplete="off"
											bind:value={s.mode}
											class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
										>
											<option value="full">Full (database + documents)</option>
											<option value="database">Database only</option>
											<option value="documents">Documents only</option>
										</select>
									</div>
									<div>
										<label
											for="backup-schedule-{i}-interval"
											class="mb-1 block text-xs font-medium text-parchment-200"
											>Interval (days)</label
										>
										<input
											id="backup-schedule-{i}-interval"
											name="backup-schedule-{i}-interval"
											autocomplete="off"
											type="number"
											inputmode="numeric"
											min="0.1"
											step="0.1"
											bind:value={s.interval}
											class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
										/>
									</div>
									<div>
										<label
											for="backup-schedule-{i}-time"
											class="mb-1 block text-xs font-medium text-parchment-200"
											>Preferred time (HH:MM)</label
										>
										<!-- Kept as text + pattern to match the backend's parseHHMM
											validation; a native time picker would change the interaction. -->
										<input
											id="backup-schedule-{i}-time"
											name="backup-schedule-{i}-time"
											autocomplete="off"
											type="text"
											bind:value={s.time}
											pattern="([01][0-9]|2[0-3]):[0-5][0-9]"
											placeholder="HH:MM…"
											class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
										/>
									</div>
									<div>
										<label
											for="backup-schedule-{i}-keep"
											class="mb-1 block text-xs font-medium text-parchment-200"
											>Keep (0 = unlimited)</label
										>
										<input
											id="backup-schedule-{i}-keep"
											name="backup-schedule-{i}-keep"
											autocomplete="off"
											type="number"
											inputmode="numeric"
											min="0"
											bind:value={s.keep}
											class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
										/>
									</div>
									<div class="sm:col-span-2 lg:col-span-2">
										<label
											for="backup-schedule-{i}-path"
											class="mb-1 block text-xs font-medium text-parchment-200"
											>Output directory (optional)</label
										>
										<input
											id="backup-schedule-{i}-path"
											name="backup-schedule-{i}-path"
											autocomplete="off"
											type="text"
											bind:value={s.path}
											placeholder="Fallback directory…"
											class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
										/>
									</div>
								</div>
								<button
									type="button"
									onclick={() => removeSchedule(i)}
									class="mt-3 rounded-lg border border-clay-800 px-3 py-1 text-sm text-parchment-400 hover:bg-clay-800 hover:text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
								>
									Remove
								</button>
							</div>
						{/each}
						<button
							type="button"
							onclick={addSchedule}
							class="text-sm text-gold-500 hover:text-gold-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
						>
							+ Add schedule
						</button>
					</div>
				</section>

				<section class="mb-3 rounded-xl border border-clay-800 bg-clay-900 p-5">
					<h2 class="mb-4 text-lg font-semibold text-parchment-200">Logging</h2>
					<div class="grid gap-4 sm:grid-cols-2">
						<div>
							<label for="log-level" class="mb-1 block text-sm font-medium text-parchment-200"
								>Log level</label
							>
							<select
								id="log-level"
								name="log-level"
								bind:value={cfg.app.log_level}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							>
								<option value="silent">silent</option>
								<option value="fatal">fatal</option>
								<option value="error">error</option>
								<option value="warn">warn</option>
								<option value="info">info</option>
								<option value="debug">debug</option>
							</select>
						</div>
						<div>
							<label for="log-max-size" class="mb-1 block text-sm font-medium text-parchment-200"
								>Max size per file (MB, 0 = no rotation)</label
							>
							<input
								id="log-max-size"
								name="log-max-size"
								autocomplete="off"
								type="number"
								inputmode="numeric"
								min="0"
								bind:value={cfg.app.logging.max_size}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							/>
						</div>
						<div>
							<label for="log-max-backups" class="mb-1 block text-sm font-medium text-parchment-200"
								>Max backups to keep</label
							>
							<input
								id="log-max-backups"
								name="log-max-backups"
								autocomplete="off"
								type="number"
								inputmode="numeric"
								min="0"
								bind:value={cfg.app.logging.max_backups}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							/>
						</div>
						<div>
							<label for="log-max-age" class="mb-1 block text-sm font-medium text-parchment-200"
								>Max age (days, 0 = no limit)</label
							>
							<input
								id="log-max-age"
								name="log-max-age"
								autocomplete="off"
								type="number"
								inputmode="numeric"
								min="0"
								bind:value={cfg.app.logging.max_age}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							/>
						</div>
						<div>
							<label for="log-compress" class="mb-1 block text-sm font-medium text-parchment-200"
								>Compress rotated logs</label
							>
							<div class="mt-2 flex items-center gap-2">
								<input
									id="log-compress"
									name="log-compress"
									autocomplete="off"
									type="checkbox"
									bind:checked={cfg.app.logging.compress}
									class="h-5 w-5 rounded border-clay-800 bg-clay-950 text-gold-500 focus:ring-gold-500"
								/>
								<span class="text-sm text-parchment-400">
									{cfg.app.logging.compress ? 'Enabled' : 'Disabled'}
								</span>
							</div>
						</div>
					</div>
				</section>

				<button
					type="button"
					onclick={save}
					disabled={saving}
					class="w-full rounded-lg bg-gold-500 px-4 py-2 text-sm font-medium text-clay-950 hover:bg-gold-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-50"
				>
					{saving ? 'Saving…' : 'Save settings'}
				</button>
			</div>
		{/if}

		{#if activeTab === 'Users'}
			<div role="tabpanel" id="panel-users" aria-labelledby="tab-users">
				<div class="space-y-4">
					<div class="flex items-center justify-between">
						<h2 class="text-lg font-semibold text-parchment-200">Users</h2>
						<button
							onclick={openNewUser}
							class="rounded-lg bg-gold-500 px-4 py-2 text-sm font-medium text-clay-950 hover:bg-gold-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
						>
							Create User
						</button>
					</div>

					<DataTable
						columns={userColumns}
						fetch={fetchUsers}
						title=""
						defaultPageSize={25}
						pageSizes={[10, 25, 50, 100]}
						{refreshKey}
						onActionClick={handleUserPageClick}
						urlSync="users"
					/>
				</div>

				<Modal
					open={showUserModal}
					title={editingUser ? 'Edit User' : 'Create User'}
					onClose={() => (showUserModal = false)}
				>
					<form
						onsubmit={(e) => {
							e.preventDefault();
							saveUser();
						}}
					>
						<div class="space-y-4">
							<div>
								<label for="user-username" class="mb-1 block text-xs font-medium text-parchment-400"
									>Username</label
								>
								<input
									id="user-username"
									name="user-username"
									autocomplete="off"
									spellcheck="false"
									type="text"
									bind:value={formUsername}
									placeholder="Username"
									class="w-full rounded-md border border-clay-700 bg-clay-900 px-3 py-1.5 text-sm text-parchment-200 placeholder-parchment-600 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
								/>
							</div>
							<div>
								<label for="user-role" class="mb-1 block text-xs font-medium text-parchment-400"
									>Role</label
								>
								<select
									id="user-role"
									name="user-role"
									bind:value={formRole}
									class="w-full rounded-md border border-clay-700 bg-clay-900 px-3 py-1.5 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
								>
									<option value="viewer">Viewer</option>
									<option value="editor">Editor</option>
									<option value="admin">Admin</option>
								</select>
							</div>
							<div>
								<label for="user-password" class="mb-1 block text-xs font-medium text-parchment-400"
									>Password</label
								>
								<input
									id="user-password"
									name="user-password"
									autocomplete="new-password"
									type="password"
									bind:value={formPassword}
									placeholder={editingUser ? 'Leave blank to keep current' : 'Password'}
									class="w-full rounded-md border border-clay-700 bg-clay-900 px-3 py-1.5 text-sm text-parchment-200 placeholder-parchment-600 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
								/>
							</div>
							{#if userError}
								<p class="text-sm text-terracotta-500" aria-live="polite">{userError}</p>
							{/if}
							<div class="flex justify-end gap-2">
								<button
									type="button"
									onclick={() => (showUserModal = false)}
									class="rounded-md px-3 py-1.5 text-xs font-medium text-parchment-400 hover:bg-clay-800 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
								>
									Cancel
								</button>
								<button
									type="submit"
									disabled={savingUser}
									class="rounded-md bg-gold-500 px-3 py-1.5 text-xs font-medium text-clay-950 hover:bg-gold-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-50"
								>
									{savingUser ? 'Saving…' : editingUser ? 'Save' : 'Create'}
								</button>
							</div>
						</div>
					</form>
				</Modal>
			</div>
		{/if}
	</div>
{/if}
