<script>
	import { onMount } from 'svelte';
	import { configApi } from '$lib/api.js';

	let step = $state(1);
	let configDir = $state('');
	let engines = $state(null);
	let settings = $state({
		ocr: { engine: 'gosseract', languages: ['eng'] },
		consumer: { workers: 1 },
		enricher: { workers: 1 }
	});
	let pendingTasks = $state(0);
	let error = $state('');
	let configured = $state(false);
	let pollInterval;

	onMount(async () => {
		try {
			const cfg = await configApi.get();
			if (cfg && cfg.app && cfg.app.config_dir) {
				configDir = cfg.app.config_dir;
				settings = restoreFormValues(cfg);
				await checkStatus();
				if (pendingTasks > 0) {
					step = 3;
					startPolling();
				} else if (configured) {
					step = 4;
				}
			}
			if (cfg && cfg.available_engines) {
				engines = cfg.available_engines;
			}
		} catch (e) {
			error = e.message;
		}
	});

	function restoreFormValues(cfg) {
		return {
			ocr: cfg.consumer?.ocr || { engine: 'gosseract', languages: ['eng'] },
			consumer: { workers: cfg.consumer?.workers || 1 },
			enricher: { workers: cfg.enricher?.workers || 1 }
		};
	}

	async function submitConfigDir() {
		error = '';
		try {
			await configApi.update({ config_dir: configDir });
			step = 2;
		} catch (e) {
			error = e.message;
		}
	}

	async function submitSettings() {
		error = '';
		try {
			const body = buildConfigBody();
			const res = await configApi.update(body);
			if (res && 'pending_tasks' in res && res.pending_tasks > 0) {
				pendingTasks = res.pending_tasks;
				step = 3;
				startPolling();
			} else {
				step = 4;
			}
		} catch (e) {
			error = e.message;
		}
	}

	function buildConfigBody() {
		return {
			'consumer.ocr.engine': settings.ocr.engine,
			'consumer.ocr.languages': settings.ocr.languages,
			'consumer.workers': settings.consumer.workers,
			'enricher.workers': settings.enricher.workers
		};
	}

	async function checkStatus() {
		const status = await configApi.status();
		pendingTasks = status.pending_tasks;
		configured = status.configured;
		return status;
	}

	function startPolling() {
		pollInterval = setInterval(async () => {
			try {
				const status = await checkStatus();
				if (status.pending_tasks === 0 && status.configured) {
					clearInterval(pollInterval);
					step = 4;
				}
			} catch (e) {
				error = e.message;
			}
		}, 3000);
	}

	function addLanguage() {
		settings.ocr.languages = [...settings.ocr.languages, ''];
	}

	function removeLanguage(index) {
		settings.ocr.languages = settings.ocr.languages.filter((_, i) => i !== index);
	}

	function updateLanguage(index, value) {
		settings.ocr.languages = settings.ocr.languages.map((lang, i) =>
			i === index ? value : lang
		);
	}
</script>

{#if error}
	<div class="mb-4 rounded-lg border border-terracotta-600 bg-terracotta-500/10 p-3 text-sm text-terracotta-500">
		{error}
	</div>
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

{#if step === 2}
	<form onsubmit={submitSettings} class="space-y-5">
		<div>
			<label for="ocr-engine" class="mb-1 block text-sm font-medium text-parchment-200">OCR engine</label>
			<select
				id="ocr-engine"
				bind:value={settings.ocr.engine}
				class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
			>
				{#each engines?.ocr ?? [{value:'gosseract',label:'gosseract (local Tesseract)'},{value:'ocrmypdf',label:'ocrmypdf'}] as opt}
					<option value={opt.value}>{opt.label}</option>
				{/each}
			</select>
		</div>

		<div>
			<span class="mb-2 block text-sm font-medium text-parchment-200">OCR languages</span>
			{#each settings.ocr.languages as lang, i}
				<div class="mb-2 flex gap-2">
					<input
						type="text"
						value={lang}
						oninput={(e) => updateLanguage(i, e.currentTarget.value)}
						placeholder="eng"
						required
						class="flex-1 rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 placeholder-parchment-500 focus:border-gold-500 focus:outline-none"
					/>
					{#if settings.ocr.languages.length > 1}
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

		<div class="grid grid-cols-2 gap-4">
			<div>
				<label for="consumer-workers" class="mb-1 block text-sm font-medium text-parchment-200">
					Consumer workers
				</label>
				<input
					id="consumer-workers"
					type="number"
					min="1"
					bind:value={settings.consumer.workers}
					class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
				/>
			</div>
			<div>
				<label for="enricher-workers" class="mb-1 block text-sm font-medium text-parchment-200">
					Enricher workers
				</label>
				<input
					id="enricher-workers"
					type="number"
					min="1"
					bind:value={settings.enricher.workers}
					class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 focus:border-gold-500 focus:outline-none"
				/>
			</div>
		</div>

		<button
			type="submit"
			class="w-full rounded-lg bg-gold-500 px-4 py-2 text-sm font-medium text-clay-950 hover:bg-gold-600"
		>
			Save and continue
		</button>
	</form>
{/if}

{#if step === 3}
	<div class="space-y-4 text-center">
		<div class="text-parchment-200">
			<h2 class="text-lg font-semibold">Setting things up...</h2>
			<p class="mt-2 text-sm text-parchment-500">
				Downloading required models and language files. This may take a few minutes.
			</p>
		</div>
		<div class="mx-auto h-8 w-8 animate-spin rounded-full border-2 border-clay-800 border-t-gold-500"></div>
		<p class="text-sm text-parchment-400">{pendingTasks} task(s) remaining</p>
	</div>
{/if}

{#if step === 4}
	<div class="space-y-4 text-center">
		<h2 class="text-lg font-semibold text-parchment-200">Setup complete</h2>
		<p class="text-sm text-parchment-500">
			Your configuration is ready. Run <code class="rounded bg-clay-800 px-1 py-0.5 text-parchment-200">edub</code>
			to start the server.
		</p>
		<button
			type="button"
			onclick={() => window.close()}
			class="rounded-lg bg-gold-500 px-4 py-2 text-sm font-medium text-clay-950 hover:bg-gold-600"
		>
			Close
		</button>
	</div>
{/if}
