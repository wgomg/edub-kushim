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

	let activeTab = $state('Configuration');

	let showUserModal = $state(false);
	let editingUser = $state(null);
	let formUsername = $state('');
	let formPassword = $state('');
	let formRole = $state('viewer');
	let userError = $state('');
	let refreshKey = $state(0);

	let llmModels = $state({ adapters: {}, providers: {} });

	let selectedAdapterProviders = $derived(
		llmModels.adapters[cfg?.enricher?.contentanalyzer?.llm?.adapter] ?? []
	);
	let selectedProviderModels = $derived(
		llmModels.providers[cfg?.enricher?.contentanalyzer?.llm?.provider] ?? []
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

	onMount(async () => {
		const loaded = await api.config.get();
		if (loaded) {
			cfg = loaded;
			checkStatus();
		}
		llmModels = await api.config.llmModels();
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

	function bodyFromConfig() {
		return {
			'server.host': cfg.server.host,
			'server.port': Number(cfg.server.port),
			'server.max_upload_size': Number(cfg.server.max_upload_size),
			'server.max_download_files': Number(cfg.server.max_download_files),
			'server.max_download_size_mb': Number(cfg.server.max_download_size_mb),
			'server.max_concurrent_batches': Number(cfg.server.max_concurrent_batches),
			'consumer.ocr.engine': cfg.consumer.ocr.engine,
			'consumer.ocr.languages': cfg.consumer.ocr.languages.filter(Boolean),
			'consumer.ocr.data_dir': cfg.consumer.ocr.data_dir,
			'consumer.ocr.timeout': Number(cfg.consumer.ocr.timeout),
			'consumer.ocr.ocr_workers': Number(cfg.consumer.ocr.ocr_workers),
			'consumer.workers': Number(cfg.consumer.workers),
			'consumer.max_files_per_batch': Number(cfg.consumer.max_files_per_batch),
			'consumer.polling.enabled': cfg.consumer.polling.enabled,
			'consumer.polling.interval': Number(cfg.consumer.polling.interval),
			'consumer.polling.windows': cfg.consumer.polling.windows ?? [],
			'consumer.reclaim.enabled': cfg.consumer.reclaim.enabled,
			'consumer.reclaim.max_retries': Number(cfg.consumer.reclaim.max_retries),
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
			'enricher.contentanalyzer.llm.adapter': cfg.enricher.contentanalyzer.llm.adapter,
			'enricher.contentanalyzer.llm.provider': cfg.enricher.contentanalyzer.llm.provider,
			'enricher.contentanalyzer.llm.model': cfg.enricher.contentanalyzer.llm.model,
			'enricher.contentanalyzer.llm.token': cfg.enricher.contentanalyzer.llm.token,
			'enricher.contentanalyzer.llm.temperature': Number(
				cfg.enricher.contentanalyzer.llm.temperature
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
			'database.host': cfg.database.host,
			'database.port': Number(cfg.database.port),
			'database.user': cfg.database.user,
			'database.database': cfg.database.database,
			'database.sslmode': cfg.database.sslmode,
			'backup.enabled': cfg.backup.enabled,
			'backup.interval': Number(cfg.backup.interval),
			'backup.time': cfg.backup.time,
			'backup.path': cfg.backup.path,
			'backup.keep': Number(cfg.backup.keep),
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
				toastStore.success('Settings saved. Downloads in progress…');
				startPolling();
			} else {
				toastStore.success('Settings saved.');
			}
			if (res && 'missing_tools' in res) {
				missingTools = res.missing_tools;
			}
			const loaded = await api.config.get();
			if (loaded) cfg = loaded;
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
			return;
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

		let result;
		if (editingUser) {
			const body = { username, role: formRole };
			if (password) body.password = password;
			result = await api.users.update(editingUser.id, body);
		} else {
			if (!password) {
				userError = 'Password is required';
				return;
			}
			result = await api.users.create({ username, password, role: formRole });
		}

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
			<h1 class="text-2xl font-bold text-parchment-200">Settings</h1>
			{#if pendingTasks > 0}
				<div class="flex items-center gap-2 text-sm text-gold-500">
					<div
						class="h-4 w-4 animate-spin rounded-full border-2 border-clay-800 border-t-gold-500 motion-reduce:animate-none"
					></div>
					{pendingTasks} task(s) pending
				</div>
			{/if}
		</div>

		<div class="flex gap-1 border-b border-clay-800">
			<button
				type="button"
				onclick={() => (activeTab = 'Configuration')}
				class="rounded-t-lg px-4 py-2 text-sm font-medium transition-colors {activeTab ===
				'Configuration'
					? 'border-b-2 border-gold-500 text-gold-500'
					: 'text-parchment-400 hover:text-parchment-200'}"
			>
				Configuration
			</button>
			<button
				type="button"
				onclick={() => (activeTab = 'Users')}
				class="rounded-t-lg px-4 py-2 text-sm font-medium transition-colors {activeTab === 'Users'
					? 'border-b-2 border-gold-500 text-gold-500'
					: 'text-parchment-400 hover:text-parchment-200'}"
			>
				Users
			</button>
		</div>

		{#if activeTab === 'Configuration'}
			{#if missingTools?.find((t) => t.engine === 'curl')}
				<div
					class="rounded-lg border border-terracotta-600 bg-terracotta-500/10 p-3 text-sm text-terracotta-500"
				>
					<p class="font-medium">"curl" not installed (required for downloads)</p>
					<p class="mt-1 text-parchment-400">
						Model and language file downloads will fail without curl.
					</p>
					{#each Object.entries(hintsForEngine('curl')) as [system, cmd]}
						<pre class="mt-1 text-xs text-parchment-300">{system}: {cmd}</pre>
					{/each}
				</div>
			{/if}

			<section class="rounded-xl border border-clay-800 bg-clay-900 p-5">
				<h2 class="mb-4 text-lg font-semibold text-parchment-200">Server</h2>
				<div class="grid gap-4 sm:grid-cols-2">
					<div>
						<label for="server-host" class="mb-1 block text-sm font-medium text-parchment-200"
							>Host</label
						>
						<input
							id="server-host"
							type="text"
							bind:value={cfg.server.host}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
						/>
					</div>
					<div>
						<label for="server-port" class="mb-1 block text-sm font-medium text-parchment-200"
							>Port</label
						>
						<input
							id="server-port"
							type="number"
							min="1"
							max="65535"
							bind:value={cfg.server.port}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
						/>
					</div>
					<div>
						<label for="server-max-upload" class="mb-1 block text-sm font-medium text-parchment-200"
							>Max upload size (MB)</label
						>
						<input
							id="server-max-upload"
							type="number"
							min="1"
							bind:value={cfg.server.max_upload_size}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
						/>
					</div>
					<div>
						<label
							for="server-max-download-files"
							class="mb-1 block text-sm font-medium text-parchment-200">Max download files</label
						>
						<input
							id="server-max-download-files"
							type="number"
							min="1"
							bind:value={cfg.server.max_download_files}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
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
							type="number"
							min="0"
							bind:value={cfg.server.max_download_size_mb}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
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
							type="number"
							min="1"
							bind:value={cfg.server.max_concurrent_batches}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
						/>
					</div>
				</div>
			</section>

			<section class="rounded-xl border border-clay-800 bg-clay-900 p-5">
				<h2 class="mb-4 text-lg font-semibold text-parchment-200">Storage</h2>
				<div class="grid gap-4 sm:grid-cols-2">
					<div>
						<label for="consumption-dir" class="mb-1 block text-sm font-medium text-parchment-200">
							Consumption directory (inbox)
						</label>
						<input
							id="consumption-dir"
							type="text"
							bind:value={cfg.storage.consumption_dir}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
						/>
					</div>
					<div>
						<label for="storage-dir" class="mb-1 block text-sm font-medium text-parchment-200">
							Storage directory
						</label>
						<input
							id="storage-dir"
							type="text"
							bind:value={cfg.storage.storage_dir}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
						/>
					</div>
				</div>
			</section>

			<section class="rounded-xl border border-clay-800 bg-clay-900 p-5">
				<h2 class="mb-4 text-lg font-semibold text-parchment-200">Database</h2>
				<div class="grid gap-4 sm:grid-cols-2">
					<div>
						<label for="db-host" class="mb-1 block text-sm font-medium text-parchment-200">
							Database host
						</label>
						<input
							id="db-host"
							type="text"
							bind:value={cfg.database.host}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
						/>
					</div>
					<div>
						<label for="db-port" class="mb-1 block text-sm font-medium text-parchment-200">
							Database port
						</label>
						<input
							id="db-port"
							type="number"
							min="1"
							max="65535"
							bind:value={cfg.database.port}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
						/>
					</div>
					<div>
						<label for="db-user" class="mb-1 block text-sm font-medium text-parchment-200">
							Database user
						</label>
						<input
							id="db-user"
							type="text"
							bind:value={cfg.database.user}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
						/>
					</div>
					<div>
						<label for="db-name" class="mb-1 block text-sm font-medium text-parchment-200">
							Database name
						</label>
						<input
							id="db-name"
							type="text"
							bind:value={cfg.database.database}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
						/>
					</div>
					<div>
						<label for="db-sslmode" class="mb-1 block text-sm font-medium text-parchment-200">
							SSL mode
						</label>
						<select
							id="db-sslmode"
							bind:value={cfg.database.sslmode}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
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

			<section class="rounded-xl border border-clay-800 bg-clay-900 p-5">
				<h2 class="mb-4 text-lg font-semibold text-parchment-200">OCR</h2>
				<div class="grid gap-4 sm:grid-cols-2">
					<div>
						<label for="ocr-engine" class="mb-1 block text-sm font-medium text-parchment-200"
							>Engine</label
						>
						<select
							id="ocr-engine"
							bind:value={cfg.consumer.ocr.engine}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
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
							type="number"
							min="1"
							bind:value={cfg.consumer.ocr.timeout}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
						/>
					</div>
					<div>
						<label for="ocr-workers" class="mb-1 block text-sm font-medium text-parchment-200"
							>Workers</label
						>
						<input
							id="ocr-workers"
							type="number"
							min="0"
							bind:value={cfg.consumer.ocr.ocr_workers}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
						/>
						<p class="mt-1 text-xs text-parchment-500">0 = auto (CPU count)</p>
					</div>
				</div>

				{#if toolStatus?.find((t) => t.category === 'ocr' && !t.available)}
					<div
						class="mt-4 rounded-lg border border-terracotta-600 bg-terracotta-500/10 p-3 text-sm text-terracotta-500"
					>
						<p class="font-medium">"{cfg.consumer.ocr.engine}" is not installed</p>
						<p class="mt-1 text-parchment-400">
							Documents won't process until it is available. Install it, e.g.:
						</p>
						{#each Object.entries(hintsForEngine(cfg.consumer.ocr.engine)) as [system, cmd]}
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
							{#each Object.entries(ocrTool.lang_hints[0].install_hints) as [system, cmd]}
								<pre class="mt-1 text-xs text-parchment-300">{system}: {cmd}</pre>
							{/each}
						</div>
					{/if}
					{#if ocrTool?.companions?.length}
						<div class="mt-4 space-y-2 text-sm">
							{#each ocrTool.companions as c}
								{#if !c.available && c.required}
									<div
										class="rounded-lg border border-terracotta-600 bg-terracotta-500/10 p-3 text-terracotta-500"
									>
										<p class="font-medium">"{c.command}" not installed (required)</p>
										<p class="mt-1 text-parchment-400">{c.purpose}</p>
										{#each Object.entries(c.install_hints) as [system, cmd]}
											<pre class="mt-1 text-xs text-parchment-300">{system}: {cmd}</pre>
										{/each}
									</div>
								{:else if !c.available}
									<div
										class="rounded-lg border border-lapis-500/30 bg-lapis-500/10 p-3 text-parchment-300"
									>
										<p class="font-medium text-parchment-200">
											"{c.command}" not installed (optional)
										</p>
										<p class="mt-1">{c.purpose}. ocrmypdf will skip this feature without it.</p>
										{#each Object.entries(c.install_hints) as [system, cmd]}
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
							type="text"
							bind:value={cfg.consumer.ocr.data_dir}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
						/>
					</div>
				</div>
				<div class="mt-4">
					<label for="ocr-lang-0" class="mb-2 block text-sm font-medium text-parchment-200"
						>Languages</label
					>
					{#each cfg.consumer.ocr.languages as lang, i (i)}
						<div class="mb-2 flex gap-2">
							<input
								id="ocr-lang-{i}"
								type="text"
								value={lang}
								oninput={(e) => updateLanguage(i, e.currentTarget.value)}
								placeholder="eng"
								class="flex-1 rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 placeholder-parchment-500 focus:border-gold-500 focus-visible:outline-none"
							/>
							{#if cfg.consumer.ocr.languages.length > 1}
								<button
									type="button"
									onclick={() => removeLanguage(i)}
									class="rounded-lg border border-clay-800 px-3 text-sm text-parchment-400 hover:bg-clay-800 hover:text-parchment-200"
								>
									Remove
								</button>
							{/if}
						</div>
					{/each}
					<button
						type="button"
						onclick={addLanguage}
						class="text-sm text-gold-500 hover:text-gold-600"
					>
						+ Add language
					</button>
				</div>
			</section>

			<section class="rounded-xl border border-clay-800 bg-clay-900 p-5">
				<h2 class="mb-4 text-lg font-semibold text-parchment-200">Consumer</h2>
				<div class="grid gap-4 sm:grid-cols-2">
					<div>
						<label for="consumer-workers" class="mb-1 block text-sm font-medium text-parchment-200"
							>Workers</label
						>
						<input
							id="consumer-workers"
							type="number"
							min="1"
							bind:value={cfg.consumer.workers}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
						/>
					</div>
					<div>
						<label
							for="consumer-max-files-per-batch"
							class="mb-1 block text-sm font-medium text-parchment-200">Max files per batch</label
						>
						<input
							id="consumer-max-files-per-batch"
							type="number"
							min="0"
							bind:value={cfg.consumer.max_files_per_batch}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
						/>
					</div>
				</div>
			</section>

			<section class="rounded-xl border border-clay-800 bg-clay-900 p-5">
				<h2 class="mb-4 text-lg font-semibold text-parchment-200">Polling</h2>
				<div class="grid gap-4 sm:grid-cols-2">
					<div>
						<label for="polling-enabled" class="mb-1 block text-sm font-medium text-parchment-200"
							>Enabled</label
						>
						<div class="flex items-center gap-2">
							<input
								id="polling-enabled"
								type="checkbox"
								bind:checked={cfg.consumer.polling.enabled}
								class="h-5 w-5 rounded border-clay-800 bg-clay-950 text-gold-500 focus:ring-gold-500"
							/>
							<span class="text-sm text-parchment-400">
								{cfg.consumer.polling.enabled ? 'Active' : 'Inactive'}
							</span>
						</div>
					</div>
					<div>
						<label for="polling-interval" class="mb-1 block text-sm font-medium text-parchment-200"
							>Interval (minutes)</label
						>
						<input
							id="polling-interval"
							type="number"
							min="1"
							bind:value={cfg.consumer.polling.interval}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
						/>
					</div>
				</div>

				<div class="mt-4">
					<span class="mb-2 block text-sm font-medium text-parchment-200"
						>Active windows (optional)</span
					>
					{#each cfg.consumer.polling.windows as w, i (i)}
						<div class="mb-2 flex items-center gap-2">
							<input
								type="text"
								bind:value={w.start}
								aria-label="Start time"
								pattern="([01][0-9]|2[0-3]):[0-5][0-9]"
								placeholder="HH:MM"
								minlength="5"
								maxlength="5"
								class="w-36 rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
							/>
							<span class="text-parchment-400">to</span>
							<input
								type="text"
								bind:value={w.end}
								aria-label="End time"
								pattern="([01][0-9]|2[0-3]):[0-5][0-9]|24:00"
								placeholder="HH:MM"
								minlength="5"
								maxlength="5"
								class="w-36 rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
							/>
							<button
								type="button"
								onclick={() => removeWindow(i)}
								class="rounded-lg border border-clay-800 px-3 text-sm text-parchment-400 hover:bg-clay-800 hover:text-parchment-200"
							>
								Remove
							</button>
						</div>
					{/each}
					<button
						type="button"
						onclick={addWindow}
						class="text-sm text-gold-500 hover:text-gold-600"
					>
						+ Add window
					</button>
				</div>
			</section>

			<section class="rounded-xl border border-clay-800 bg-clay-900 p-5">
				<h2 class="mb-4 text-lg font-semibold text-parchment-200">Reclaim</h2>
				<div class="grid gap-4 sm:grid-cols-2">
					<div>
						<label for="reclaim-enabled" class="mb-1 block text-sm font-medium text-parchment-200"
							>Auto-resume interrupted batches</label
						>
						<div class="flex items-center gap-2">
							<input
								id="reclaim-enabled"
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
							class="mb-1 block text-sm font-medium text-parchment-200">Max retries per task</label
						>
						<input
							id="reclaim-max-retries"
							type="number"
							min="1"
							max="10"
							class="w-24 rounded-lg border border-clay-700 bg-clay-950 px-3 py-2 text-parchment-200 focus:border-gold-500 focus-visible:ring-1 focus-visible:ring-gold-500 focus-visible:outline-none"
							bind:value={cfg.consumer.reclaim.max_retries}
						/>
					</div>
				</div>
			</section>

			<section class="rounded-xl border border-clay-800 bg-clay-900 p-5">
				<h2 class="mb-4 text-lg font-semibold text-parchment-200">Text extractor</h2>
				<div class="grid gap-4 sm:grid-cols-2">
					<div>
						<label
							for="text-extractor-engine"
							class="mb-1 block text-sm font-medium text-parchment-200">Engine</label
						>
						<select
							id="text-extractor-engine"
							bind:value={cfg.consumer.textextractor.engine}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
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
							type="number"
							min="1"
							bind:value={cfg.consumer.textextractor.timeout}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
						/>
					</div>
				</div>
				{#if toolStatus?.find((t) => t.category === 'textextractor' && !t.available)}
					<div
						class="mt-4 rounded-lg border border-terracotta-600 bg-terracotta-500/10 p-3 text-sm text-terracotta-500"
					>
						<p class="font-medium">"{cfg.consumer.textextractor.engine}" is not installed</p>
						<p class="mt-1 text-parchment-400">
							Documents won't process until it is available. Install it, e.g.:
						</p>
						{#each Object.entries(hintsForEngine(cfg.consumer.textextractor.engine)) as [system, cmd]}
							<pre class="mt-1 text-xs text-parchment-300">{system}: {cmd}</pre>
						{/each}
					</div>
				{/if}
			</section>

			<section class="rounded-xl border border-clay-800 bg-clay-900 p-5">
				<h2 class="mb-4 text-lg font-semibold text-parchment-200">PDF optimizer</h2>
				<div class="grid gap-4 sm:grid-cols-2">
					<div>
						<label for="pdf-engine" class="mb-1 block text-sm font-medium text-parchment-200"
							>Engine</label
						>
						<select
							id="pdf-engine"
							bind:value={cfg.consumer.pdfoptimizer.engine}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
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
							type="text"
							bind:value={cfg.consumer.pdfoptimizer.fallback}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 placeholder-parchment-500 focus:border-gold-500 focus-visible:outline-none"
						/>
					</div>
				</div>
				{#if toolStatus?.find((t) => t.category === 'pdfoptimizer' && !t.available)}
					<div
						class="mt-4 rounded-lg border border-terracotta-600 bg-terracotta-500/10 p-3 text-sm text-terracotta-500"
					>
						<p class="font-medium">"{cfg.consumer.pdfoptimizer.engine}" is not installed</p>
						<p class="mt-1 text-parchment-400">
							Documents won't process until it is available. Install it, e.g.:
						</p>
						{#each Object.entries(hintsForEngine(cfg.consumer.pdfoptimizer.engine)) as [system, cmd]}
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
							type="number"
							min="0"
							bind:value={cfg.consumer.pdfoptimizer.timeout}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
						/>
					</div>
				</div>
			</section>

			<section class="rounded-xl border border-clay-800 bg-clay-900 p-5">
				<h2 class="mb-4 text-lg font-semibold text-parchment-200">Enricher</h2>
				<div class="grid gap-4 sm:grid-cols-2">
					<div>
						<label for="enricher-workers" class="mb-1 block text-sm font-medium text-parchment-200"
							>Workers</label
						>
						<input
							id="enricher-workers"
							type="number"
							min="1"
							bind:value={cfg.enricher.workers}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
						/>
					</div>
				</div>
			</section>

			<section class="rounded-xl border border-clay-800 bg-clay-900 p-5">
				<div class="mb-4 flex items-center justify-between">
					<h2 class="text-lg font-semibold text-parchment-200">Content analyzer (LLM)</h2>
					<label class="relative inline-flex cursor-pointer items-center">
						<input
							type="checkbox"
							bind:checked={cfg.enricher.contentanalyzer.enabled}
							class="peer sr-only"
						/>
						<div
							class="h-6 w-11 rounded-full border border-clay-700 bg-clay-800 peer-checked:border-gold-500 peer-checked:bg-gold-600 after:absolute after:top-0.5 after:left-0.5 after:h-5 after:w-5 after:rounded-full after:bg-parchment-400 after:transition-transform peer-checked:after:translate-x-full peer-checked:after:bg-white"
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
								bind:value={cfg.enricher.contentanalyzer.llm.adapter}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
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
								bind:value={cfg.enricher.contentanalyzer.llm.provider}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
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
								bind:value={cfg.enricher.contentanalyzer.llm.model}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
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
									class="flex-1 rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 placeholder-parchment-500 focus:border-gold-500 focus-visible:outline-none"
								/>
								<button
									type="button"
									onclick={() => (showToken = !showToken)}
									class="rounded-lg border border-clay-800 px-3 text-sm text-parchment-400 hover:bg-clay-800 hover:text-parchment-200"
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
								type="number"
								min="0"
								max="2"
								step="0.1"
								bind:value={cfg.enricher.contentanalyzer.llm.temperature}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
							/>
						</div>
					</div>

					<div class="mt-4">
						<label
							for="content-analyzer-prompt-template"
							class="mb-1 block text-sm font-medium text-parchment-200"
						>
							Prompt template (advanced)
						</label>
						<p class="mb-2 text-xs text-parchment-500">
							Leave empty for default. Available placeholders: {`{{.DocTypePrompt}}`}, {`{{.TagsPrompt}}`},
							{`{{.PeoplePrompt}}`}, {`{{.Text}}`} (required)
						</p>
						<textarea
							id="content-analyzer-prompt-template"
							rows="8"
							bind:value={cfg.enricher.contentanalyzer.prompt_template}
							spellcheck="false"
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 font-mono text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
						></textarea>
					</div>
				{/if}
			</section>

			<section class="rounded-xl border border-clay-800 bg-clay-900 p-5">
				<h2 class="mb-4 text-lg font-semibold text-parchment-200">Tag matcher</h2>
				<div class="grid gap-4 sm:grid-cols-2">
					<div>
						<label
							for="tag-matcher-timeout"
							class="mb-1 block text-sm font-medium text-parchment-200">Timeout (s)</label
						>
						<input
							id="tag-matcher-timeout"
							type="number"
							min="1"
							bind:value={cfg.enricher.tagmatcher.timeout}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
						/>
					</div>
					<div>
						<label
							for="tag-matcher-reduce-target"
							class="mb-1 block text-sm font-medium text-parchment-200">Reduce target words</label
						>
						<input
							id="tag-matcher-reduce-target"
							type="number"
							min="0"
							bind:value={cfg.enricher.tagmatcher.reduce_target_words}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
						/>
					</div>
					<div>
						<label
							for="tag-matcher-chunk-size"
							class="mb-1 block text-sm font-medium text-parchment-200">Chunk size</label
						>
						<input
							id="tag-matcher-chunk-size"
							type="number"
							min="0"
							bind:value={cfg.enricher.tagmatcher.chunk_size}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
						/>
					</div>
					<div>
						<label
							for="tag-matcher-hugot-model"
							class="mb-1 block text-sm font-medium text-parchment-200">Hugot model</label
						>
						<input
							id="tag-matcher-hugot-model"
							type="text"
							bind:value={cfg.enricher.tagmatcher.hugot.model}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
						/>
					</div>
					<div>
						<label
							for="tag-matcher-hugot-backend"
							class="mb-1 block text-sm font-medium text-parchment-200">Hugot backend</label
						>
						<select
							id="tag-matcher-hugot-backend"
							bind:value={cfg.enricher.tagmatcher.hugot.backend}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
						>
							<option value="ort">ort</option>
							<option value="GO">GO</option>
						</select>
					</div>
				</div>
			</section>

			<section class="rounded-xl border border-clay-800 bg-clay-900 p-5">
				<h2 class="mb-4 text-lg font-semibold text-parchment-200">Text reducer</h2>
				<div class="grid gap-4 sm:grid-cols-2">
					<div>
						<label
							for="text-reducer-engine"
							class="mb-1 block text-sm font-medium text-parchment-200">Engine</label
						>
						<select
							id="text-reducer-engine"
							bind:value={cfg.enricher.textreducer.engine}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
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
							type="number"
							min="1"
							bind:value={cfg.enricher.textreducer.timeout}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
						/>
					</div>
					<div class="sm:col-span-2">
						<label
							for="text-reducer-target-words"
							class="mb-1 block text-sm font-medium text-parchment-200">Target words</label
						>
						<input
							id="text-reducer-target-words"
							type="number"
							min="1"
							bind:value={cfg.enricher.textreducer.target_words}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
						/>
					</div>
				</div>
			</section>

			<section class="rounded-xl border border-clay-800 bg-clay-900 p-5">
				<h2 class="mb-4 text-lg font-semibold text-parchment-200">Backup</h2>
				<div class="grid gap-4 sm:grid-cols-2">
					<div>
						<label for="backup-enabled" class="mb-1 block text-sm font-medium text-parchment-200">
							Enabled
						</label>
						<input
							id="backup-enabled"
							type="checkbox"
							bind:checked={cfg.backup.enabled}
							class="mt-2 h-5 w-5 rounded border-clay-800 bg-clay-950 text-gold-500 focus:ring-gold-500"
						/>
					</div>
					<div>
						<label for="backup-interval" class="mb-1 block text-sm font-medium text-parchment-200">
							Interval (days)
						</label>
						<input
							id="backup-interval"
							type="number"
							min="1"
							step="0.1"
							bind:value={cfg.backup.interval}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
						/>
					</div>
					<div>
						<label for="backup-time" class="mb-1 block text-sm font-medium text-parchment-200">
							Preferred time (HH:MM)
						</label>
						<input
							id="backup-time"
							type="text"
							bind:value={cfg.backup.time}
							pattern="([01][0-9]|2[0-3]):[0-5][0-9]"
							placeholder="HH:MM"
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
						/>
					</div>
					<div>
						<label for="backup-keep" class="mb-1 block text-sm font-medium text-parchment-200">
							Keep (0 = unlimited)
						</label>
						<input
							id="backup-keep"
							type="number"
							min="0"
							bind:value={cfg.backup.keep}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
						/>
					</div>
					<div class="sm:col-span-2">
						<label for="backup-path" class="mb-1 block text-sm font-medium text-parchment-200">
							Output directory
						</label>
						<input
							id="backup-path"
							type="text"
							bind:value={cfg.backup.path}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
						/>
					</div>
				</div>
			</section>

			<section class="rounded-xl border border-clay-800 bg-clay-900 p-5">
				<h2 class="mb-4 text-lg font-semibold text-parchment-200">Logging</h2>
				<div class="grid gap-4 sm:grid-cols-2">
					<div>
						<label for="log-level" class="mb-1 block text-sm font-medium text-parchment-200"
							>Log level</label
						>
						<select
							id="log-level"
							bind:value={cfg.app.log_level}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
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
							type="number"
							min="0"
							bind:value={cfg.app.logging.max_size}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
						/>
					</div>
					<div>
						<label for="log-max-backups" class="mb-1 block text-sm font-medium text-parchment-200"
							>Max backups to keep</label
						>
						<input
							id="log-max-backups"
							type="number"
							min="0"
							bind:value={cfg.app.logging.max_backups}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
						/>
					</div>
					<div>
						<label for="log-max-age" class="mb-1 block text-sm font-medium text-parchment-200"
							>Max age (days, 0 = no limit)</label
						>
						<input
							id="log-max-age"
							type="number"
							min="0"
							bind:value={cfg.app.logging.max_age}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:outline-none"
						/>
					</div>
					<div>
						<label for="log-compress" class="mb-1 block text-sm font-medium text-parchment-200"
							>Compress rotated logs</label
						>
						<div class="mt-2 flex items-center gap-2">
							<input
								id="log-compress"
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
				class="w-full rounded-lg bg-gold-500 px-4 py-2 text-sm font-medium text-clay-950 hover:bg-gold-600 disabled:opacity-50"
			>
				{saving ? 'Saving…' : 'Save settings'}
			</button>
		{/if}

		{#if activeTab === 'Users'}
			<div
				class="space-y-4"
				onclick={handleUserPageClick}
				onkeydown={(e) => {
					if (e.key === 'Enter' || e.key === ' ') handleUserPageClick(e);
				}}
				role="presentation"
			>
				<div class="flex items-center justify-between">
					<h2 class="text-lg font-semibold text-parchment-200">Users</h2>
					<button
						onclick={openNewUser}
						class="rounded-lg bg-gold-500 px-4 py-2 text-sm font-medium text-clay-950 hover:bg-gold-600"
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
								type="text"
								bind:value={formUsername}
								placeholder="Username"
								class="w-full rounded-md border border-clay-700 bg-clay-900 px-3 py-1.5 text-sm text-parchment-200 placeholder-parchment-600 focus:border-gold-500 focus:ring-0 focus-visible:outline-none"
							/>
						</div>
						<div>
							<label for="user-role" class="mb-1 block text-xs font-medium text-parchment-400"
								>Role</label
							>
							<select
								id="user-role"
								bind:value={formRole}
								class="w-full rounded-md border border-clay-700 bg-clay-900 px-3 py-1.5 text-sm text-parchment-200 focus:border-gold-500 focus:ring-0 focus-visible:outline-none"
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
								type="password"
								bind:value={formPassword}
								placeholder={editingUser ? 'Leave blank to keep current' : 'Password'}
								class="w-full rounded-md border border-clay-700 bg-clay-900 px-3 py-1.5 text-sm text-parchment-200 placeholder-parchment-600 focus:border-gold-500 focus:ring-0 focus-visible:outline-none"
							/>
						</div>
						{#if userError}
							<p class="text-sm text-terracotta-500">{userError}</p>
						{/if}
						<div class="flex justify-end gap-2">
							<button
								type="button"
								onclick={() => (showUserModal = false)}
								class="rounded-md px-3 py-1.5 text-xs font-medium text-parchment-400 hover:bg-clay-800"
							>
								Cancel
							</button>
							<button
								type="submit"
								disabled={!formUsername.trim()}
								class="rounded-md bg-gold-500 px-3 py-1.5 text-xs font-medium text-clay-950 hover:bg-gold-600 disabled:opacity-50"
							>
								{editingUser ? 'Save' : 'Create'}
							</button>
						</div>
					</div>
				</form>
			</Modal>
		{/if}
	</div>
{/if}
