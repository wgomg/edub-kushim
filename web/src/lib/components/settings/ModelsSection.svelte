<script>
	import { onMount } from 'svelte';
	import { api } from '$lib/api';
	import { confirmStore } from '$lib/stores/confirmStore.svelte.js';
	import { toastStore } from '$lib/stores/toastStore.svelte.js';
	import Card from './Card.svelte';
	import GroupHead from './GroupHead.svelte';
	import Switch from './Switch.svelte';
	import UnitField from './UnitField.svelte';

	let { onChanged = () => {} } = $props();

	let catalog = $state({ providers: [], models: [] });
	let loaded = $state(false);
	let loadError = $state('');
	let busy = $state(false);

	let showAddProvider = $state(false);
	let addProviderForm = $state({ provider: '', adapter: '', official_url: '', session_header: '' });

	let editingProvider = $state(null);
	let providerForm = $state({ adapter: '', official_url: '', session_header: '' });

	let addModelFor = $state(null);
	let addModelForm = $state(emptyModelForm());

	let editingModel = $state(null);
	let editModelBase = $state(null);
	let editModelForm = $state(emptyModelForm());

	let adapterOptions = $derived(
		[
			...new Set([...catalog.providers.map((p) => p.adapter), 'openai-compatible', 'anthropic'])
		].filter(Boolean)
	);

	function emptyModelForm() {
		return {
			provider: '',
			model_id: '',
			display_name: '',
			max_input_tokens: '',
			max_output_tokens: '',
			disable_reasoning: false,
			reasoning_efforts: '',
			supports_structured_output: true,
			supports_temperature: true,
			supports_prompt_caching: false,
			uses_max_completion_tokens: false,
			uses_responses_endpoint: false
		};
	}

	function modelFormFrom(m) {
		return {
			provider: m.provider,
			model_id: m.model_id,
			display_name: m.display_name ?? '',
			max_input_tokens: m.max_input_tokens ? String(m.max_input_tokens) : '',
			max_output_tokens: m.max_output_tokens ? String(m.max_output_tokens) : '',
			disable_reasoning: !!m.disable_reasoning,
			reasoning_efforts: (m.reasoning_efforts ?? []).join(', '),
			supports_structured_output: !!m.supports_structured_output,
			supports_temperature: !!m.supports_temperature,
			supports_prompt_caching: !!m.supports_prompt_caching,
			uses_max_completion_tokens: !!m.uses_max_completion_tokens,
			uses_responses_endpoint: !!m.uses_responses_endpoint
		};
	}

	// base is the fetched entry, spread under the edited fields so a field this form does not know survives
	function modelBody(form, base = {}) {
		return {
			...base,
			provider: form.provider.trim(),
			model_id: form.model_id.trim(),
			display_name: form.display_name.trim(),
			max_input_tokens: parseInt(form.max_input_tokens, 10) || 0,
			max_output_tokens: parseInt(form.max_output_tokens, 10) || 0,
			disable_reasoning: form.disable_reasoning,
			reasoning_efforts: form.reasoning_efforts
				.split(',')
				.map((s) => s.trim())
				.filter(Boolean),
			supports_structured_output: form.supports_structured_output,
			supports_temperature: form.supports_temperature,
			supports_prompt_caching: form.supports_prompt_caching,
			uses_max_completion_tokens: form.uses_max_completion_tokens,
			uses_responses_endpoint: form.uses_responses_endpoint
		};
	}

	function modelsFor(provider) {
		return catalog.models.filter((m) => m.provider === provider);
	}

	function capsSummary(m) {
		const parts = [];
		if (m.disable_reasoning) parts.push('disable-reasoning');
		if ((m.reasoning_efforts ?? []).length)
			parts.push(`efforts: ${m.reasoning_efforts.join(', ')}`);
		if (m.supports_structured_output) parts.push('structured output');
		if (m.supports_temperature) parts.push('temperature');
		if (m.supports_prompt_caching) parts.push('caching');
		if (m.uses_max_completion_tokens) parts.push('max_completion_tokens');
		if (m.uses_responses_endpoint) parts.push('responses api');
		return parts.join(' · ') || 'no capabilities';
	}

	async function load() {
		const res = await api.llm.catalog();
		if (res.ok) {
			catalog = res.data ?? { providers: [], models: [] };
			loadError = '';
		} else {
			loadError = res.data?.error ?? `Failed to load the catalog (${res.status}).`;
		}
		loaded = true;
	}

	async function run(op) {
		busy = true;
		const res = await op();
		busy = false;
		if (res.ok) {
			catalog = res.data ?? catalog;
			loadError = '';
			onChanged();
			return true;
		}
		toastStore.error(res.data?.error ?? `Request failed (${res.status})`);
		return false;
	}

	async function addProvider() {
		const name = addProviderForm.provider.trim();
		const ok = await run(() =>
			api.llm.addProvider({
				provider: name,
				adapter: addProviderForm.adapter.trim(),
				official_url: addProviderForm.official_url.trim(),
				session_header: addProviderForm.session_header.trim()
			})
		);
		if (ok) {
			toastStore.success(`Provider "${name}" added.`);
			addProviderForm = { provider: '', adapter: '', official_url: '', session_header: '' };
			showAddProvider = false;
		}
	}

	function openProviderEdit(p) {
		editingProvider = p.provider;
		providerForm = {
			adapter: p.adapter,
			official_url: p.official_url,
			session_header: p.session_header ?? ''
		};
	}

	async function saveProvider() {
		const ok = await run(() =>
			api.llm.setProvider(editingProvider, {
				adapter: providerForm.adapter.trim(),
				official_url: providerForm.official_url.trim(),
				session_header: providerForm.session_header.trim()
			})
		);
		if (ok) {
			toastStore.success(`Provider "${editingProvider}" updated.`);
			editingProvider = null;
		}
	}

	async function removeProvider(p) {
		const count = modelsFor(p.provider).length;
		const message =
			count > 0
				? `Delete provider "${p.provider}" and its ${count} model(s)? This cannot be undone.`
				: `Delete provider "${p.provider}"? This action cannot be undone.`;
		const ok = await confirmStore.confirm({ title: 'Delete provider', message, danger: true });
		if (!ok) return;
		if (await run(() => api.llm.removeProvider(p.provider, count > 0))) {
			toastStore.success(`Provider "${p.provider}" removed.`);
		}
	}

	function openAddModel(provider) {
		addModelFor = provider;
		editingModel = null;
		addModelForm = emptyModelForm();
		addModelForm.provider = provider;
	}

	async function addModel() {
		const ok = await run(() => api.llm.addModel(modelBody(addModelForm)));
		if (ok) {
			toastStore.success(`Model "${addModelForm.model_id.trim()}" added.`);
			addModelFor = null;
		}
	}

	function openModelEdit(m) {
		editingModel = { provider: m.provider, model_id: m.model_id };
		editModelBase = m;
		addModelFor = null;
		editModelForm = modelFormFrom(m);
	}

	async function saveModel() {
		const { provider, model_id } = editingModel;
		const ok = await run(() =>
			api.llm.setModel(provider, model_id, modelBody(editModelForm, editModelBase))
		);
		if (ok) {
			toastStore.success(`Model "${model_id}" updated.`);
			editingModel = null;
		}
	}

	async function removeModel(m) {
		const ok = await confirmStore.confirm({
			title: 'Delete model',
			message: `Delete model "${m.provider}/${m.model_id}"? This action cannot be undone.`,
			danger: true
		});
		if (!ok) return;
		if (await run(() => api.llm.removeModel(m.provider, m.model_id))) {
			toastStore.success(`Model "${m.model_id}" removed.`);
		}
	}

	async function resetCatalogs() {
		const ok = await confirmStore.confirm({
			title: 'Restore default catalogs',
			message:
				'Replace both catalog files with the embedded defaults? Custom providers and models will be lost.',
			danger: true
		});
		if (!ok) return;
		if (await run(() => api.llm.reset({ providers: true, models: true }))) {
			toastStore.success('Catalogs restored to defaults.');
		}
	}

	onMount(load);
