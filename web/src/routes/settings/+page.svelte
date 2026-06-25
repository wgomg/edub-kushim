<script>
	import { onMount } from 'svelte';
	import { api } from '$lib/api.js';
	import { hintsForEngine } from '$lib/tools.js';
	import { toastStore } from '$lib/stores/toastStore.svelte.js';

	let cfg = $state(null);
	let saving = $state(false);
	let pendingTasks = $state(0);
	let missingTools = $state([]);
	let toolStatus = $state([]);
	let pollInterval;
	let showToken = $state(false);

	let providerKey = $derived(cfg?.enricher?.contentanalyzer?.engine?.replace(/^llm/, '') ?? null);

	onMount(async () => {
		const loaded = await api.config.get();
		if (loaded) {
			cfg = loaded;
			checkStatus();
		}
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

	function bodyFromConfig() {
		return {
			'server.host': cfg.server.host,
			'server.port': Number(cfg.server.port),
			'server.max_upload_size': Number(cfg.server.max_upload_size),
			'server.max_download_files': Number(cfg.server.max_download_files),
			'server.max_download_size_mb': Number(cfg.server.max_download_size_mb),
			'consumer.ocr.engine': cfg.consumer.ocr.engine,
			'consumer.ocr.languages': cfg.consumer.ocr.languages.filter(Boolean),
			'consumer.ocr.data_dir': cfg.consumer.ocr.data_dir,
			'consumer.ocr.timeout': Number(cfg.consumer.ocr.timeout),
			'consumer.workers': Number(cfg.consumer.workers),
			'consumer.delete_original': cfg.consumer.delete_original,
			'consumer.pdfoptimizer.engine': cfg.consumer.pdfoptimizer.engine,
			'consumer.pdfoptimizer.fallback': cfg.consumer.pdfoptimizer.fallback,
			'consumer.pdfoptimizer.timeout': Number(cfg.consumer.pdfoptimizer.timeout),
			'consumer.textextractor.engine': cfg.consumer.textextractor.engine,
			'consumer.textextractor.timeout': Number(cfg.consumer.textextractor.timeout),
			'enricher.workers': Number(cfg.enricher.workers),
			'enricher.textreducer.engine': cfg.enricher.textreducer.engine,
			'enricher.textreducer.timeout': Number(cfg.enricher.textreducer.timeout),
			'enricher.textreducer.target_words': Number(cfg.enricher.textreducer.target_words),
			'enricher.contentanalyzer.engine': cfg.enricher.contentanalyzer.engine,
			'enricher.contentanalyzer.timeout': Number(cfg.enricher.contentanalyzer.timeout),
			...(providerKey
				? {
						[`enricher.contentanalyzer.llm.${providerKey}.base_url`]:
							cfg.enricher.contentanalyzer.llm[providerKey].base_url,
						[`enricher.contentanalyzer.llm.${providerKey}.model`]:
							cfg.enricher.contentanalyzer.llm[providerKey].model,
						[`enricher.contentanalyzer.llm.${providerKey}.token`]:
							cfg.enricher.contentanalyzer.llm[providerKey].token
					}
				: {}),
			'enricher.tagmatcher.timeout': Number(cfg.enricher.tagmatcher.timeout),
			'enricher.tagmatcher.reduce_target_words': Number(
				cfg.enricher.tagmatcher.reduce_target_words
			),
			'enricher.tagmatcher.chunk_size': Number(cfg.enricher.tagmatcher.chunk_size),
			'enricher.tagmatcher.hugot.model': cfg.enricher.tagmatcher.hugot.model,
			'enricher.tagmatcher.hugot.backend': cfg.enricher.tagmatcher.hugot.backend
		};
	}

	async function save() {
		saving = true;
		try {
			const res = await api.config.update(bodyFromConfig());
			if (res && 'pending_tasks' in res && res.pending_tasks > 0) {
				pendingTasks = res.pending_tasks;
				toastStore.success('Settings saved. Downloads in progress...');
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
</script>

{#if !cfg}
	<div class="text-parchment-500">Loading settings...</div>
{:else}
	<div class="mx-auto max-w-3xl space-y-6">
		<div class="flex items-center justify-between">
			<h1 class="text-2xl font-bold text-parchment-200">Settings</h1>
			{#if pendingTasks > 0}
				<div class="flex items-center gap-2 text-sm text-gold-500">
					<div
						class="h-4 w-4 animate-spin rounded-full border-2 border-clay-800 border-t-gold-500"
					></div>
					{pendingTasks} task(s) pending
				</div>
			{/if}
		</div>

		{#if missingTools?.find((t) => t.engine === 'curl')}
			<div
				class="rounded-lg border border-terracotta-600 bg-terracotta-500/10 p-3 text-sm text-terracotta-500"
			>
				<p class="font-medium">"curl" not installed (required for downloads)</p>
				<p class="mt-1 text-parchment-400">
					Model and language file downloads will fail without curl.
				</p>
				{#each Object.entries(hintsForEngine('curl')) as [system, cmd]}
					<pre class="text-parchment-300 mt-1 text-xs">{system}: {cmd}</pre>
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
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
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
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
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
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
					/>
				</div>
				<div>
					<label for="server-max-download-files" class="mb-1 block text-sm font-medium text-parchment-200"
						>Max download files</label
					>
					<input
						id="server-max-download-files"
						type="number"
						min="1"
						bind:value={cfg.server.max_download_files}
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
					/>
				</div>
				<div>
					<label for="server-max-download-size" class="mb-1 block text-sm font-medium text-parchment-200"
						>Max download size (MB)</label
					>
					<input
						id="server-max-download-size"
						type="number"
						min="0"
						bind:value={cfg.server.max_download_size_mb}
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
					/>
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
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
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
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
					/>
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
						<pre class="text-parchment-300 mt-1 text-xs">{system}: {cmd}</pre>
					{/each}
				</div>
			{/if}
			{#if cfg.consumer.ocr.engine === 'ocrmypdf'}
				{@const ocrTool = toolStatus?.find((t) => t.engine === 'ocrmypdf')}
				{#if ocrTool?.lang_hints?.length}
					<div
						class="border-lapis-500/30 bg-lapis-500/10 mt-4 rounded-lg border p-3 text-sm text-parchment-200"
					>
						<p class="font-medium">Tesseract language packs required</p>
						<p class="text-parchment-300 mt-1">
							Install the packs for your configured languages ({ocrTool.languages.join(', ')}):
						</p>
						{#each Object.entries(ocrTool.lang_hints[0].install_hints) as [system, cmd]}
							<pre class="text-parchment-300 mt-1 text-xs">{system}: {cmd}</pre>
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
										<pre class="text-parchment-300 mt-1 text-xs">{system}: {cmd}</pre>
									{/each}
								</div>
							{:else if !c.available}
								<div
									class="border-lapis-500/30 bg-lapis-500/10 text-parchment-300 rounded-lg border p-3"
								>
									<p class="font-medium text-parchment-200">
										"{c.command}" not installed (optional)
									</p>
									<p class="mt-1">{c.purpose}. ocrmypdf will skip this feature without it.</p>
									{#each Object.entries(c.install_hints) as [system, cmd]}
										<pre class="text-parchment-300 mt-1 text-xs">{system}: {cmd}</pre>
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
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
					/>
				</div>
			</div>
			<div class="mt-4">
				<span class="mb-2 block text-sm font-medium text-parchment-200">Languages</span>
				{#each cfg.consumer.ocr.languages as lang, i (i)}
					<div class="mb-2 flex gap-2">
						<input
							type="text"
							value={lang}
							oninput={(e) => updateLanguage(i, e.currentTarget.value)}
							placeholder="eng"
							class="flex-1 rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 placeholder-parchment-500 focus:border-gold-500 focus:outline-none"
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
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
					/>
				</div>
				<div class="flex items-center gap-2">
					<input
						id="delete-original"
						type="checkbox"
						bind:checked={cfg.consumer.delete_original}
						class="rounded border-clay-800 bg-clay-950 text-gold-500 focus:ring-gold-500"
					/>
					<label for="delete-original" class="text-sm text-parchment-200"
						>Delete original files after processing</label
					>
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
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
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
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
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
						<pre class="text-parchment-300 mt-1 text-xs">{system}: {cmd}</pre>
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
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
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
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 placeholder-parchment-500 focus:border-gold-500 focus:outline-none"
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
						<pre class="text-parchment-300 mt-1 text-xs">{system}: {cmd}</pre>
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
						min="1"
						bind:value={cfg.consumer.pdfoptimizer.timeout}
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
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
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
					/>
				</div>
			</div>
		</section>

		<section class="rounded-xl border border-clay-800 bg-clay-900 p-5">
			<h2 class="mb-4 text-lg font-semibold text-parchment-200">Content analyzer (LLM)</h2>
			<div class="grid gap-4 sm:grid-cols-2">
				<div>
					<label
						for="content-analyzer-engine"
						class="mb-1 block text-sm font-medium text-parchment-200">Engine</label
					>
					<select
						id="content-analyzer-engine"
						bind:value={cfg.enricher.contentanalyzer.engine}
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
					>
						{#each cfg.available_engines.content_analyzer as opt (opt.value)}
							<option value={opt.value}>{opt.label}</option>
						{/each}
					</select>
				</div>
				<div>
					<label
						for="content-analyzer-timeout"
						class="mb-1 block text-sm font-medium text-parchment-200">Timeout (s)</label
					>
					<input
						id="content-analyzer-timeout"
						type="number"
						min="1"
						bind:value={cfg.enricher.contentanalyzer.timeout}
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
					/>
				</div>
			</div>

			{#if providerKey}
				<div class="mt-4 rounded-lg border border-clay-800 bg-clay-950 p-4">
					<h3 class="mb-3 text-sm font-semibold text-parchment-200 capitalize">
						{providerKey} provider
					</h3>
					<div class="grid gap-4 sm:grid-cols-2">
						<div class="sm:col-span-2">
							<label
								for="llm-{providerKey}-base-url"
								class="mb-1 block text-sm font-medium text-parchment-200">Base URL</label
							>
							<input
								id="llm-{providerKey}-base-url"
								type="text"
								bind:value={cfg.enricher.contentanalyzer.llm[providerKey].base_url}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
							/>
						</div>
						<div class="sm:col-span-2">
							<label
								for="llm-{providerKey}-model"
								class="mb-1 block text-sm font-medium text-parchment-200">Model</label
							>
							<input
								id="llm-{providerKey}-model"
								type="text"
								bind:value={cfg.enricher.contentanalyzer.llm[providerKey].model}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
							/>
						</div>
						<div class="sm:col-span-2">
							<label
								for="llm-{providerKey}-token"
								class="mb-1 block text-sm font-medium text-parchment-200">Token</label
							>
							<div class="flex gap-2">
								<input
									id="llm-{providerKey}-token"
									type={showToken ? 'text' : 'password'}
									bind:value={cfg.enricher.contentanalyzer.llm[providerKey].token}
									placeholder="sk-..."
									class="flex-1 rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 placeholder-parchment-500 focus:border-gold-500 focus:outline-none"
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
				</div>
			{/if}
		</section>

		<section class="rounded-xl border border-clay-800 bg-clay-900 p-5">
			<h2 class="mb-4 text-lg font-semibold text-parchment-200">Tag matcher</h2>
			<div class="grid gap-4 sm:grid-cols-2">
				<div>
					<label for="tag-matcher-timeout" class="mb-1 block text-sm font-medium text-parchment-200"
						>Timeout (s)</label
					>
					<input
						id="tag-matcher-timeout"
						type="number"
						min="1"
						bind:value={cfg.enricher.tagmatcher.timeout}
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
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
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
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
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
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
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
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
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
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
					<label for="text-reducer-engine" class="mb-1 block text-sm font-medium text-parchment-200"
						>Engine</label
					>
					<select
						id="text-reducer-engine"
						bind:value={cfg.enricher.textreducer.engine}
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
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
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
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
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
					/>
				</div>
			</div>
		</section>

		<button
			type="button"
			onclick={save}
			disabled={saving}
			class="w-full rounded-lg bg-gold-500 px-4 py-2 text-sm font-medium text-clay-950 hover:bg-gold-600 disabled:opacity-50"
		>
			{saving ? 'Saving...' : 'Save settings'}
		</button>
	</div>
{/if}
