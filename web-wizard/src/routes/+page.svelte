<script>
	import { onMount } from 'svelte';
	import { configApi } from '$lib/api.js';
	import { hintsForEngine } from '$lib/tools.js';

	let step = $state(parseInt(new URL(window.location.href).searchParams.get('step') || '1') || 1);
	let configDir = $state('');
	let cfg = $state(null);
	let pendingTasks = $state(0);
	let configured = $state(false);
	let error = $state('');
	let missingTools = $state([]);
	let toolStatus = $state([]);
	let failedTasks = $state([]);
	let pollInterval;
	let adminUsername = $state('');
	let adminPassword = $state('');
	let adminConfirm = $state('');
	let adminError = $state('');
	let usernameError = $state('');
	let passwordError = $state('');
	let confirmError = $state('');
	let adminCreating = $state(false);
	let saving = $state(false);
	let submittingDir = $state(false);
	let serviceFilesPath = $state('');

	let mimeTypeOptions = $state([]);

	$effect(() => {
		const url = new URL(window.location.href);
		url.searchParams.set('step', String(step));
		history.replaceState(null, '', url.pathname + url.search);
	});

	function syncMimeCheckboxes() {
		const types = cfg?.available_file_types;
		const exts = cfg?.consumer?.supported_files ?? [];
		if (!types) return;
		mimeTypeOptions = types.map((ft) => ({
			...ft,
			checked: ft.required || ft.extensions.some((e) => exts.includes(e))
		}));
	}

	onMount(async () => {
		try {
			const loaded = await configApi.get();
			cfg = loaded;
			syncMimeCheckboxes();
			if (cfg.app.initialized) {
				await checkStatus();
				if (pendingTasks > 0) {
					step = 4;
					startPolling();
				} else if (configured) {
					step = 5;
				} else {
					step = 2;
				}
			}
		} catch (e) {
			error = e.message;
		}
	});

	async function submitConfigDir(e) {
		e.preventDefault();
		error = '';
		submittingDir = true;
		try {
			const res = await configApi.update({ config_dir: configDir });
			if (res && res.service_files_path) {
				serviceFilesPath = res.service_files_path;
			}
			const loaded = await configApi.get();
			if (loaded) {
				cfg = loaded;
				syncMimeCheckboxes();
			}
			step = 2;
		} catch (e) {
			error = e.message;
		} finally {
			submittingDir = false;
		}
	}

	async function submitSettings(e) {
		e.preventDefault();
		saving = true;
		error = '';
		try {
			const body = buildConfigBody();
			const res = await configApi.update(body);
			const loaded = await configApi.get();
			if (loaded) {
				cfg = loaded;
				syncMimeCheckboxes();
			}
			if (res && res.service_files_path) {
				serviceFilesPath = res.service_files_path;
			}
			if (res && 'missing_tools' in res) {
				missingTools = res.missing_tools ?? [];
			}
			if (res && 'pending_tasks' in res && res.pending_tasks > 0) {
				pendingTasks = res.pending_tasks;
				step = 4;
				startPolling();
			} else {
				step = 5;
			}
		} catch (e) {
			error = e.message;
		} finally {
			saving = false;
		}
	}

	function buildConfigBody() {
		return {
			'server.port': Number(cfg.server.port),
			'consumer.ocr.engine': cfg.consumer.ocr.engine,
			'consumer.ocr.languages': cfg.consumer.ocr.languages.filter(Boolean),
			'consumer.ocr.timeout': Number(cfg.consumer.ocr.timeout),
			'consumer.workers': Number(cfg.consumer.workers),
			'consumer.pdfoptimizer.engine': cfg.consumer.pdfoptimizer.engine,
			'consumer.pdfoptimizer.fallback': cfg.consumer.pdfoptimizer.fallback,
			'consumer.pdfoptimizer.timeout': Number(cfg.consumer.pdfoptimizer.timeout),
			'consumer.textextractor.engine': cfg.consumer.textextractor.engine,
			'consumer.textextractor.timeout': Number(cfg.consumer.textextractor.timeout),
			...(mimeTypeOptions.length > 0
				? {
						'consumer.supported_files': mimeTypeOptions
							.filter((o) => o.checked)
							.flatMap((o) => o.extensions)
					}
				: {}),
			'consumer.converter.enabled': cfg.consumer.converter.enabled,
			'consumer.converter.binary': cfg.consumer.converter.binary,
			'consumer.converter.timeout': Number(cfg.consumer.converter.timeout),
			'enricher.workers': Number(cfg.enricher.workers),
			'enricher.textreducer.engine': cfg.enricher.textreducer.engine,
			'enricher.textreducer.timeout': Number(cfg.enricher.textreducer.timeout),
			'enricher.textreducer.target_words': Number(cfg.enricher.textreducer.target_words),
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
			'database.runtime': cfg.database.runtime,
			'database.container': cfg.database.container
		};
	}

	async function checkStatus() {
		const status = await configApi.status();
		pendingTasks = status.pending_tasks;
		configured = status.configured;
		missingTools = status.missing_tools ?? [];
		toolStatus = status.tools ?? [];
		failedTasks = status.failed_tasks ?? [];
		return status;
	}

	async function retryFailed() {
		error = '';
		try {
			await configApi.retryFailed();
			await checkStatus();
		} catch (e) {
			error = e.message;
		}
	}

	function startPolling() {
		if (pollInterval) clearInterval(pollInterval);
		pollInterval = setInterval(async () => {
			try {
				const status = await checkStatus();
				if (status.pending_tasks === 0 && status.configured) {
					clearInterval(pollInterval);
					pollInterval = null;
					step = 5;
				}
			} catch (e) {
				error = e.message;
			}
		}, 3000);
	}

	function addLanguage() {
		cfg.consumer.ocr.languages = [...cfg.consumer.ocr.languages, ''];
	}

	function removeLanguage(index) {
		const lang = cfg.consumer.ocr.languages[index];
		if (lang && lang.trim() && !window.confirm(`Remove language "${lang}"?`)) return;
		cfg.consumer.ocr.languages = cfg.consumer.ocr.languages.filter((_, i) => i !== index);
	}

	function updateLanguage(index, value) {
		cfg.consumer.ocr.languages = cfg.consumer.ocr.languages.map((lang, i) =>
			i === index ? value : lang
		);
	}

	async function createAdminUser(e) {
		e.preventDefault();
		adminError = '';
		usernameError = '';
		passwordError = '';
		confirmError = '';
		let hasError = false;
		if (!adminUsername.trim()) {
			usernameError = 'Username is required';
			hasError = true;
		}
		if (adminPassword.length < 12) {
			passwordError = 'Password must be at least 12 characters';
			hasError = true;
		}
		if (
			!/[A-Z]/.test(adminPassword) ||
			!/[a-z]/.test(adminPassword) ||
			!/[0-9]/.test(adminPassword) ||
			!/[^A-Za-z0-9]/.test(adminPassword)
		) {
			passwordError =
				'Password must contain at least one uppercase letter, lowercase letter, digit, and special character';
			hasError = true;
		}
		if (adminPassword !== adminConfirm) {
			confirmError = 'Passwords do not match';
			hasError = true;
		}
		if (hasError) return;
		try {
			adminCreating = true;
			await configApi.createAdminUser({ username: adminUsername.trim(), password: adminPassword });
			step = 6;
		} catch (e) {
			if (e.message.includes('username already exists')) {
				step = 6;
			} else {
				adminError = e.message;
			}
		} finally {
			adminCreating = false;
		}
	}
