<script>
	import Card from './Card.svelte';
	import EngineSelect from './EngineSelect.svelte';
	import GroupHead from './GroupHead.svelte';
	import Note from './Note.svelte';
	import Switch from './Switch.svelte';
	import UnitField from './UnitField.svelte';

	let { cfg, toolStatus = [], statusKnown = false } = $props();

	let newLanguage = $state('');

	function entryFor(engine) {
		return toolStatus.find((t) => t.engine === engine);
	}

	let ocrTool = $derived(toolStatus.find((t) => t.engine === cfg.consumer.ocr.engine));

	function addLanguage() {
		const lang = newLanguage.trim().toLowerCase();
		if (lang && !cfg.consumer.ocr.languages.includes(lang)) {
			cfg.consumer.ocr.languages = [...cfg.consumer.ocr.languages, lang];
		}
		newLanguage = '';
	}

	function removeLanguage(index) {
		cfg.consumer.ocr.languages = cfg.consumer.ocr.languages.filter((_, i) => i !== index);
	}
</script>

<section id="processing" class="group scroll-mt-6 pb-12">
	<GroupHead
		eyebrow="Stage 2 · Pipeline"
		title="Processing"
		description="What happens to each file — OCR, text extraction, optimization, thumbnails."
	/>

	<div class="space-y-4">
		<Card
			title="OCR"
			name="ocr tesseract languages scanning tessdata"
			description="Reads scanned pages into searchable text."
		>
			<div class="grid gap-4 sm:grid-cols-2">
				<EngineSelect
					id="ocr-engine"
					label="Engine"
					engines={cfg.available_engines?.ocr ?? []}
					toolEntry={entryFor(cfg.consumer.ocr.engine)}
					{statusKnown}
					bind:value={cfg.consumer.ocr.engine}
				/>
				<UnitField
					id="ocr-timeout"
					label="Timeout"
					unit="s"
					type="number"
					inputmode="numeric"
					min="0"
					bind:value={cfg.consumer.ocr.timeout}
				/>
				<div class="field sm:col-span-2">
					<span class="text-[0.8125rem] font-medium text-parchment-200">Languages</span>
					<div class="flex flex-wrap items-center gap-2">
						{#each cfg.consumer.ocr.languages as lang, i (i)}
							<span
								class="inline-flex items-center gap-1.5 rounded-full border border-clay-700 bg-clay-800 py-0.5 pr-1 pl-2.5 font-mono text-[0.8125rem] text-parchment-200"
							>
								{lang}
								{#if cfg.consumer.ocr.languages.length > 1}
									<button
										type="button"
										aria-label="Remove {lang}"
										onclick={() => removeLanguage(i)}
										class="rounded-full px-1 text-sm leading-none text-parchment-500 hover:text-terracotta-300 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
									>
										×
									</button>
								{/if}
							</span>
						{/each}
					</div>
					<div class="flex gap-2">
						<input
							type="text"
							bind:value={newLanguage}
							onkeydown={(e) => {
								if (e.key === 'Enter') {
									e.preventDefault();
									addLanguage();
								}
							}}
							placeholder="eng, spa, deu…"
							spellcheck="false"
							autocomplete="off"
							aria-label="Add language"
							class="min-w-0 flex-1 rounded-lg border border-clay-800 bg-clay-950 px-2.5 py-[0.45rem] font-mono text-[0.8125rem] text-parchment-200 placeholder-parchment-500 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
						/>
						<button
							type="button"
							onclick={addLanguage}
							class="rounded-lg border border-clay-800 px-2.5 py-[0.45rem] text-[0.8125rem] font-medium text-parchment-400 hover:bg-clay-800 hover:text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
						>
							Add
						</button>
					</div>
					<p class="text-xs text-parchment-500">Tesseract three-letter language codes.</p>
				</div>
				<UnitField
					id="ocr-data-dir"
					label="Tessdata directory"
					mono
					wide
					bind:value={cfg.consumer.ocr.data_dir}
				/>
				<UnitField
					id="ocr-workers"
					label="OCR workers"
					unit="workers"
					type="number"
					inputmode="numeric"
					min="0"
					wide
					hint="0 = auto (CPU count)"
					bind:value={cfg.consumer.ocr.ocr_workers}
				/>
			</div>

			{#if ocrTool?.lang_hints?.length}
				<div class="mt-4">
					<Note variant="info" title="Tesseract language packs required">
						Install the packs for your configured languages ({ocrTool.languages.join(', ')}):
						{#each Object.entries(ocrTool.lang_hints[0].install_hints) as [system, cmd] (system)}
							<pre class="mt-1 overflow-x-auto text-xs text-parchment-300">{system}: {cmd}</pre>
						{/each}
					</Note>
				</div>
			{/if}

			{#each ocrTool?.companions ?? [] as companion (companion.command)}
				{#if !companion.available && companion.required}
					<div class="mt-4">
						<Note variant="danger" title={`“${companion.command}” not installed (required)`}>
							{companion.purpose}
							{#each Object.entries(companion.install_hints) as [system, cmd] (system)}
								<pre class="mt-1 overflow-x-auto text-xs text-parchment-300">{system}: {cmd}</pre>
							{/each}
						</Note>
					</div>
				{:else if !companion.available}
					<div class="mt-4">
						<Note variant="info" title={`“${companion.command}” not installed (optional)`}>
							{companion.purpose}. ocrmypdf will skip this feature without it.
							{#each Object.entries(companion.install_hints) as [system, cmd] (system)}
								<pre class="mt-1 overflow-x-auto text-xs text-parchment-300">{system}: {cmd}</pre>
							{/each}
						</Note>
					</div>
				{/if}
			{/each}
		</Card>

		<Card
			title="Text extraction"
			name="text extraction pdftotext mupdf gopdf"
			description="Pulls plain text out of PDFs."
		>
			<div class="grid gap-4 sm:grid-cols-2">
				<EngineSelect
					id="text-extractor-engine"
					label="Engine"
					engines={cfg.available_engines?.text_extractor ?? []}
					toolEntry={entryFor(cfg.consumer.textextractor.engine)}
					{statusKnown}
					bind:value={cfg.consumer.textextractor.engine}
				/>
				<UnitField
					id="text-extractor-timeout"
					label="Timeout"
					unit="s"
					type="number"
					inputmode="numeric"
					min="0"
					bind:value={cfg.consumer.textextractor.timeout}
				/>
			</div>
		</Card>

		<Card
			title="PDF optimizer"
			name="pdf optimizer ghostscript gs mupdf qpdf"
			description="Compresses and linearizes stored PDFs."
		>
			<div class="grid gap-4 sm:grid-cols-2">
				<EngineSelect
					id="pdf-engine"
					label="Engine"
					engines={cfg.available_engines?.pdf_optimizer ?? []}
					toolEntry={entryFor(cfg.consumer.pdfoptimizer.engine)}
					{statusKnown}
					bind:value={cfg.consumer.pdfoptimizer.engine}
				/>
				<UnitField
					id="pdf-fallback"
					label="Fallback (optional)"
					mono
					placeholder="gs"
					bind:value={cfg.consumer.pdfoptimizer.fallback}
				/>
				<UnitField
					id="pdf-timeout"
					label="Timeout"
					unit="s"
					type="number"
					inputmode="numeric"
					min="0"
					wide
					bind:value={cfg.consumer.pdfoptimizer.timeout}
				/>
			</div>
		</Card>

		<Card
			title="Thumbnails"
			name="thumbnails preview dpi quality backfill"
			description="Page previews for lists and galleries."
		>
			<div class="grid gap-4 sm:grid-cols-2">
				<div class="sm:col-span-2">
					<Switch
						id="thumb-enabled"
						bind:checked={cfg.consumer.thumbnail.enabled}
						label="Generate thumbnails"
					/>
				</div>
				<EngineSelect
					id="thumb-engine"
					label="Engine"
					engines={cfg.available_engines?.thumbnail ?? []}
					toolEntry={entryFor(cfg.consumer.thumbnail.engine)}
					{statusKnown}
					bind:value={cfg.consumer.thumbnail.engine}
				/>
				<UnitField
					id="thumb-dpi"
					label="Render DPI"
					unit="dpi"
					type="number"
					inputmode="numeric"
					min="1"
					bind:value={cfg.consumer.thumbnail.dpi}
				/>
				<UnitField
					id="thumb-max-width"
					label="Max width"
					unit="px"
					type="number"
					inputmode="numeric"
					min="1"
					bind:value={cfg.consumer.thumbnail.max_width}
				/>
				<UnitField
					id="thumb-quality"
					label="Quality"
					unit="%"
					type="number"
					inputmode="numeric"
					min="1"
					max="100"
					bind:value={cfg.consumer.thumbnail.quality}
				/>
				<UnitField
					id="thumb-timeout"
					label="Timeout"
					unit="s"
					type="number"
					inputmode="numeric"
					min="0"
					hint="0 = disabled (no deadline)"
					bind:value={cfg.consumer.thumbnail.timeout}
				/>
				<UnitField
					id="thumb-workers"
					label="Workers"
					unit="workers"
					type="number"
					inputmode="numeric"
					min="1"
					hint="1 = single worker"
					bind:value={cfg.consumer.thumbnail.workers}
				/>
				<UnitField
					id="thumb-backfill-interval"
					label="Backfill interval"
					unit="days"
					type="number"
					inputmode="decimal"
					min="0"
					step="0.1"
					hint="0 = disabled. Automatically enqueue thumbnail tasks for documents missing one."
					bind:value={cfg.consumer.thumbnail.backfill_interval}
				/>
				<UnitField
					id="thumb-backfill-time"
					label="Backfill preferred time"
					mono
					placeholder="02:00"
					pattern="([01][0-9]|2[0-3]):[0-5][0-9]"
					hint="HH:MM, 24-hour. Ignored for sub-daily intervals."
					bind:value={cfg.consumer.thumbnail.backfill_time}
				/>
			</div>
			<div class="mt-4">
				<Note variant="info">
					Changing the interval or preferred time takes effect at the closest upcoming occurrence of
					the new timing. If that time has already passed since the last run, the task runs once
					immediately and the new cadence counts from that run.
				</Note>
			</div>
		</Card>

		<Card
			title="Throughput & recovery"
			name="workers batch throughput reclaim retries stale"
			description="How many documents move through at once, and what happens to interrupted work."
		>
			<div class="grid gap-4 sm:grid-cols-2">
				<UnitField
					id="consumer-workers"
					label="Consumer workers"
					unit="workers"
					type="number"
					inputmode="numeric"
					min="1"
					bind:value={cfg.consumer.workers}
				/>
				<UnitField
					id="consumer-max-files-per-batch"
					label="Max files per batch"
					unit="files"
					type="number"
					inputmode="numeric"
					min="0"
					bind:value={cfg.consumer.max_files_per_batch}
				/>
			</div>
			<details class="adv mt-[1.125rem] border-t border-dashed border-clay-700 pt-3">
				<summary
					class="flex cursor-pointer list-none items-center gap-1.5 text-[0.8125rem] font-medium text-parchment-500 before:font-mono before:text-gold-500 before:content-['+'] open:before:content-['−'] focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none [&::-webkit-details-marker]:hidden"
				>
					Recovery
				</summary>
				<div class="mt-3.5 grid gap-4 sm:grid-cols-2">
					<div class="sm:col-span-2">
						<Switch
							id="reclaim-enabled"
							bind:checked={cfg.consumer.reclaim.enabled}
							label="Auto-resume interrupted batches"
							description="Requeue tasks whose worker died."
						/>
					</div>
					<UnitField
						id="reclaim-max-retries"
						label="Max retries per task"
						unit="tries"
						type="number"
						inputmode="numeric"
						min="1"
						max="10"
						bind:value={cfg.consumer.reclaim.max_retries}
					/>
					<UnitField
						id="reclaim-stale-task-after"
						label="Stale task after"
						unit="s"
						type="number"
						inputmode="numeric"
						min="60"
						bind:value={cfg.consumer.reclaim.stale_task_after}
					/>
				</div>
			</details>
		</Card>
	</div>
</section>
