<script>
	import { onMount } from 'svelte';
	import { configApi } from '$lib/api.js';
	import { hintsForEngine } from '$lib/tools.js';

	let step = $state(1);
	let configDir = $state('');
	let cfg = $state(null);
	let pendingTasks = $state(0);
	let configured = $state(false);
	let error = $state('');
	let missingTools = $state([]);
	let toolStatus = $state([]);
	let pollInterval;
	let showToken = $state(false);

	let providerKey = $derived(cfg?.enricher?.contentanalyzer?.engine?.replace(/^llm/, '') ?? null);

	onMount(async () => {
		try {
			const loaded = await configApi.get();
			cfg = loaded;
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
		try {
			await configApi.update({ config_dir: configDir });
			const loaded = await configApi.get();
			if (loaded) cfg = loaded;
			step = 2;
		} catch (e) {
			error = e.message;
		}
	}

	async function submitSettings(e) {
		e.preventDefault();
		error = '';
		try {
			const body = buildConfigBody();
			const res = await configApi.update(body);
			const loaded = await configApi.get();
			if (loaded) cfg = loaded;
			if (res && 'missing_tools' in res) {
				missingTools = res.missing_tools;
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
		}
	}

	function buildConfigBody() {
		return {
			'server.port': Number(cfg.server.port),
			'consumer.ocr.engine': cfg.consumer.ocr.engine,
			'consumer.ocr.languages': cfg.consumer.ocr.languages.filter(Boolean),
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
			...(providerKey ? {
				[`enricher.contentanalyzer.llm.${providerKey}.base_url`]: cfg.enricher.contentanalyzer.llm[providerKey].base_url,
				[`enricher.contentanalyzer.llm.${providerKey}.model`]: cfg.enricher.contentanalyzer.llm[providerKey].model,
				[`enricher.contentanalyzer.llm.${providerKey}.token`]: cfg.enricher.contentanalyzer.llm[providerKey].token,
			} : {}),
			'enricher.tagmatcher.engine': cfg.enricher.tagmatcher.engine,
			'enricher.tagmatcher.timeout': Number(cfg.enricher.tagmatcher.timeout),
			'enricher.tagmatcher.reduce_target_words': Number(cfg.enricher.tagmatcher.reduce_target_words),
			'enricher.tagmatcher.chunk_size': Number(cfg.enricher.tagmatcher.chunk_size),
			'enricher.tagmatcher.hugot.model': cfg.enricher.tagmatcher.hugot.model,
			'enricher.tagmatcher.hugot.backend': cfg.enricher.tagmatcher.hugot.backend
		};
	}

	async function checkStatus() {
		const status = await configApi.status();
		pendingTasks = status.pending_tasks;
		configured = status.configured;
		missingTools = status.missing_tools ?? [];
		toolStatus = status.tools ?? [];
		return status;
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
		cfg.consumer.ocr.languages = cfg.consumer.ocr.languages.filter((_, i) => i !== index);
	}

	function updateLanguage(index, value) {
		cfg.consumer.ocr.languages = cfg.consumer.ocr.languages.map((lang, i) =>
			i === index ? value : lang
		);
	}
</script>

{#if error}
	<div class="mb-4 rounded-lg border border-terracotta-600 bg-terracotta-500/10 p-3 text-sm text-terracotta-500">
		{error}
	</div>
{/if}

{#if !cfg && step !== 1}
	<div class="text-center text-sm text-parchment-500">Loading configuration...</div>
{/if}

{#if step === 1}
	<form onsubmit={submitConfigDir} class="space-y-4">
		<div>
			<label for="config-dir" class="mb-1 block text-sm font-medium text-parchment-200">
				Configuration directory
			</label>
			<input
				id="config-dir"
				type="text"
				bind:value={configDir}
				placeholder="/home/user/.config/edub-kushim"
				required
				class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 placeholder-parchment-500 focus:border-gold-500 focus:outline-none"
			/>
			<p class="mt-1 text-xs text-parchment-500">
				Where config.yaml, database, and downloaded models will be stored.
			</p>
		</div>
		<button
			type="submit"
			class="w-full rounded-lg bg-gold-500 px-4 py-2 text-sm font-medium text-clay-950 hover:bg-gold-600"
		>
			Continue
		</button>
	</form>
{/if}

{#if step === 2 && cfg}
	<div class="mb-4 text-center">
		<p class="text-xs font-medium uppercase tracking-wide text-parchment-500">Step 2 of 5</p>
		<h2 class="text-lg font-semibold text-parchment-200">Consumer settings</h2>
	</div>

	{#if missingTools?.find(t => t.engine === 'curl')}
		<div class="mb-4 rounded-lg border border-terracotta-600 bg-terracotta-500/10 p-3 text-sm text-terracotta-500">
			<p class="font-medium">"curl" not installed (required for downloads)</p>
			<p class="mt-1 text-parchment-400">Model and language file downloads will fail without curl.</p>
			{#each Object.entries(hintsForEngine('curl')) as [system, cmd]}
				<pre class="mt-1 text-xs text-parchment-300">{system}: {cmd}</pre>
			{/each}
		</div>
	{/if}

	<form onsubmit={(e) => { e.preventDefault(); step = 3; }} class="space-y-5">
		<section class="rounded-xl border border-clay-800 bg-clay-950/50 p-4">
			<h3 class="mb-3 text-sm font-semibold text-parchment-200">Server</h3>
			<div>
				<label for="server-port" class="mb-1 block text-sm font-medium text-parchment-200">
					edub server port
				</label>
				<input
					id="server-port"
					type="number"
					min="1"
					max="65535"
					bind:value={cfg.server.port}
					class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
				/>
			</div>
		</section>

		<section class="rounded-xl border border-clay-800 bg-clay-950/50 p-4">
			<h3 class="mb-3 text-sm font-semibold text-parchment-200">OCR</h3>
			<div class="space-y-3">
				<div>
					<label for="ocr-engine" class="mb-1 block text-sm font-medium text-parchment-200">
						Engine
					</label>
					<select
						id="ocr-engine"
						bind:value={cfg.consumer.ocr.engine}
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
					>
						{#each cfg.available_engines.ocr as opt (opt.value)}
							<option value={opt.value}>{opt.label}</option>
						{/each}
					</select>
					{#if toolStatus?.find(t => t.category === 'ocr' && !t.available)}
						<div class="mt-2 rounded-lg border border-terracotta-600 bg-terracotta-500/10 p-3 text-sm text-terracotta-500">
							<p class="font-medium">"{cfg.consumer.ocr.engine}" is not installed</p>
							<p class="mt-1 text-parchment-400">Documents won't process until it is available. Install it, e.g.:</p>
							{#each Object.entries(hintsForEngine(cfg.consumer.ocr.engine)) as [system, cmd]}
								<pre class="mt-1 text-xs text-parchment-300">{system}: {cmd}</pre>
							{/each}
						</div>
					{/if}
					{#if cfg.consumer.ocr.engine === 'ocrmypdf'}
						{@const ocrTool = toolStatus?.find(t => t.engine === 'ocrmypdf')}
						{#if ocrTool?.lang_hints?.length}
							<div class="mt-2 rounded-lg border border-lapis-500/30 bg-lapis-500/10 p-3 text-sm text-parchment-200">
								<p class="font-medium">Tesseract language packs required</p>
								<p class="mt-1 text-parchment-300">Install the packs for your configured languages
									({ocrTool.languages.join(', ')}):</p>
								{#each Object.entries(ocrTool.lang_hints[0].install_hints) as [system, cmd]}
									<pre class="mt-1 text-xs text-parchment-300">{system}: {cmd}</pre>
								{/each}
							</div>
						{/if}
						{#if ocrTool?.companions?.length}
							<div class="mt-2 space-y-2 text-sm">
								{#each ocrTool.companions as c}
									{#if !c.available && c.required}
										<div class="rounded-lg border border-terracotta-600 bg-terracotta-500/10 p-3 text-terracotta-500">
											<p class="font-medium">"{c.command}" not installed (required)</p>
											<p class="mt-1 text-parchment-400">{c.purpose}</p>
											{#each Object.entries(c.install_hints) as [system, cmd]}
												<pre class="mt-1 text-xs text-parchment-300">{system}: {cmd}</pre>
											{/each}
										</div>
									{:else if !c.available}
										<div class="rounded-lg border border-lapis-500/30 bg-lapis-500/10 p-3 text-parchment-300">
											<p class="font-medium text-parchment-200">"{c.command}" not installed (optional)</p>
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
				</div>
				<div>
					<label for="ocr-timeout" class="mb-1 block text-sm font-medium text-parchment-200">
						Timeout (s)
					</label>
					<input
						id="ocr-timeout"
						type="number"
						min="1"
						bind:value={cfg.consumer.ocr.timeout}
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
					/>
				</div>
				<div>
					<span class="mb-2 block text-sm font-medium text-parchment-200">Languages</span>
					{#each cfg.consumer.ocr.languages as lang, i (i)}
						<div class="mb-2 flex gap-2">
							<input
								type="text"
								value={lang}
								oninput={(e) => updateLanguage(i, e.currentTarget.value)}
								placeholder="eng"
								required
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
			</div>
		</section>

		<section class="rounded-xl border border-clay-800 bg-clay-950/50 p-4">
			<h3 class="mb-3 text-sm font-semibold text-parchment-200">Text extractor</h3>
			<div class="grid gap-3 sm:grid-cols-2">
				<div>
					<label for="text-extractor-engine" class="mb-1 block text-sm font-medium text-parchment-200">
						Engine
					</label>
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
					<label for="text-extractor-timeout" class="mb-1 block text-sm font-medium text-parchment-200">
						Timeout (s)
					</label>
					<input
						id="text-extractor-timeout"
						type="number"
						min="1"
						bind:value={cfg.consumer.textextractor.timeout}
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
					/>
				</div>
			</div>
			{#if toolStatus?.find(t => t.category === 'textextractor' && !t.available)}
				<div class="mt-3 rounded-lg border border-terracotta-600 bg-terracotta-500/10 p-3 text-sm text-terracotta-500">
					<p class="font-medium">"{cfg.consumer.textextractor.engine}" is not installed</p>
					<p class="mt-1 text-parchment-400">Documents won't process until it is available. Install it, e.g.:</p>
					{#each Object.entries(hintsForEngine(cfg.consumer.textextractor.engine)) as [system, cmd]}
						<pre class="mt-1 text-xs text-parchment-300">{system}: {cmd}</pre>
					{/each}
				</div>
			{/if}
		</section>

		<section class="rounded-xl border border-clay-800 bg-clay-950/50 p-4">
			<h3 class="mb-3 text-sm font-semibold text-parchment-200">PDF optimizer</h3>
			<div class="grid gap-3 sm:grid-cols-2">
				<div>
					<label for="pdf-optimizer-engine" class="mb-1 block text-sm font-medium text-parchment-200">
						Engine
					</label>
					<select
						id="pdf-optimizer-engine"
						bind:value={cfg.consumer.pdfoptimizer.engine}
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
					>
						{#each cfg.available_engines.pdf_optimizer as opt (opt.value)}
							<option value={opt.value}>{opt.label}</option>
						{/each}
					</select>
				</div>
				<div>
					<label for="pdf-optimizer-fallback" class="mb-1 block text-sm font-medium text-parchment-200">
						Fallback (optional)
					</label>
					<input
						id="pdf-optimizer-fallback"
						type="text"
						bind:value={cfg.consumer.pdfoptimizer.fallback}
						placeholder="gs"
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 placeholder-parchment-500 focus:border-gold-500 focus:outline-none"
					/>
				</div>
			</div>
			{#if toolStatus?.find(t => t.category === 'pdfoptimizer' && !t.available)}
				<div class="mt-3 rounded-lg border border-terracotta-600 bg-terracotta-500/10 p-3 text-sm text-terracotta-500">
					<p class="font-medium">"{cfg.consumer.pdfoptimizer.engine}" is not installed</p>
					<p class="mt-1 text-parchment-400">Documents won't process until it is available. Install it, e.g.:</p>
					{#each Object.entries(hintsForEngine(cfg.consumer.pdfoptimizer.engine)) as [system, cmd]}
						<pre class="mt-1 text-xs text-parchment-300">{system}: {cmd}</pre>
					{/each}
				</div>
			{/if}
			<div class="mt-3 grid gap-3 sm:grid-cols-2">
				<div class="sm:col-span-2">
					<label for="pdf-optimizer-timeout" class="mb-1 block text-sm font-medium text-parchment-200">
						Timeout (s)
					</label>
					<input
						id="pdf-optimizer-timeout"
						type="number"
						min="1"
						bind:value={cfg.consumer.pdfoptimizer.timeout}
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
					/>
				</div>
			</div>
		</section>

		<section class="rounded-xl border border-clay-800 bg-clay-950/50 p-4">
			<h3 class="mb-3 text-sm font-semibold text-parchment-200">General</h3>
			<div class="grid gap-3 sm:grid-cols-2">
				<div>
					<label for="consumer-workers" class="mb-1 block text-sm font-medium text-parchment-200">
						Workers
					</label>
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
					<label for="delete-original" class="text-sm text-parchment-200">
						Delete original files after processing
					</label>
				</div>
			</div>
		</section>

		<div class="flex gap-3">
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
		<p class="text-xs font-medium uppercase tracking-wide text-parchment-500">Step 3 of 5</p>
		<h2 class="text-lg font-semibold text-parchment-200">Enricher settings</h2>
	</div>

	<form onsubmit={submitSettings} class="space-y-5">
		<section class="rounded-xl border border-clay-800 bg-clay-950/50 p-4">
			<h3 class="mb-3 text-sm font-semibold text-parchment-200">Content analyzer (LLM)</h3>
			<div class="space-y-3">
				<div class="grid gap-3 sm:grid-cols-2">
					<div>
						<label for="content-analyzer-engine" class="mb-1 block text-sm font-medium text-parchment-200">
							Engine
						</label>
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
						<label for="content-analyzer-timeout" class="mb-1 block text-sm font-medium text-parchment-200">
							Timeout (s)
						</label>
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
					<div class="rounded-lg border border-clay-800 bg-clay-900 p-3">
						<h4 class="mb-2 text-sm font-medium capitalize text-parchment-200">{providerKey} provider</h4>
						<div class="space-y-3">
							<div>
								<label for="llm-{providerKey}-base-url" class="mb-1 block text-sm font-medium text-parchment-200">
									Base URL
								</label>
								<input
									id="llm-{providerKey}-base-url"
									type="text"
									bind:value={cfg.enricher.contentanalyzer.llm[providerKey].base_url}
									class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
								/>
							</div>
							<div>
								<label for="llm-{providerKey}-model" class="mb-1 block text-sm font-medium text-parchment-200">
									Model
								</label>
								<input
									id="llm-{providerKey}-model"
									type="text"
									bind:value={cfg.enricher.contentanalyzer.llm[providerKey].model}
									class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
								/>
							</div>
							<div>
								<label for="llm-{providerKey}-token" class="mb-1 block text-sm font-medium text-parchment-200">
									Token
								</label>
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
			</div>
		</section>

		<section class="rounded-xl border border-clay-800 bg-clay-950/50 p-4">
			<h3 class="mb-3 text-sm font-semibold text-parchment-200">Tag matcher</h3>
			<div class="grid gap-3 sm:grid-cols-2">
				<div>
					<label for="tag-matcher-engine" class="mb-1 block text-sm font-medium text-parchment-200">
						Engine
					</label>
					<select
						id="tag-matcher-engine"
						bind:value={cfg.enricher.tagmatcher.engine}
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
					>
						{#each cfg.available_engines.tag_matcher as opt (opt.value)}
							<option value={opt.value}>{opt.label}</option>
						{/each}
					</select>
				</div>
				<div>
					<label for="tag-matcher-timeout" class="mb-1 block text-sm font-medium text-parchment-200">
						Timeout (s)
					</label>
					<input
						id="tag-matcher-timeout"
						type="number"
						min="1"
						bind:value={cfg.enricher.tagmatcher.timeout}
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
					/>
				</div>
				<div>
					<label for="tag-matcher-reduce-target" class="mb-1 block text-sm font-medium text-parchment-200">
						Reduce target words
					</label>
					<input
						id="tag-matcher-reduce-target"
						type="number"
						min="0"
						bind:value={cfg.enricher.tagmatcher.reduce_target_words}
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
					/>
				</div>
				<div>
					<label for="tag-matcher-chunk-size" class="mb-1 block text-sm font-medium text-parchment-200">
						Chunk size
					</label>
					<input
						id="tag-matcher-chunk-size"
						type="number"
						min="0"
						bind:value={cfg.enricher.tagmatcher.chunk_size}
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
					/>
				</div>
				<div>
					<label for="tag-matcher-hugot-model" class="mb-1 block text-sm font-medium text-parchment-200">
						Hugot model
					</label>
					<input
						id="tag-matcher-hugot-model"
						type="text"
						bind:value={cfg.enricher.tagmatcher.hugot.model}
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
					/>
				</div>
				<div>
					<label for="tag-matcher-hugot-backend" class="mb-1 block text-sm font-medium text-parchment-200">
						Hugot backend
					</label>
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

		<section class="rounded-xl border border-clay-800 bg-clay-950/50 p-4">
			<h3 class="mb-3 text-sm font-semibold text-parchment-200">Text reducer</h3>
			<div class="grid gap-3 sm:grid-cols-2">
				<div>
					<label for="text-reducer-engine" class="mb-1 block text-sm font-medium text-parchment-200">
						Engine
					</label>
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
					<label for="text-reducer-timeout" class="mb-1 block text-sm font-medium text-parchment-200">
						Timeout (s)
					</label>
					<input
						id="text-reducer-timeout"
						type="number"
						min="1"
						bind:value={cfg.enricher.textreducer.timeout}
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
					/>
				</div>
				<div class="sm:col-span-2">
					<label for="text-reducer-target-words" class="mb-1 block text-sm font-medium text-parchment-200">
						Target words
					</label>
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

		<section class="rounded-xl border border-clay-800 bg-clay-950/50 p-4">
			<h3 class="mb-3 text-sm font-semibold text-parchment-200">General</h3>
			<div>
				<label for="enricher-workers" class="mb-1 block text-sm font-medium text-parchment-200">
					Workers
				</label>
				<input
					id="enricher-workers"
					type="number"
					min="1"
					bind:value={cfg.enricher.workers}
					class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
				/>
			</div>
		</section>

		<div class="flex gap-3">
			<button
				type="button"
				onclick={() => (step = 2)}
				class="flex-1 rounded-lg border border-clay-800 px-4 py-2 text-sm font-medium text-parchment-200 hover:bg-clay-800"
			>
				Back
			</button>
			<button
				type="submit"
				class="flex-1 rounded-lg bg-gold-500 px-4 py-2 text-sm font-medium text-clay-950 hover:bg-gold-600"
			>
				Save and continue
			</button>
		</div>
	</form>
{/if}

{#if step === 4}
	<div class="space-y-4 text-center">
		<p class="text-xs font-medium uppercase tracking-wide text-parchment-500">Step 4 of 5</p>
		<h2 class="text-lg font-semibold text-parchment-200">Setting things up...</h2>
		<p class="text-sm text-parchment-500">
			Downloading required models and language files. This may take a few minutes.
		</p>
		<div class="mx-auto h-8 w-8 animate-spin rounded-full border-2 border-clay-800 border-t-gold-500"></div>
		<p class="text-sm text-parchment-400">{pendingTasks} task(s) remaining</p>
	</div>
{/if}

{#if step === 5}
	<div class="space-y-4 text-center">
		<h2 class="text-lg font-semibold text-parchment-200">Setup complete</h2>
		<p class="text-sm text-parchment-500">
			Your configuration is ready. Run <code class="rounded bg-clay-800 px-1 py-0.5 text-parchment-200">edub</code>
			to start the server.
		</p>

		{#if missingTools.length > 0}
			<div class="rounded-lg border border-terracotta-600 bg-terracotta-500/10 p-4 text-left text-sm text-terracotta-500">
				<p class="font-medium text-parchment-200">Setup complete — but the following tools are not installed and must be
				installed before you can consume documents:</p>
				<ul class="mt-2 list-inside list-disc space-y-2">
					{#each missingTools as t}
						{#if t.engine === 'curl'}
							<li>curl (required for downloads)</li>
						{:else}
							<li>{t.engine} ({t.category} engine)</li>
						{/if}
						{#each Object.entries(hintsForEngine(t.engine)) as [system, cmd]}
							<li class="ml-4 list-none text-xs text-parchment-300">{system}: {cmd}</li>
						{/each}
						{#if t.companions}
							{#each t.companions as c}
							{#if c.required && !c.available}
								<li class="ml-2">{c.command} (required companion — {c.purpose})</li>
								{#each Object.entries(c.install_hints) as [system, cmd]}
									<li class="ml-6 list-none text-xs text-parchment-300">{system}: {cmd}</li>
								{/each}
							{/if}
							{/each}
						{/if}
					{/each}
					</ul>
			</div>
		{/if}

		{#if cfg?.consumer?.ocr?.engine === 'ocrmypdf'}
			{@const ocrTool = toolStatus?.find(t => t.engine === 'ocrmypdf')}
			{#if ocrTool?.lang_hints?.length}
				<div class="rounded-lg border border-lapis-500/30 bg-lapis-500/10 p-4 text-left text-sm text-parchment-200">
					<p class="font-medium">Reminder: ocrmypdf needs the tesseract language packs</p>
					<p class="mt-1 text-parchment-300">Install the packs for your configured languages ({ocrTool.languages.join(', ')}):</p>
					{#each Object.entries(ocrTool.lang_hints[0].install_hints) as [system, cmd]}
						<pre class="mt-1 text-xs text-parchment-300">{system}: {cmd}</pre>
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
