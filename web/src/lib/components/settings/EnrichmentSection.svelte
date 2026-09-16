<script>
	import Card from './Card.svelte';
	import EngineSelect from './EngineSelect.svelte';
	import GroupHead from './GroupHead.svelte';
	import Note from './Note.svelte';
	import Switch from './Switch.svelte';
	import TokenField from './TokenField.svelte';
	import UnitField from './UnitField.svelte';

	let {
		cfg,
		llmModels = { adapters: {}, providers: {} },
		toolStatus = [],
		statusKnown = false
	} = $props();

	let selectedAdapterProviders = $derived(
		llmModels.adapters[cfg.enricher.contentanalyzer.llm.adapter] ?? []
	);
	let selectedProviderModels = $derived(
		llmModels.providers[cfg.enricher.contentanalyzer.llm.provider] ?? []
	);

	function adapterProviders(llm) {
		return llmModels.adapters[llm?.adapter] ?? [];
	}

	function providerModels(llm) {
		return llmModels.providers[llm?.provider] ?? [];
	}

	function entryFor(engine) {
		return toolStatus.find((t) => t.engine === engine);
	}

	function onAdjudicatorAdapterChange() {
		if (!cfg.enricher.tag_adjudicator.llm.adapter) {
			cfg.enricher.tag_adjudicator.llm.provider = '';
			cfg.enricher.tag_adjudicator.llm.model = '';
		}
	}

	function onTagQualityAdapterChange() {
		if (!cfg.enricher.tag_quality.llm.adapter) {
			cfg.enricher.tag_quality.llm.provider = '';
			cfg.enricher.tag_quality.llm.model = '';
		}
	}

	function addFallback() {
		const ca = cfg.enricher.contentanalyzer;
		if (!ca.fallbacks) ca.fallbacks = [];
		ca.fallbacks = [
			...ca.fallbacks,
			{
				enabled: false,
				llm: { adapter: '', provider: '', model: '', token: '', temperature: 0, request_delay: 0 }
			}
		];
	}

	function removeFallback(index) {
		cfg.enricher.contentanalyzer.fallbacks = cfg.enricher.contentanalyzer.fallbacks.filter(
			(_, i) => i !== index
		);
	}

	function moveFallback(index, dir) {
		const fbs = cfg.enricher.contentanalyzer.fallbacks;
		const target = index + dir;
		if (target < 0 || target >= fbs.length) return;
		const next = [...fbs];
		[next[index], next[target]] = [next[target], next[index]];
		cfg.enricher.contentanalyzer.fallbacks = next;
	}
</script>

