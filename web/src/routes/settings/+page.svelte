<script>
	import { onMount } from 'svelte';
	import { api } from '$lib/api.js';

	let cfg = $state(null);
	let saving = $state(false);
	let message = $state('');
	let error = $state('');
	let pendingTasks = $state(0);
	let pollInterval;

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
			'enricher.tagmatcher.engine': cfg.enricher.tagmatcher.engine,
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
		message = '';
		error = '';
		try {
			const res = await api.config.update(bodyFromConfig());
			if (res && 'pending_tasks' in res && res.pending_tasks > 0) {
				pendingTasks = res.pending_tasks;
				message = 'Settings saved. Downloads in progress...';
				startPolling();
			} else {
				message = 'Settings saved.';
			}
			const loaded = await api.config.get();
			if (loaded) cfg = loaded;
		} catch (e) {
			error = e.message;
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

		{#if message}
			<div class="rounded-lg border border-gold-500/30 bg-gold-500/10 p-3 text-sm text-gold-500">
				{message}
			</div>
		{/if}
		{#if error}
			<div
				class="rounded-lg border border-terracotta-600 bg-terracotta-500/10 p-3 text-sm text-terracotta-500"
			>
				{error}
			</div>
		{/if}

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
						{#each cfg.available_engines.ocr as opt}
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
			<div class="mt-4">
				<span class="mb-2 block text-sm font-medium text-parchment-200">Languages</span>
				{#each cfg.consumer.ocr.languages as lang, i}
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
				<div>
					<label for="pdf-engine" class="mb-1 block text-sm font-medium text-parchment-200"
						>PDF optimizer</label
					>
					<select
						id="pdf-engine"
						bind:value={cfg.consumer.pdfoptimizer.engine}
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
					>
						{#each cfg.available_engines.pdf_optimizer as opt}
							<option value={opt.value}>{opt.label}</option>
						{/each}
					</select>
				</div>
				<div>
					<label for="text-extractor" class="mb-1 block text-sm font-medium text-parchment-200"
						>Text extractor</label
					>
					<select
						id="text-extractor"
						bind:value={cfg.consumer.textextractor.engine}
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
					>
						{#each cfg.available_engines.text_extractor as opt}
							<option value={opt.value}>{opt.label}</option>
						{/each}
					</select>
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
				<div>
					<label
						for="content-analyzer-engine"
						class="mb-1 block text-sm font-medium text-parchment-200">Content analyzer</label
					>
					<select
						id="content-analyzer-engine"
						bind:value={cfg.enricher.contentanalyzer.engine}
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
					>
						{#each cfg.available_engines.content_analyzer as opt}
							<option value={opt.value}>{opt.label}</option>
						{/each}
					</select>
				</div>
				<div>
					<label for="tag-matcher-engine" class="mb-1 block text-sm font-medium text-parchment-200"
						>Tag matcher</label
					>
					<select
						id="tag-matcher-engine"
						bind:value={cfg.enricher.tagmatcher.engine}
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
					>
						{#each cfg.available_engines.tag_matcher as opt}
							<option value={opt.value}>{opt.label}</option>
						{/each}
					</select>
				</div>
				<div>
					<label for="text-reducer-engine" class="mb-1 block text-sm font-medium text-parchment-200"
						>Text reducer</label
					>
					<select
						id="text-reducer-engine"
						bind:value={cfg.enricher.textreducer.engine}
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
					>
						{#each cfg.available_engines.text_reducer as opt}
							<option value={opt.value}>{opt.label}</option>
						{/each}
					</select>
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