</script>

{#if error}
	<div
		aria-live="polite"
		class="mb-4 rounded-lg border border-terracotta-600 bg-terracotta-500/10 p-3 text-sm text-terracotta-500"
	>
		{error}
	</div>
{/if}

{#if !cfg && step !== 1}
	<div class="text-center text-sm text-parchment-500">Loading configuration…</div>
{/if}

{#if step === 1}
	<form onsubmit={submitConfigDir} class="touch-manipulation space-y-4">
		<div>
			<label for="config-dir" class="mb-1 block text-sm font-medium text-parchment-200">
				Configuration directory
			</label>
			<input
				id="config-dir"
				type="text"
				bind:value={configDir}
				placeholder="/home/user/.config/edub-kushim…"
				autocomplete="off"
				required
				class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 placeholder-parchment-500 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
			/>
			<p class="mt-1 text-xs text-parchment-500">
				Where config.yaml, database, and downloaded models will be stored.
			</p>
			{#if error}
				<p class="mt-1 text-xs text-terracotta-500">{error}</p>
			{/if}
		</div>
		<button
			type="submit"
			disabled={submittingDir}
			class="w-full rounded-lg bg-gold-500 px-4 py-2 text-sm font-medium text-clay-950 hover:bg-gold-600 disabled:cursor-not-allowed disabled:opacity-50"
		>
			{submittingDir ? 'Continuing…' : 'Continue'}
		</button>
	</form>
{/if}

{#if step === 2 && cfg}
	<div class="mb-4 text-center">
		<p class="text-xs font-medium tracking-wide text-parchment-500 uppercase">Step 2 of 6</p>
		<h2 class="text-lg font-semibold text-balance text-parchment-200">Consumer Settings</h2>
	</div>

	{#if missingTools?.find((t) => t.engine === 'curl')}
		<div
			class="mb-4 rounded-lg border border-terracotta-600 bg-terracotta-500/10 p-3 text-sm text-terracotta-500"
		>
			<p class="font-medium">&ldquo;curl&rdquo; not installed (required for downloads)</p>
			<p class="mt-1 text-parchment-400">
				Model and language file downloads will fail without curl.
			</p>
			{#each Object.entries(hintsForEngine('curl')) as [system, cmd], i (i)}
				<pre class="text-parchment-300 mt-1 text-xs">{system}: {cmd}</pre>
			{/each}
		</div>
	{/if}

	<form
		onsubmit={(e) => {
			e.preventDefault();
			step = 3;
		}}
		class="touch-manipulation space-y-5"
	>
		<section class="rounded-xl border border-clay-800 bg-clay-950/50 p-4">
			<h3 class="mb-3 text-sm font-semibold text-balance text-parchment-200">Server</h3>
			<div>
				<label for="server-port" class="mb-1 block text-sm font-medium text-parchment-200">
					edub server port
				</label>
				<input
					id="server-port"
					name="server-port"
					type="number"
					min="1"
					max="65535"
					bind:value={cfg.server.port}
					autocomplete="off"
					class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
				/>
			</div>
		</section>

		<section class="rounded-xl border border-clay-800 bg-clay-950/50 p-4">
			<h3 class="mb-3 text-sm font-semibold text-balance text-parchment-200">Storage</h3>
			<div class="space-y-3">
				<div>
					<label for="consumption-dir" class="mb-1 block text-sm font-medium text-parchment-200">
						Consumption directory (inbox)
					</label>
					<input
						id="consumption-dir"
						name="consumption-dir"
						type="text"
						autocomplete="off"
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
						type="text"
						autocomplete="off"
						bind:value={cfg.storage.storage_dir}
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					/>
				</div>
			</div>
		</section>

		<section class="rounded-xl border border-clay-800 bg-clay-950/50 p-4">
			<h3 class="mb-3 text-sm font-semibold text-balance text-parchment-200">Database</h3>
			<div class="space-y-3">
				<div>
					<label for="db-host" class="mb-1 block text-sm font-medium text-parchment-200">
						Database host
					</label>
					<input
						id="db-host"
						name="db-host"
						type="text"
						autocomplete="off"
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
						type="number"
						min="1"
						max="65535"
						autocomplete="off"
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
						type="text"
						autocomplete="off"
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
						type="text"
						autocomplete="off"
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
				<div>
					<label for="db-runtime" class="mb-1 block text-sm font-medium text-parchment-200">
						Runtime
					</label>
					<select
						id="db-runtime"
						name="db-runtime"
						bind:value={cfg.database.runtime}
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					>
						<option value="host">host</option>
						<option value="docker">docker</option>
						<option value="podman">podman</option>
						<option value="remote">remote</option>
					</select>
					<p class="mt-1 text-xs text-parchment-500">
						Where restores run psql: on this machine (host), inside a container (docker/podman), or
						not at all (remote — restore must be done manually on the remote host).
					</p>
				</div>
				{#if cfg.database.runtime === 'docker' || cfg.database.runtime === 'podman'}
					<div>
						<label for="db-container" class="mb-1 block text-sm font-medium text-parchment-200">
							Container name
						</label>
						<input
							id="db-container"
							name="db-container"
							type="text"
							autocomplete="off"
							bind:value={cfg.database.container}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
						/>
						<p class="mt-1 text-xs text-parchment-500">
							Required for docker/podman: the container running PostgreSQL, where psql is executed
							for restores.
						</p>
					</div>
				{/if}
			</div>
		</section>

		<section class="rounded-xl border border-clay-800 bg-clay-950/50 p-4">
			<h3 class="mb-3 text-sm font-semibold text-balance text-parchment-200">OCR</h3>
			<div class="space-y-3">
				<div>
					<label for="ocr-engine" class="mb-1 block text-sm font-medium text-parchment-200">
						Engine
					</label>
					<select
						id="ocr-engine"
						bind:value={cfg.consumer.ocr.engine}
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					>
						{#each cfg.available_engines.ocr as opt (opt.value)}
							<option value={opt.value}>{opt.label}</option>
						{/each}
					</select>
					{#if toolStatus?.find((t) => t.category === 'ocr' && !t.available)}
						<div
							class="mt-2 rounded-lg border border-terracotta-600 bg-terracotta-500/10 p-3 text-sm text-terracotta-500"
						>
							<p class="font-medium">&ldquo;{cfg.consumer.ocr.engine}&rdquo; is not installed</p>
							<p class="mt-1 text-parchment-400">
								Documents won't process until it is available. Install it, e.g.:
							</p>
							{#each Object.entries(hintsForEngine(cfg.consumer.ocr.engine)) as [system, cmd], i (i)}
								<pre class="text-parchment-300 mt-1 text-xs">{system}: {cmd}</pre>
							{/each}
						</div>
					{/if}
					{#if cfg.consumer.ocr.engine === 'ocrmypdf'}
						{@const ocrTool = toolStatus?.find((t) => t.engine === 'ocrmypdf')}
						{#if ocrTool?.lang_hints?.length}
							<div
								class="border-lapis-500/30 bg-lapis-500/10 mt-2 rounded-lg border p-3 text-sm text-parchment-200"
							>
								<p class="font-medium">Tesseract language packs required</p>
								<p class="text-parchment-300 mt-1">
									Install the packs for your configured languages ({ocrTool.languages.join(', ')}):
								</p>
								{#each Object.entries(ocrTool.lang_hints[0].install_hints) as [system, cmd], i (i)}
									<pre class="text-parchment-300 mt-1 text-xs">{system}: {cmd}</pre>
								{/each}
							</div>
						{/if}
						{#if ocrTool?.companions?.length}
							<div class="mt-2 space-y-2 text-sm">
								{#each ocrTool.companions as c (c.command)}
									{#if !c.available && c.required}
										<div
											class="rounded-lg border border-terracotta-600 bg-terracotta-500/10 p-3 text-terracotta-500"
										>
											<p class="font-medium">&ldquo;{c.command}&rdquo; not installed (required)</p>
											<p class="mt-1 text-parchment-400">{c.purpose}</p>
											{#each Object.entries(c.install_hints) as [system, cmd], i (i)}
												<pre class="text-parchment-300 mt-1 text-xs">{system}: {cmd}</pre>
											{/each}
										</div>
									{:else if !c.available}
										<div
											class="border-lapis-500/30 bg-lapis-500/10 text-parchment-300 rounded-lg border p-3"
										>
											<p class="font-medium text-parchment-200">
												&ldquo;{c.command}&rdquo; not installed (optional)
											</p>
											<p class="mt-1">{c.purpose}. ocrmypdf will skip this feature without it.</p>
											{#each Object.entries(c.install_hints) as [system, cmd], i (i)}
												<pre class="text-parchment-300 mt-1 text-xs">{system}: {cmd}</pre>
											{/each}
										</div>
									{/if}
								{/each}
							</div>
						{/if}
					{/if}
				</div>
				<div>
					<label for="ocr-timeout" class="mb-1 block text-sm font-medium text-parchment-200">
						Timeout (s)
					</label>
					<input
						id="ocr-timeout"
						name="ocr-timeout"
						type="number"
						min="0"
						autocomplete="off"
						bind:value={cfg.consumer.ocr.timeout}
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					/>
				</div>
				<div>
					<span class="mb-2 block text-sm font-medium text-parchment-200" id="ocr-languages-label"
						>Languages</span
					>
					{#each cfg.consumer.ocr.languages as lang, i (i)}
						<div class="mb-2 flex gap-2">
							<input
								id="ocr-language-{i}"
								type="text"
								aria-labelledby="ocr-languages-label"
								value={lang}
								oninput={(e) => updateLanguage(i, e.currentTarget.value)}
								placeholder="eng…"
								autocomplete="off"
								name="ocr-language-{i}"
								spellcheck="false"
								required
								class="flex-1 rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 placeholder-parchment-500 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
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
			</div>
		</section>

		<section class="rounded-xl border border-clay-800 bg-clay-950/50 p-4">
			<h3 class="mb-3 text-sm font-semibold text-balance text-parchment-200">Text Extractor</h3>
			<div class="grid gap-3 sm:grid-cols-2">
				<div>
					<label
						for="text-extractor-engine"
						class="mb-1 block text-sm font-medium text-parchment-200"
					>
						Engine
					</label>
					<select
						id="text-extractor-engine"
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
						class="mb-1 block text-sm font-medium text-parchment-200"
					>
						Timeout (s)
					</label>
					<input
						id="text-extractor-timeout"
						name="text-extractor-timeout"
						type="number"
						min="0"
						autocomplete="off"
						bind:value={cfg.consumer.textextractor.timeout}
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					/>
				</div>
			</div>
			{#if toolStatus?.find((t) => t.category === 'textextractor' && !t.available)}
				<div
					class="mt-3 rounded-lg border border-terracotta-600 bg-terracotta-500/10 p-3 text-sm text-terracotta-500"
				>
					<p class="font-medium">
						&ldquo;{cfg.consumer.textextractor.engine}&rdquo; is not installed
					</p>
					<p class="mt-1 text-parchment-400">
						Documents won't process until it is available. Install it, e.g.:
					</p>
					{#each Object.entries(hintsForEngine(cfg.consumer.textextractor.engine)) as [system, cmd], i (i)}
						<pre class="text-parchment-300 mt-1 text-xs">{system}: {cmd}</pre>
					{/each}
				</div>
			{/if}
		</section>

		<section class="rounded-xl border border-clay-800 bg-clay-950/50 p-4">
			<h3 class="mb-3 text-sm font-semibold text-balance text-parchment-200">PDF Optimizer</h3>
			<div class="grid gap-3 sm:grid-cols-2">
				<div>
					<label
						for="pdf-optimizer-engine"
						class="mb-1 block text-sm font-medium text-parchment-200"
					>
						Engine
					</label>
					<select
						id="pdf-optimizer-engine"
						bind:value={cfg.consumer.pdfoptimizer.engine}
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					>
						{#each cfg.available_engines.pdf_optimizer as opt (opt.value)}
							<option value={opt.value}>{opt.label}</option>
						{/each}
					</select>
				</div>
				<div>
					<label
						for="pdf-optimizer-fallback"
						class="mb-1 block text-sm font-medium text-parchment-200"
					>
						Fallback (optional)
					</label>
					<input
						id="pdf-optimizer-fallback"
						name="pdf-optimizer-fallback"
						type="text"
						autocomplete="off"
						bind:value={cfg.consumer.pdfoptimizer.fallback}
						placeholder="gs…"
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 placeholder-parchment-500 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					/>
				</div>
			</div>
			{#if toolStatus?.find((t) => t.category === 'pdfoptimizer' && !t.available)}
				<div
					class="mt-3 rounded-lg border border-terracotta-600 bg-terracotta-500/10 p-3 text-sm text-terracotta-500"
				>
					<p class="font-medium">
						&ldquo;{cfg.consumer.pdfoptimizer.engine}&rdquo; is not installed
					</p>
					<p class="mt-1 text-parchment-400">
						Documents won't process until it is available. Install it, e.g.:
					</p>
					{#each Object.entries(hintsForEngine(cfg.consumer.pdfoptimizer.engine)) as [system, cmd], i (i)}
						<pre class="text-parchment-300 mt-1 text-xs">{system}: {cmd}</pre>
					{/each}
				</div>
			{/if}
			<div class="mt-3 grid gap-3 sm:grid-cols-2">
				<div class="sm:col-span-2">
					<label
						for="pdf-optimizer-timeout"
						class="mb-1 block text-sm font-medium text-parchment-200"
					>
						Timeout (s)
					</label>
					<input
						id="pdf-optimizer-timeout"
						name="pdf-optimizer-timeout"
						type="number"
						min="0"
						autocomplete="off"
						bind:value={cfg.consumer.pdfoptimizer.timeout}
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					/>
				</div>
			</div>
		</section>

		<section class="rounded-xl border border-clay-800 bg-clay-950/50 p-4">
			<h3 class="mb-3 text-sm font-semibold text-balance text-parchment-200">General</h3>
			<div class="grid gap-3 sm:grid-cols-2">
				<div>
					<label for="consumer-workers" class="mb-1 block text-sm font-medium text-parchment-200">
						Workers
					</label>
					<input
						id="consumer-workers"
						name="consumer-workers"
						type="number"
						min="1"
						autocomplete="off"
						bind:value={cfg.consumer.workers}
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					/>
				</div>
			</div>
		</section>

		<section class="rounded-xl border border-clay-800 bg-clay-950/50 p-4">
			<h3 class="mb-3 text-sm font-semibold text-balance text-parchment-200">
				Supported file types
			</h3>
			<div class="flex flex-wrap gap-3">
				{#each mimeTypeOptions as opt (opt.mime_type)}
					<label class="text-parchment-300 flex items-center gap-1.5 text-sm">
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
		</section>

		<section class="rounded-xl border border-clay-800 bg-clay-950/50 p-4">
			<h3 class="mb-1 text-sm font-semibold text-balance text-parchment-200">DOCX/ODT Converter</h3>
			<p class="mb-3 text-xs text-parchment-400">
				Converts DOCX and ODT to PDF via LibreOffice. Requires
				<code class="text-parchment-300 text-xs" translate="no">libreoffice</code> installed on the system.
			</p>
			<div class="space-y-3">
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
						>Enable conversion</label
					>
				</div>
				<div>
					<label for="converter-binary" class="mb-1 block text-sm font-medium text-parchment-200">
						LibreOffice binary
					</label>
					<input
						id="converter-binary"
						name="converter-binary"
						type="text"
						bind:value={cfg.consumer.converter.binary}
						autocomplete="off"
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					/>
					<p class="mt-1 text-xs text-parchment-400">
						Command name on PATH or full path like
						<code class="text-parchment-300" translate="no">/opt/libreoffice/bin/soffice</code>
					</p>
				</div>
				<div>
					<label for="converter-timeout" class="mb-1 block text-sm font-medium text-parchment-200">
						Timeout (seconds)
					</label>
					<input
						id="converter-timeout"
						name="converter-timeout"
						type="number"
						min="1"
						bind:value={cfg.consumer.converter.timeout}
						autocomplete="off"
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					/>
				</div>
			</div>
		</section>

		<div class="flex min-w-0 gap-3">
			<button
				type="button"
				onclick={() => (step = 1)}
				class="flex-1 rounded-lg border border-clay-800 px-4 py-2 text-sm font-medium text-parchment-200 hover:bg-clay-800"
			>
				Back
			</button>
			<button
				type="submit"
				class="flex-1 rounded-lg bg-gold-500 px-4 py-2 text-sm font-medium text-clay-950 hover:bg-gold-600"
			>
				Continue
			</button>
		</div>
	</form>
{/if}

{#if step === 3 && cfg}
	<div class="mb-4 text-center">
		<p class="text-xs font-medium tracking-wide text-parchment-500 uppercase">Step 3 of 6</p>
		<h2 class="text-lg font-semibold text-balance text-parchment-200">Enricher Settings</h2>
	</div>

	<form onsubmit={submitSettings} class="touch-manipulation space-y-5">
		<section class="rounded-xl border border-clay-800 bg-clay-950/50 p-4">
			<h3 class="mb-3 text-sm font-semibold text-balance text-parchment-200">Tag Matcher</h3>
			<div class="grid gap-3 sm:grid-cols-2">
				<div>
					<label
						for="tag-matcher-timeout"
						class="mb-1 block text-sm font-medium text-parchment-200"
					>
						Timeout (s)
					</label>
					<input
						id="tag-matcher-timeout"
						name="tag-matcher-timeout"
						type="number"
						min="0"
						autocomplete="off"
						bind:value={cfg.enricher.tagmatcher.timeout}
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					/>
				</div>
				<div>
					<label
						for="tag-matcher-reduce-target"
						class="mb-1 block text-sm font-medium text-parchment-200"
					>
						Reduce target words
					</label>
					<input
						id="tag-matcher-reduce-target"
						name="tag-matcher-reduce-target"
						type="number"
						min="0"
						autocomplete="off"
						bind:value={cfg.enricher.tagmatcher.reduce_target_words}
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					/>
				</div>
				<div>
					<label
						for="tag-matcher-chunk-size"
						class="mb-1 block text-sm font-medium text-parchment-200"
					>
						Chunk size
					</label>
					<input
						id="tag-matcher-chunk-size"
						name="tag-matcher-chunk-size"
						type="number"
						min="0"
						autocomplete="off"
						bind:value={cfg.enricher.tagmatcher.chunk_size}
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					/>
				</div>
				<div>
					<label
						for="tag-matcher-hugot-model"
						class="mb-1 block text-sm font-medium text-parchment-200"
						translate="no"
					>
						Hugot model
					</label>
					<input
						id="tag-matcher-hugot-model"
						name="tag-matcher-hugot-model"
						type="text"
						autocomplete="off"
						bind:value={cfg.enricher.tagmatcher.hugot.model}
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					/>
				</div>
				<div>
					<label
						for="tag-matcher-hugot-backend"
						class="mb-1 block text-sm font-medium text-parchment-200"
					>
						Hugot backend
					</label>
					<select
						id="tag-matcher-hugot-backend"
						bind:value={cfg.enricher.tagmatcher.hugot.backend}
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					>
						<option value="ort">ort</option>
						<option value="GO">GO</option>
					</select>
				</div>
			</div>
		</section>

		<section class="rounded-xl border border-clay-800 bg-clay-950/50 p-4">
			<h3 class="mb-3 text-sm font-semibold text-balance text-parchment-200">Text Reducer</h3>
			<div class="grid gap-3 sm:grid-cols-2">
				<div>
					<label
						for="text-reducer-engine"
						class="mb-1 block text-sm font-medium text-parchment-200"
					>
						Engine
					</label>
					<select
						id="text-reducer-engine"
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
						class="mb-1 block text-sm font-medium text-parchment-200"
					>
						Timeout (s)
					</label>
					<input
						id="text-reducer-timeout"
						name="text-reducer-timeout"
						type="number"
						min="0"
						autocomplete="off"
						bind:value={cfg.enricher.textreducer.timeout}
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					/>
				</div>
				<div class="sm:col-span-2">
					<label
						for="text-reducer-target-words"
						class="mb-1 block text-sm font-medium text-parchment-200"
					>
						Target words
					</label>
					<input
						id="text-reducer-target-words"
						name="text-reducer-target-words"
						type="number"
						min="1"
						autocomplete="off"
						bind:value={cfg.enricher.textreducer.target_words}
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					/>
				</div>
			</div>
		</section>

		<section class="rounded-xl border border-clay-800 bg-clay-950/50 p-4">
			<h3 class="mb-3 text-sm font-semibold text-balance text-parchment-200">General</h3>
			<div>
				<label for="enricher-workers" class="mb-1 block text-sm font-medium text-parchment-200">
					Workers
				</label>
				<input
					id="enricher-workers"
					name="enricher-workers"
					type="number"
					min="1"
					autocomplete="off"
					bind:value={cfg.enricher.workers}
					class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
				/>
			</div>
		</section>

		<div class="flex min-w-0 gap-3">
			<button
				type="button"
				onclick={() => (step = 2)}
				class="flex-1 rounded-lg border border-clay-800 px-4 py-2 text-sm font-medium text-parchment-200 hover:bg-clay-800"
			>
				Back
			</button>
			<button
				type="submit"
				disabled={saving}
				class="flex-1 rounded-lg bg-gold-500 px-4 py-2 text-sm font-medium text-clay-950 hover:bg-gold-600 disabled:cursor-not-allowed disabled:opacity-50"
			>
				{#if saving}
					<span
						class="mr-2 inline-block h-4 w-4 animate-spin rounded-full border-2 border-clay-950 border-t-transparent align-text-bottom motion-reduce:animate-none"
					></span>
					Saving…
				{:else}
					Save and Continue
				{/if}
			</button>
		</div>
	</form>
{/if}

{#if step === 4}
	<div class="space-y-4 text-center">
		<p class="text-xs font-medium tracking-wide text-parchment-500 uppercase">Step 4 of 6</p>
		<h2 class="text-lg font-semibold text-parchment-200">Setting Things Up…</h2>
		<p class="text-sm text-parchment-500">
			Downloading required models and language files. This may take a few minutes.
		</p>
		<div
			class="mx-auto h-8 w-8 animate-spin rounded-full border-2 border-clay-800 border-t-gold-500 motion-reduce:animate-none"
		></div>
		<p class="text-sm text-parchment-400" aria-live="polite">
			{pendingTasks}
			{pendingTasks === 1 ? 'task' : 'tasks'} remaining
		</p>

		{#if failedTasks.length > 0}
			<div
				class="rounded-lg border border-terracotta-600 bg-terracotta-500/10 p-4 text-left text-sm"
				aria-live="polite"
			>
				<p class="mb-2 font-medium text-terracotta-500">Some downloads failed</p>
				<ul class="space-y-2">
					{#each failedTasks as ft (ft.op + ft.lang)}
						<li class="text-parchment-300">
							<span class="font-medium text-parchment-200"
								>{ft.op}{#if ft.lang}
									({ft.lang}){/if}</span
							>
							: {ft.error}
						</li>
					{/each}
				</ul>
				<button
					type="button"
					onclick={retryFailed}
					class="mt-3 rounded-lg bg-terracotta-600 px-4 py-2 text-sm font-medium text-white hover:bg-terracotta-500"
				>
					Retry failed downloads
				</button>
			</div>
		{/if}
	</div>
{/if}

{#if step === 5}
	<div class="mb-4 text-center">
		<p class="text-xs font-medium tracking-wide text-parchment-500 uppercase">Step 5 of 6</p>
		<h2 class="text-lg font-semibold text-parchment-200">Create Admin User</h2>
	</div>

	{#if adminError}
		<div
			aria-live="polite"
			class="mb-4 rounded-lg border border-terracotta-600 bg-terracotta-500/10 p-3 text-sm text-terracotta-500"
		>
			{adminError}
		</div>
	{/if}

	<form onsubmit={createAdminUser} class="touch-manipulation space-y-4">
		<div>
			<label for="admin-username" class="mb-1 block text-sm font-medium text-parchment-200">
				Username
			</label>
			<input
				id="admin-username"
				name="admin-username"
				type="text"
				bind:value={adminUsername}
				autocomplete="username"
				spellcheck={false}
				aria-invalid={!!usernameError}
				required
				class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 placeholder-parchment-500 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
			/>
			{#if usernameError}
				<p class="mt-1 text-xs text-terracotta-500" role="alert">{usernameError}</p>
			{/if}
		</div>
		<div>
			<label for="admin-password" class="mb-1 block text-sm font-medium text-parchment-200">
				Password
			</label>
			<input
				id="admin-password"
				name="admin-password"
				type="password"
				bind:value={adminPassword}
				autocomplete="new-password"
				aria-invalid={!!passwordError}
				minlength="12"
				required
				class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 placeholder-parchment-500 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
			/>
			{#if passwordError}
				<p class="mt-1 text-xs text-terracotta-500" role="alert">{passwordError}</p>
			{:else}
				<p class="mt-1 text-xs text-parchment-500">
					At least 12 characters with uppercase, lowercase, digit, and special character.
				</p>
			{/if}
		</div>
		<div>
			<label for="admin-confirm" class="mb-1 block text-sm font-medium text-parchment-200">
				Confirm password
			</label>
			<input
				id="admin-confirm"
				name="admin-confirm"
				type="password"
				bind:value={adminConfirm}
				autocomplete="new-password"
				aria-invalid={!!confirmError}
				required
				class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 placeholder-parchment-500 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
			/>
			{#if confirmError}
				<p class="mt-1 text-xs text-terracotta-500" role="alert">{confirmError}</p>
			{/if}
		</div>

		<div class="flex min-w-0 gap-3">
			<button
				type="button"
				onclick={() => (step = 4)}
				class="flex-1 rounded-lg border border-clay-800 px-4 py-2 text-sm font-medium text-parchment-200 hover:bg-clay-800"
			>
				Back
			</button>
			<button
				type="button"
				onclick={() => (step = 6)}
				class="flex-1 rounded-lg border border-clay-800 px-4 py-2 text-sm font-medium text-parchment-400 hover:bg-clay-800 hover:text-parchment-200"
			>
				Skip for now
			</button>
			<button
				type="submit"
				disabled={adminCreating}
				class="flex-1 rounded-lg bg-gold-500 px-4 py-2 text-sm font-medium text-clay-950 hover:bg-gold-600 disabled:cursor-not-allowed disabled:opacity-50"
			>
				{#if adminCreating}
					<span
						class="inline-block h-4 w-4 animate-spin rounded-full border-2 border-clay-950 border-t-transparent align-text-bottom motion-reduce:animate-none"
					></span>
					Creating…
				{:else}
					Create
				{/if}
			</button>
		</div>
	</form>
{/if}

{#if step === 6}
	<div class="space-y-4 text-center">
		<h2 class="text-lg font-semibold text-parchment-200">Setup complete</h2>

		{#if serviceFilesPath}
			<div class="rounded-lg border border-clay-800 bg-clay-950/50 p-4 text-left text-sm">
				<p class="mb-3 font-medium text-parchment-200">Install and start as system services:</p>
				<pre
					class="text-parchment-300 mb-2 overflow-x-auto rounded bg-clay-950 px-3 py-2 text-xs break-words">
sudo cp {serviceFilesPath}/* /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now edub-kushim.target</pre>
				<p class="text-xs text-parchment-500">
					This starts all three services (matcher, queue daemon, API server) and ensures they
					restart after reboot.
				</p>
			</div>

			<div class="rounded-lg border border-clay-800 bg-clay-950/50 p-4 text-left text-sm">
				<p class="mb-1 font-medium text-parchment-200">Check status:</p>
				<pre class="text-parchment-300 rounded bg-clay-950 px-3 py-2 text-xs">
sudo systemctl status edub-kushim.target</pre>
			</div>
		{:else}
			<p class="text-sm text-parchment-500">
				Run <code class="rounded bg-clay-800 px-1 py-0.5 text-parchment-200" translate="no"
					>edub</code
				>
				to start the server.
			</p>
		{/if}

		{#if missingTools.length > 0}
			<div
				class="rounded-lg border border-terracotta-600 bg-terracotta-500/10 p-4 text-left text-sm text-terracotta-500"
			>
				<p class="font-medium text-parchment-200">
					Setup complete — but the following tools are not installed and must be installed before
					you can consume documents:
				</p>
				<ul class="mt-2 list-inside list-disc space-y-2">
					{#each missingTools as t (t.engine)}
						{#if t.engine === 'curl'}
							<li>curl (required for downloads)</li>
						{:else}
							<li>{t.engine} ({t.category} engine)</li>
						{/if}
						{#each Object.entries(hintsForEngine(t.engine)) as [system, cmd], i (i)}
							<li class="text-parchment-300 ml-4 list-none text-xs">{system}: {cmd}</li>
						{/each}
						{#if t.companions}
							{#each t.companions as c (c.command)}
								{#if c.required && !c.available}
									<li class="ml-2">{c.command} (required companion — {c.purpose})</li>
									{#each Object.entries(c.install_hints) as [system, cmd], i (i)}
										<li class="text-parchment-300 ml-6 list-none text-xs">{system}: {cmd}</li>
									{/each}
								{/if}
							{/each}
						{/if}
					{/each}
				</ul>
			</div>
		{/if}

		{#if cfg?.consumer?.ocr?.engine === 'ocrmypdf'}
			{@const ocrTool = toolStatus?.find((t) => t.engine === 'ocrmypdf')}
			{#if ocrTool?.lang_hints?.length}
				<div
					class="border-lapis-500/30 bg-lapis-500/10 rounded-lg border p-4 text-left text-sm text-parchment-200"
				>
					<p class="font-medium">Reminder: ocrmypdf needs the tesseract language packs</p>
					<p class="text-parchment-300 mt-1">
						Install the packs for your configured languages ({ocrTool.languages.join(', ')}):
					</p>
					{#each Object.entries(ocrTool.lang_hints[0].install_hints) as [system, cmd], i (i)}
						<pre class="text-parchment-300 mt-1 text-xs">{system}: {cmd}</pre>
					{/each}
				</div>
			{/if}
		{/if}

		<button
			type="button"
			onclick={() => window.close()}
			class="rounded-lg bg-gold-500 px-4 py-2 text-sm font-medium text-clay-950 hover:bg-gold-600"
		>
			Close
		</button>
	</div>
{/if}