<section id="enrichment" class="group scroll-mt-6 pb-12">
	<GroupHead
		eyebrow="Stage 3 · Pipeline"
		title="Enrichment"
		description="What the machine understands — analysis, summaries, tags."
	/>

	<div class="space-y-4">
		<Card
			title="Content analyzer"
			name="content analyzer llm openai anthropic token prompt fallback"
			description="Sends each document's summary to an LLM to propose types, tags and people."
		>
			{#snippet actions()}
				<Switch
					bind:checked={cfg.enricher.contentanalyzer.enabled}
					label="Enabled"
					ariaLabel="Enable content analyzer"
				/>
			{/snippet}

			{#if cfg.enricher.contentanalyzer.enabled}
				<div class="grid gap-4 sm:grid-cols-2">
					<div class="field">
						<label for="llm-adapter" class="text-[0.8125rem] font-medium text-parchment-200"
							>Adapter</label
						>
						<select
							id="llm-adapter"
							name="llm-adapter"
							bind:value={cfg.enricher.contentanalyzer.llm.adapter}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-2.5 py-[0.45rem] text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
						>
							{#each Object.keys(llmModels.adapters) as adapter (adapter)}
								<option value={adapter}>{adapter}</option>
							{/each}
						</select>
					</div>

					<div class="field">
						<label for="llm-provider" class="text-[0.8125rem] font-medium text-parchment-200"
							>Provider</label
						>
						<select
							id="llm-provider"
							name="llm-provider"
							bind:value={cfg.enricher.contentanalyzer.llm.provider}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-2.5 py-[0.45rem] text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
						>
							{#each selectedAdapterProviders as provider (provider)}
								<option value={provider}>{provider}</option>
							{/each}
						</select>
					</div>

					<div class="field">
						<label for="llm-model" class="text-[0.8125rem] font-medium text-parchment-200"
							>Model</label
						>
						<select
							id="llm-model"
							name="llm-model"
							bind:value={cfg.enricher.contentanalyzer.llm.model}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-2.5 py-[0.45rem] text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
						>
							{#each selectedProviderModels as model (model.id)}
								<option value={model.id}>{model.id}</option>
							{/each}
						</select>
					</div>

					<div class="field">
						<label for="llm-token" class="text-[0.8125rem] font-medium text-parchment-200"
							>Token</label
						>
						<TokenField id="llm-token" bind:value={cfg.enricher.contentanalyzer.llm.token} />
					</div>

					<UnitField
						id="llm-temperature"
						label="Temperature"
						type="number"
						inputmode="numeric"
						min="0"
						max="2"
						step="0.1"
						bind:value={cfg.enricher.contentanalyzer.llm.temperature}
					/>
					<UnitField
						id="llm-request-delay"
						label="Request delay"
						unit="s"
						type="number"
						inputmode="numeric"
						min="0"
						max="60"
						step="0.1"
						hint="Seconds to sleep after each LLM request; 0 = off, max 60. Use for rate-limited providers."
						bind:value={cfg.enricher.contentanalyzer.llm.request_delay}
					/>
					<UnitField
						id="content-analyzer-timeout"
						label="Timeout"
						unit="s"
						type="number"
						inputmode="numeric"
						min="1"
						bind:value={cfg.enricher.contentanalyzer.timeout}
					/>
					<UnitField
						id="content-analyzer-min-tags"
						label="Min tags"
						unit="tags"
						type="number"
						inputmode="numeric"
						min="1"
						max="20"
						hint="Lower bound requested from the analyzer."
						bind:value={cfg.enricher.contentanalyzer.min_tags}
					/>
					<UnitField
						id="content-analyzer-max-tags"
						label="Max tags"
						unit="tags"
						type="number"
						inputmode="numeric"
						min="1"
						max="20"
						hint="Upper bound per document; also scales how many tag suggestions the matcher feeds the prompt."
						bind:value={cfg.enricher.contentanalyzer.max_tags}
					/>
					<div class="sm:col-span-2">
						<Switch
							id="content-analyzer-pause-credit"
							bind:checked={cfg.enricher.contentanalyzer.pause_on_credit_error}
							label="Pause batches on credit errors"
						/>
					</div>
				</div>

				{#if cfg.enricher.contentanalyzer.min_tags > cfg.enricher.contentanalyzer.max_tags}
					<div class="mt-4">
						<Note variant="warn" title="Tag range inverted.">
							Min tags must be less than or equal to max tags; the save will be rejected.
						</Note>
					</div>
				{/if}

				<details class="adv mt-[1.125rem] border-t border-dashed border-clay-700 pt-3">
					<summary
						class="flex cursor-pointer list-none items-center gap-1.5 text-[0.8125rem] font-medium text-parchment-500 before:font-mono before:text-gold-500 before:content-['+'] open:before:content-['−'] focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none [&::-webkit-details-marker]:hidden"
					>
						Prompt template (advanced)
					</summary>
					<div class="mt-3.5">
						<div class="field">
							<label
								for="content-analyzer-prompt-template"
								class="text-[0.8125rem] font-medium text-parchment-200">Template</label
							>
							<textarea
								id="content-analyzer-prompt-template"
								name="content-analyzer-prompt-template"
								rows="6"
								bind:value={cfg.enricher.contentanalyzer.prompt_template}
								spellcheck="false"
								placeholder="Leave empty for default."
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-2.5 py-[0.45rem] font-mono text-[0.8125rem] text-parchment-200 placeholder-parchment-500 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							></textarea>
							<p class="text-xs text-parchment-500">
								Leave empty for default. Available placeholders: {`{{.DocTypePrompt}}`},
								{`{{.TagsPrompt}}`}, {`{{.PeoplePrompt}}`}, {`{{.Text}}`} (required)
							</p>
						</div>
					</div>
				</details>

				<div class="mt-[1.125rem] rounded-[0.625rem] border border-clay-800 bg-clay-950 p-4">
					<h4 class="text-sm font-semibold text-parchment-200">Doc type refinement</h4>
					<p class="mt-1 mb-3 text-xs text-parchment-500">
						Feeds the analyzer the head and tail of the raw text to refine document types.
					</p>
					<div class="grid gap-4 sm:grid-cols-2">
						<div class="sm:col-span-2">
							<Switch
								id="doc-type-refinement-enabled"
								bind:checked={cfg.enricher.contentanalyzer.doc_type_refinement.enabled}
								label="Enabled"
							/>
						</div>
						<UnitField
							id="doc-type-refinement-head-words"
							label="Head words"
							unit="words"
							type="number"
							inputmode="numeric"
							min="0"
							bind:value={cfg.enricher.contentanalyzer.doc_type_refinement.head_words}
						/>
						<UnitField
							id="doc-type-refinement-tail-words"
							label="Tail words"
							unit="words"
							type="number"
							inputmode="numeric"
							min="0"
							bind:value={cfg.enricher.contentanalyzer.doc_type_refinement.tail_words}
						/>
					</div>
				</div>

				<div class="mt-[1.125rem] rounded-[0.625rem] border border-clay-800 bg-clay-950 p-4">
					<div class="mb-1 flex items-center justify-between gap-3">
						<h4 class="text-sm font-semibold text-parchment-200">Fallback LLMs</h4>
						<button
							type="button"
							onclick={addFallback}
							class="text-[0.8125rem] font-medium text-gold-500 hover:text-gold-600 hover:underline focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
						>
							+ Add fallback
						</button>
					</div>
					<p class="mb-3 text-xs text-parchment-500">
						Tried in order when the primary LLM fails with a provider error. The batch pauses on
						credit errors only when every enabled fallback also fails.
					</p>
					<div class="space-y-2.5">
						{#each cfg.enricher.contentanalyzer.fallbacks as fb, i (i)}
							<div class="rounded-[0.625rem] border border-clay-800 bg-clay-900 p-3.5">
								<div class="mb-3 flex items-center justify-between gap-3">
									<Switch
										bind:checked={fb.enabled}
										label="Fallback {i + 1}"
										ariaLabel="Enable fallback {i + 1}"
									/>
									<div class="flex items-center gap-1.5">
										<button
											type="button"
											aria-label="Move fallback {i + 1} up"
											disabled={i === 0}
											onclick={() => moveFallback(i, -1)}
											class="rounded-lg border border-clay-800 px-2 py-1 text-[0.75rem] text-parchment-400 hover:bg-clay-800 hover:text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:cursor-not-allowed disabled:opacity-40"
										>
											↑
										</button>
										<button
											type="button"
											aria-label="Move fallback {i + 1} down"
											disabled={i === cfg.enricher.contentanalyzer.fallbacks.length - 1}
											onclick={() => moveFallback(i, 1)}
											class="rounded-lg border border-clay-800 px-2 py-1 text-[0.75rem] text-parchment-400 hover:bg-clay-800 hover:text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:cursor-not-allowed disabled:opacity-40"
										>
											↓
										</button>
										<button
											type="button"
											onclick={() => removeFallback(i)}
											class="rounded-lg border border-clay-800 px-2 py-1 text-[0.75rem] text-parchment-400 hover:bg-clay-800 hover:text-terracotta-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
										>
											Remove
										</button>
									</div>
								</div>
								{#if fb.enabled}
									<div class="grid gap-4 sm:grid-cols-3">
										<div class="field">
											<label
												for="llm-fallback-{i}-adapter"
												class="text-[0.8125rem] font-medium text-parchment-200">Adapter</label
											>
											<select
												id="llm-fallback-{i}-adapter"
												name="llm-fallback-{i}-adapter"
												bind:value={fb.llm.adapter}
												class="w-full rounded-lg border border-clay-800 bg-clay-950 px-2.5 py-[0.45rem] text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
											>
												{#each Object.keys(llmModels.adapters) as adapter (adapter)}
													<option value={adapter}>{adapter}</option>
												{/each}
											</select>
										</div>
										<div class="field">
											<label
												for="llm-fallback-{i}-provider"
												class="text-[0.8125rem] font-medium text-parchment-200">Provider</label
											>
											<select
												id="llm-fallback-{i}-provider"
												name="llm-fallback-{i}-provider"
												bind:value={fb.llm.provider}
												class="w-full rounded-lg border border-clay-800 bg-clay-950 px-2.5 py-[0.45rem] text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
											>
												{#each adapterProviders(fb.llm) as provider (provider)}
													<option value={provider}>{provider}</option>
												{/each}
											</select>
										</div>
										<div class="field">
											<label
												for="llm-fallback-{i}-model"
												class="text-[0.8125rem] font-medium text-parchment-200">Model</label
											>
											<select
												id="llm-fallback-{i}-model"
												name="llm-fallback-{i}-model"
												bind:value={fb.llm.model}
												class="w-full rounded-lg border border-clay-800 bg-clay-950 px-2.5 py-[0.45rem] text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
											>
												{#each providerModels(fb.llm) as model (model.id)}
													<option value={model.id}>{model.id}</option>
												{/each}
											</select>
										</div>
										<div class="field">
											<label
												for="llm-fallback-{i}-token"
												class="text-[0.8125rem] font-medium text-parchment-200">Token</label
											>
											<TokenField id="llm-fallback-{i}-token" bind:value={fb.llm.token} />
										</div>
										<UnitField
											id="llm-fallback-{i}-temperature"
											label="Temperature"
											type="number"
											inputmode="numeric"
											min="0"
											max="2"
											step="0.1"
											bind:value={fb.llm.temperature}
										/>
										<UnitField
											id="llm-fallback-{i}-request-delay"
											label="Request delay"
											unit="s"
											type="number"
											inputmode="numeric"
											min="0"
											max="60"
											step="0.1"
											hint="Seconds to sleep after each fallback request; 0 = off, max 60."
											bind:value={fb.llm.request_delay}
										/>
									</div>
								{/if}
							</div>
						{/each}
					</div>
				</div>
			{/if}
		</Card>

		<Card
			title="Text reducer"
			name="text reducer textrank summary words"
			description="Condenses extracted text before it is sent anywhere."
		>
			<div class="grid gap-4 sm:grid-cols-2">
				<EngineSelect
					id="text-reducer-engine"
					label="Engine"
					engines={cfg.available_engines?.text_reducer ?? []}
					toolEntry={entryFor(cfg.enricher.textreducer.engine)}
					{statusKnown}
					bind:value={cfg.enricher.textreducer.engine}
				/>
				<UnitField
					id="text-reducer-timeout"
					label="Timeout"
					unit="s"
					type="number"
					inputmode="numeric"
					min="0"
					bind:value={cfg.enricher.textreducer.timeout}
				/>
				<UnitField
					id="text-reducer-target-words"
					label="Target words"
					unit="words"
					type="number"
					inputmode="numeric"
					min="1"
					wide
					bind:value={cfg.enricher.textreducer.target_words}
				/>
			</div>
		</Card>

		<Card
			title="Tag matcher"
			name="tag matcher hugot embeddings bge chunk"
			description="Embedding-based matching of tags to document text. Runs in the kushim hugot daemon — tag editing is unavailable while it is down."
		>
			<div class="grid gap-4 sm:grid-cols-2">
				<UnitField
					id="tag-matcher-timeout"
					label="Timeout"
					unit="s"
					type="number"
					inputmode="numeric"
					min="0"
					bind:value={cfg.enricher.tagmatcher.timeout}
				/>
				<UnitField
					id="tag-matcher-reduce-target"
					label="Reduce target words"
					unit="words"
					type="number"
					inputmode="numeric"
					min="0"
					bind:value={cfg.enricher.tagmatcher.reduce_target_words}
				/>
				<UnitField
					id="tag-matcher-chunk-size"
					label="Chunk size"
					unit="tokens"
					type="number"
					inputmode="numeric"
					min="0"
					bind:value={cfg.enricher.tagmatcher.chunk_size}
				/>
				<div class="field">
					<label
						for="tag-matcher-hugot-backend"
						class="text-[0.8125rem] font-medium text-parchment-200">Hugot backend</label
					>
					<select
						id="tag-matcher-hugot-backend"
						name="tag-matcher-hugot-backend"
						bind:value={cfg.enricher.tagmatcher.hugot.backend}
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-2.5 py-[0.45rem] text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					>
						<option value="ort">ort</option>
						<option value="GO">GO</option>
					</select>
				</div>
				<UnitField
					id="tag-matcher-hugot-model"
					label="Hugot model"
					mono
					wide
					bind:value={cfg.enricher.tagmatcher.hugot.model}
				/>
			</div>
		</Card>

		<Card
			title="Tag adjudicator"
			name="tag adjudicator llm inherit"
			description="Second LLM pass that reviews borderline tag assignments."
		>
			{#snippet actions()}
				<Switch
					bind:checked={cfg.enricher.tag_adjudicator.enabled}
					label="Enabled"
					ariaLabel="Enable tag adjudicator"
				/>
			{/snippet}

			{#if cfg.enricher.tag_adjudicator.enabled}
				<div class="grid gap-4 sm:grid-cols-2">
					<UnitField
						id="tag-adjudicator-timeout"
						label="Timeout"
						unit="s"
						type="number"
						inputmode="numeric"
						min="0"
						hint="0 = no deadline"
						bind:value={cfg.enricher.tag_adjudicator.timeout}
					/>
					<div class="field">
						<label
							for="tag-adjudicator-adapter"
							class="text-[0.8125rem] font-medium text-parchment-200">Adapter</label
						>
						<select
							id="tag-adjudicator-adapter"
							name="tag-adjudicator-adapter"
							bind:value={cfg.enricher.tag_adjudicator.llm.adapter}
							onchange={onAdjudicatorAdapterChange}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-2.5 py-[0.45rem] text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
						>
							<option value="">Inherit from content analyzer</option>
							{#each Object.keys(llmModels.adapters) as adapter (adapter)}
								<option value={adapter}>{adapter}</option>
							{/each}
						</select>
					</div>
					<div class="field">
						<label
							for="tag-adjudicator-provider"
							class="text-[0.8125rem] font-medium text-parchment-200">Provider</label
						>
						<select
							id="tag-adjudicator-provider"
							name="tag-adjudicator-provider"
							bind:value={cfg.enricher.tag_adjudicator.llm.provider}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-2.5 py-[0.45rem] text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
						>
							<option value="">Inherit from content analyzer</option>
							{#each adapterProviders(cfg.enricher.tag_adjudicator.llm) as provider (provider)}
								<option value={provider}>{provider}</option>
							{/each}
						</select>
					</div>
					<div class="field">
						<label
							for="tag-adjudicator-model"
							class="text-[0.8125rem] font-medium text-parchment-200">Model</label
						>
						<select
							id="tag-adjudicator-model"
							name="tag-adjudicator-model"
							bind:value={cfg.enricher.tag_adjudicator.llm.model}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-2.5 py-[0.45rem] text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
						>
							<option value="">Inherit from content analyzer</option>
							{#each providerModels(cfg.enricher.tag_adjudicator.llm) as model (model.id)}
								<option value={model.id}>{model.id}</option>
							{/each}
						</select>
					</div>
					<div class="field">
						<label
							for="tag-adjudicator-token"
							class="text-[0.8125rem] font-medium text-parchment-200">Token</label
						>
						<TokenField
							id="tag-adjudicator-token"
							bind:value={cfg.enricher.tag_adjudicator.llm.token}
						/>
					</div>
					<UnitField
						id="tag-adjudicator-request-delay"
						label="Request delay"
						unit="s"
						type="number"
						inputmode="numeric"
						min="0"
						max="60"
						step="0.1"
						bind:value={cfg.enricher.tag_adjudicator.llm.request_delay}
					/>
				</div>
			{/if}
		</Card>

		<Card
			title="Tag quality"
			name="tag quality judge rubric reject threshold similar"
			description="Rubric judge that filters newly emitted tags before they enter the shared vocabulary."
		>
			{#snippet actions()}
				<Switch
					bind:checked={cfg.enricher.tag_quality.enabled}
					label="Enabled"
					ariaLabel="Enable tag quality judge"
				/>
			{/snippet}

			{#if cfg.enricher.tag_quality.enabled}
				<div class="grid gap-4 sm:grid-cols-2">
					<UnitField
						id="tag-quality-timeout"
						label="Timeout"
						unit="s"
						type="number"
						inputmode="numeric"
						min="0"
						hint="0 = no deadline"
						bind:value={cfg.enricher.tag_quality.timeout}
					/>
					<div class="field">
						<label for="tag-quality-adapter" class="text-[0.8125rem] font-medium text-parchment-200"
							>Adapter</label
						>
						<select
							id="tag-quality-adapter"
							name="tag-quality-adapter"
							bind:value={cfg.enricher.tag_quality.llm.adapter}
							onchange={onTagQualityAdapterChange}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-2.5 py-[0.45rem] text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
						>
							<option value="">Inherit from content analyzer</option>
							{#each Object.keys(llmModels.adapters) as adapter (adapter)}
								<option value={adapter}>{adapter}</option>
							{/each}
						</select>
					</div>
					<div class="field">
						<label
							for="tag-quality-provider"
							class="text-[0.8125rem] font-medium text-parchment-200">Provider</label
						>
						<select
							id="tag-quality-provider"
							name="tag-quality-provider"
							bind:value={cfg.enricher.tag_quality.llm.provider}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-2.5 py-[0.45rem] text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
						>
							<option value="">Inherit from content analyzer</option>
							{#each adapterProviders(cfg.enricher.tag_quality.llm) as provider (provider)}
								<option value={provider}>{provider}</option>
							{/each}
						</select>
					</div>
					<div class="field">
						<label for="tag-quality-model" class="text-[0.8125rem] font-medium text-parchment-200"
							>Model</label
						>
						<select
							id="tag-quality-model"
							name="tag-quality-model"
							bind:value={cfg.enricher.tag_quality.llm.model}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-2.5 py-[0.45rem] text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
						>
							<option value="">Inherit from content analyzer</option>
							{#each providerModels(cfg.enricher.tag_quality.llm) as model (model.id)}
								<option value={model.id}>{model.id}</option>
							{/each}
						</select>
					</div>
					<div class="field">
						<label for="tag-quality-token" class="text-[0.8125rem] font-medium text-parchment-200"
							>Token</label
						>
						<TokenField id="tag-quality-token" bind:value={cfg.enricher.tag_quality.llm.token} />
					</div>
					<UnitField
						id="tag-quality-request-delay"
						label="Request delay"
						unit="s"
						type="number"
						inputmode="numeric"
						min="0"
						max="60"
						step="0.1"
						bind:value={cfg.enricher.tag_quality.llm.request_delay}
					/>
				</div>
				<div class="mt-4">
					<Note variant="info">
						The judge LLM should differ from the content analyzer's model; a shared model biases
						every verdict with the generator's own failure modes.
					</Note>
				</div>
			{/if}
		</Card>

		<Card
			title="Tag reconciliation"
			name="tag reconciliation report rejudge retag"
			description="Periodically re-checks old tag decisions against the current tag set."
		>
			{#if cfg.enricher.tag_reconcile.enabled && !cfg.enricher.tag_adjudicator?.enabled}
				<div class="mb-4">
					<Note variant="warn" title="Tag adjudicator not enabled.">
						Acting runs require the tag adjudicator — enable it on the Tag adjudicator card. Until
						then, scheduled runs stay snapshot-only.
					</Note>
				</div>
			{/if}
			<div class="grid gap-4 sm:grid-cols-2">
				<div class="sm:col-span-2">
					<Switch
						id="tag-reconcile-enabled"
						bind:checked={cfg.enricher.tag_reconcile.enabled}
						label="Schedule reconciliation runs"
					/>
				</div>
				<Switch
					id="tag-reconcile-report-only"
					bind:checked={cfg.enricher.tag_reconcile.report_only}
					label="Report only"
					description="The first run is always report-only regardless of this toggle."
				/>
				<UnitField
					id="tag-reconcile-interval"
					label="Interval"
					unit="days"
					type="number"
					inputmode="numeric"
					min="1"
					bind:value={cfg.enricher.tag_reconcile.interval}
				/>
				<UnitField
					id="tag-reconcile-time"
					label="Preferred time"
					mono
					placeholder="02:00"
					pattern="([01][0-9]|2[0-3]):[0-5][0-9]"
					hint="HH:MM, 24-hour."
					bind:value={cfg.enricher.tag_reconcile.time}
				/>
				<UnitField
					id="tag-reconcile-max-rejudge"
					label="Max pairs judged per run"
					unit="pairs"
					type="number"
					inputmode="numeric"
					min="1"
					bind:value={cfg.enricher.tag_reconcile.max_rejudge}
				/>
				<UnitField
					id="tag-reconcile-max-retag"
					label="Max documents re-tagged per run"
					unit="docs"
					type="number"
					inputmode="numeric"
					min="1"
					bind:value={cfg.enricher.tag_reconcile.max_retag}
				/>
				<UnitField
					id="tag-reconcile-dead-tag-grace"
					label="Orphan tag grace period"
					unit="days"
					type="number"
					inputmode="numeric"
					min="1"
					hint="Tags left without documents for this many days are removed during reconciliation."
					bind:value={cfg.enricher.tag_reconcile.dead_tag_grace_days}
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
			title="Enrichment workers"
			name="enricher workers parallel"
			description="Parallel documents moving through this stage."
		>
			<div class="grid gap-4 sm:grid-cols-2">
				<UnitField
					id="enricher-workers"
					label="Workers"
					unit="workers"
					type="number"
					inputmode="numeric"
					min="1"
					bind:value={cfg.enricher.workers}
				/>
			</div>
		</Card>
	</div>
</section>