</script>

{#snippet modelFields(form, prefix)}
	<div class="grid gap-3 sm:grid-cols-2">
		<UnitField id="{prefix}-display-name" label="Display name" bind:value={form.display_name} />
		<UnitField
			id="{prefix}-efforts"
			label="Reasoning efforts"
			placeholder="low, medium, high"
			hint="Comma separated; empty = none"
			bind:value={form.reasoning_efforts}
		/>
		<UnitField
			id="{prefix}-max-input-tokens"
			label="Max input tokens"
			unit="tokens"
			type="number"
			inputmode="numeric"
			min="0"
			hint="0 = not set"
			bind:value={form.max_input_tokens}
		/>
		<UnitField
			id="{prefix}-max-output-tokens"
			label="Max output tokens"
			unit="tokens"
			type="number"
			inputmode="numeric"
			min="0"
			hint="0 = not set"
			bind:value={form.max_output_tokens}
		/>
		<Switch
			id="{prefix}-disable-reasoning"
			bind:checked={form.disable_reasoning}
			label="Disable reasoning"
			description="The endpoint accepts a flag to turn thinking off."
		/>
		<Switch
			id="{prefix}-structured-output"
			bind:checked={form.supports_structured_output}
			label="Structured output"
			description="Accepts a JSON response format request."
		/>
		<Switch
			id="{prefix}-temperature"
			bind:checked={form.supports_temperature}
			label="Temperature"
			description="Accepts a temperature parameter."
		/>
		<Switch
			id="{prefix}-prompt-caching"
			bind:checked={form.supports_prompt_caching}
			label="Prompt caching"
			description="Caches the prompt prefix across requests."
		/>
		<Switch
			id="{prefix}-max-completion-tokens"
			bind:checked={form.uses_max_completion_tokens}
			label="max_completion_tokens"
			description="Output bound sent as max_completion_tokens."
		/>
		<Switch
			id="{prefix}-responses-endpoint"
			bind:checked={form.uses_responses_endpoint}
			label="Responses API"
			description="Served over the /responses endpoint."
		/>
	</div>
{/snippet}

<section id="models" class="group scroll-mt-6 pb-12">
	<GroupHead
		eyebrow="Catalog"
		title="Models"
		description="The providers and models the enricher can draw from. Changes apply immediately."
	/>

	<div class="space-y-4">
		<Card
			title="Providers"
			name="models providers catalog adapters endpoints models llm"
			description="Endpoints with their adapter and models."
		>
			{#snippet actions()}
				<button
					onclick={() => (showAddProvider = !showAddProvider)}
					class="rounded-lg bg-gold-500 px-4 py-2 text-sm font-medium text-clay-950 hover:bg-gold-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
				>
					Add provider
				</button>
			{/snippet}

			{#if showAddProvider}
				<form
					class="field mb-4 grid gap-3 rounded-lg border border-dashed border-clay-700 p-3 sm:grid-cols-2"
					onsubmit={(e) => {
						e.preventDefault();
						addProvider();
					}}
				>
					<UnitField
						id="provider-add-name"
						label="Name"
						placeholder="groq"
						spellcheck="false"
						bind:value={addProviderForm.provider}
					/>
					<div class="field">
						<label
							for="provider-add-adapter"
							class="text-[0.8125rem] font-medium text-parchment-200">Adapter</label
						>
						<select
							id="provider-add-adapter"
							bind:value={addProviderForm.adapter}
							class="w-full rounded-lg border border-clay-800 bg-clay-950 px-2.5 py-[0.45rem] text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
						>
							{#each adapterOptions as adapter (adapter)}
								<option value={adapter}>{adapter}</option>
							{/each}
						</select>
					</div>
					<UnitField
						id="provider-add-url"
						label="Official URL"
						placeholder="https://api.example.com/v1"
						spellcheck="false"
						wide
						bind:value={addProviderForm.official_url}
					/>
					<UnitField
						id="provider-add-session-header"
						label="Session header"
						placeholder="optional"
						spellcheck="false"
						hint="Header the provider expects the session id in"
						bind:value={addProviderForm.session_header}
					/>
					<div class="flex items-end gap-2 sm:col-span-2">
						<button
							type="submit"
							disabled={busy}
							class="rounded-md bg-gold-500 px-3 py-1.5 text-xs font-medium text-clay-950 hover:bg-gold-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-50"
						>
							Add
						</button>
						<button
							type="button"
							onclick={() => (showAddProvider = false)}
							class="rounded-md px-3 py-1.5 text-xs font-medium text-parchment-400 hover:bg-clay-800 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
						>
							Cancel
						</button>
					</div>
				</form>
			{/if}

			{#if !loaded}
				<p class="text-parchment-500">Loading catalog…</p>
			{:else if loadError}
				<p class="field text-terracotta-400">{loadError}</p>
			{:else if catalog.providers.length === 0}
				<p class="text-parchment-500">No providers cataloged.</p>
			{:else}
				<div class="space-y-3">
					{#each catalog.providers as p (p.provider)}
						<div class="field rounded-lg border border-clay-800 p-3">
							<div class="flex flex-wrap items-start justify-between gap-2">
								<div class="min-w-0">
									<div class="flex items-center gap-2">
										<span class="text-sm font-medium text-parchment-200">{p.provider}</span>
										<span
											class="rounded-full border border-clay-700 px-2 py-0.5 font-mono text-[0.6875rem] text-parchment-500"
											>{p.adapter}</span
										>
									</div>
									<p class="truncate font-mono text-xs text-parchment-500">{p.official_url}</p>
									{#if p.session_header}
										<p class="font-mono text-xs text-parchment-500">
											session: {p.session_header}
										</p>
									{/if}
								</div>
								<div class="flex shrink-0 gap-1">
									<button
										onclick={() => openProviderEdit(p)}
										class="rounded-md px-2 py-1 text-xs font-medium text-parchment-400 hover:bg-clay-800 hover:text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
									>
										Edit
									</button>
									<button
										onclick={() => openAddModel(p.provider)}
										class="rounded-md px-2 py-1 text-xs font-medium text-parchment-400 hover:bg-clay-800 hover:text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
									>
										Add model
									</button>
									<button
										onclick={() => removeProvider(p)}
										class="rounded-md px-2 py-1 text-xs font-medium text-parchment-400 hover:bg-clay-800 hover:text-terracotta-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
									>
										Remove
									</button>
								</div>
							</div>

							{#if editingProvider === p.provider}
								<form
									class="mt-3 grid gap-3 rounded-lg border border-dashed border-clay-700 p-3 sm:grid-cols-2"
									onsubmit={(e) => {
										e.preventDefault();
										saveProvider();
									}}
								>
									<div class="field">
										<label
											for="provider-edit-adapter-{p.provider}"
											class="text-[0.8125rem] font-medium text-parchment-200">Adapter</label
										>
										<select
											id="provider-edit-adapter-{p.provider}"
											bind:value={providerForm.adapter}
											class="w-full rounded-lg border border-clay-800 bg-clay-950 px-2.5 py-[0.45rem] text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
										>
											{#each adapterOptions as adapter (adapter)}
												<option value={adapter}>{adapter}</option>
											{/each}
										</select>
									</div>
									<UnitField
										id="provider-edit-url-{p.provider}"
										label="Official URL"
										spellcheck="false"
										bind:value={providerForm.official_url}
									/>
									<UnitField
										id="provider-edit-session-{p.provider}"
										label="Session header"
										placeholder="optional"
										spellcheck="false"
										bind:value={providerForm.session_header}
									/>
									<div class="flex items-end gap-2">
										<button
											type="submit"
											disabled={busy}
											class="rounded-md bg-gold-500 px-3 py-1.5 text-xs font-medium text-clay-950 hover:bg-gold-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-50"
										>
											Save
										</button>
										<button
											type="button"
											onclick={() => (editingProvider = null)}
											class="rounded-md px-3 py-1.5 text-xs font-medium text-parchment-400 hover:bg-clay-800 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
										>
											Cancel
										</button>
									</div>
								</form>
							{/if}

							<ul class="mt-2 divide-y divide-clay-800/60">
								{#each modelsFor(p.provider) as m (m.model_id)}
									<li class="field py-2">
										<div class="flex flex-wrap items-start justify-between gap-2">
											<div class="min-w-0">
												<div class="flex items-center gap-2">
													<span class="font-mono text-xs text-parchment-300">{m.model_id}</span>
													{#if m.display_name}
														<span class="text-xs text-parchment-500">{m.display_name}</span>
													{/if}
												</div>
												<p class="text-xs text-parchment-500">
													in {m.max_input_tokens || '—'} · out {m.max_output_tokens || '—'} ·
													{capsSummary(m)}
												</p>
											</div>
											<div class="flex shrink-0 gap-1">
												<button
													onclick={() => openModelEdit(m)}
													class="rounded-md px-2 py-1 text-xs font-medium text-parchment-400 hover:bg-clay-800 hover:text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
												>
													Edit
												</button>
												<button
													onclick={() => removeModel(m)}
													class="rounded-md px-2 py-1 text-xs font-medium text-parchment-400 hover:bg-clay-800 hover:text-terracotta-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
												>
													Remove
												</button>
											</div>
										</div>

										{#if editingModel && editingModel.provider === p.provider && editingModel.model_id === m.model_id}
											<form
												class="mt-2 rounded-lg border border-dashed border-clay-700 p-3"
												onsubmit={(e) => {
													e.preventDefault();
													saveModel();
												}}
											>
												{@render modelFields(
													editModelForm,
													`model-edit-${m.provider}-${m.model_id}`
												)}
												<div class="mt-3 flex items-end gap-2">
													<button
														type="submit"
														disabled={busy}
														class="rounded-md bg-gold-500 px-3 py-1.5 text-xs font-medium text-clay-950 hover:bg-gold-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-50"
													>
														Save
													</button>
													<button
														type="button"
														onclick={() => (editingModel = null)}
														class="rounded-md px-3 py-1.5 text-xs font-medium text-parchment-400 hover:bg-clay-800 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
													>
														Cancel
													</button>
												</div>
											</form>
										{/if}
									</li>
								{/each}
							</ul>

							{#if addModelFor === p.provider}
								<form
									class="mt-2 rounded-lg border border-dashed border-clay-700 p-3"
									onsubmit={(e) => {
										e.preventDefault();
										addModel();
									}}
								>
									<div class="mb-3">
										<UnitField
											id="model-add-id-{p.provider}"
											label="Model id"
											placeholder="llama-4-scout"
											spellcheck="false"
											wide
											bind:value={addModelForm.model_id}
										/>
									</div>
									{@render modelFields(addModelForm, `model-add-${p.provider}`)}
									<div class="mt-3 flex items-end gap-2">
										<button
											type="submit"
											disabled={busy}
											class="rounded-md bg-gold-500 px-3 py-1.5 text-xs font-medium text-clay-950 hover:bg-gold-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-50"
										>
											Add
										</button>
										<button
											type="button"
											onclick={() => (addModelFor = null)}
											class="rounded-md px-3 py-1.5 text-xs font-medium text-parchment-400 hover:bg-clay-800 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
										>
											Cancel
										</button>
									</div>
								</form>
							{/if}
						</div>
					{/each}
				</div>
			{/if}
		</Card>

		<Card
			title="Defaults"
			name="defaults restore reset embedded catalog providers models"
			description="Recover the shipped provider and model catalogs."
		>
			<div class="field flex flex-wrap items-center justify-between gap-3">
				<p class="text-[0.8125rem] text-parchment-500">
					Replaces both catalog files with the embedded defaults.
				</p>
				<button
					onclick={resetCatalogs}
					disabled={busy}
					class="rounded-lg border border-terracotta-700 px-4 py-2 text-sm font-medium text-terracotta-400 hover:bg-terracotta-950 focus-visible:ring-2 focus-visible:ring-terracotta-500 focus-visible:outline-none disabled:opacity-50"
				>
					Restore default catalogs
				</button>
			</div>
		</Card>
	</div>
</section>
