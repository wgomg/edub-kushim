<script>
	import { page } from '$app/stores';
	import { replaceState } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { confirmStore } from '$lib/stores/confirmStore.svelte.js';
	import { toastStore } from '$lib/stores/toastStore.svelte.js';

	let {
		catalog = { providers: [], models: [] },
		loaded = false,
		error = '',
		usedBy = {},
		pendingCount = 0,
		busy = false,
		onAddProvider = () => {},
		onUpdateProvider = () => {},
		onRemoveProvider = () => {},
		onAddModel = () => {},
		onUpdateModel = () => {},
		onRemoveModel = () => {},
		onReset = async () => false
	} = $props();

	let providerQuery = $state('');
	let selectedProviderName = $state($page.url.searchParams.get('provider'));
	let panelMode = $state(null);
	let panelProvider = $state(null);
	let panelModelId = $state(null);
	let resetting = $state(false);

	let providerErrors = $state({ name: '', url: '' });
	let modelErrors = $state({ id: '' });

	let providerForm = $state(emptyProviderForm());
	let modelForm = $state(emptyModelForm());
	let editModelBase = $state(null);

	let adapterOptions = $derived(
		[
			...new Set([
				...catalog.providers.map((provider) => provider.adapter),
				'openai-compatible',
				'anthropic',
				'google'
			])
		].filter(Boolean)
	);

	let filteredProviders = $derived.by(() => {
		const needle = providerQuery.trim().toLowerCase();
		if (!needle) return catalog.providers;
		return catalog.providers.filter(
			(provider) =>
				provider.provider.toLowerCase().includes(needle) ||
				provider.adapter.toLowerCase().includes(needle) ||
				catalog.models.some(
					(model) =>
						model.provider === provider.provider && model.model_id.toLowerCase().includes(needle)
				)
		);
	});

	let selectedProvider = $derived(
		catalog.providers.find((provider) => provider.provider === selectedProviderName) ??
			catalog.providers[0] ??
			null
	);

	function emptyProviderForm() {
		return { provider: '', adapter: 'openai-compatible', official_url: '', session_header: '' };
	}

	function emptyModelForm() {
		return {
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

	function modelFormFrom(model) {
		return {
			model_id: model.model_id,
			display_name: model.display_name ?? '',
			max_input_tokens: model.max_input_tokens ? String(model.max_input_tokens) : '',
			max_output_tokens: model.max_output_tokens ? String(model.max_output_tokens) : '',
			disable_reasoning: !!model.disable_reasoning,
			reasoning_efforts: (model.reasoning_efforts ?? []).join(', '),
			supports_structured_output: !!model.supports_structured_output,
			supports_temperature: !!model.supports_temperature,
			supports_prompt_caching: !!model.supports_prompt_caching,
			uses_max_completion_tokens: !!model.uses_max_completion_tokens,
			uses_responses_endpoint: !!model.uses_responses_endpoint
		};
	}

	function modelsFor(providerName) {
		return catalog.models.filter((model) => model.provider === providerName);
	}

	const compactNumber = new Intl.NumberFormat(undefined, {
		notation: 'compact',
		maximumFractionDigits: 1
	});

	function formatTokens(value) {
		if (!value) return '—';
		return compactNumber.format(value);
	}

	function selectProvider(name) {
		selectedProviderName = name;
		const url = new URL($page.url);
		if (name) url.searchParams.set('provider', name);
		else url.searchParams.delete('provider');
		replaceState(resolve(`${url.pathname}${url.search}`), $page.state);
	}

	function capsSummary(model) {
		const parts = [];
		if (model.supports_structured_output) parts.push('JSON');
		if (model.supports_temperature) parts.push('temp');
		if (model.supports_prompt_caching) parts.push('cache');
		if (model.uses_responses_endpoint) parts.push('responses');
		if (model.disable_reasoning) parts.push('no think');
		if (model.uses_max_completion_tokens) parts.push('max tokens');
		if ((model.reasoning_efforts ?? []).length) parts.push('efforts');
		return parts;
	}

	function usedByFor(provider, modelId) {
		return usedBy[`${provider}/${modelId}`] ?? [];
	}

	function providerUsedBy(provider) {
		return usedBy[`${provider}/*`] ?? [];
	}

	function modelBody(form) {
		return {
			...form,
			provider: panelProvider,
			model_id: form.model_id.trim(),
			display_name: form.display_name.trim(),
			max_input_tokens: parseInt(form.max_input_tokens, 10) || 0,
			max_output_tokens: parseInt(form.max_output_tokens, 10) || 0,
			reasoning_efforts: form.reasoning_efforts
				.split(',')
				.map((value) => value.trim())
				.filter(Boolean)
		};
	}

	function openAddProvider() {
		panelMode = 'provider-add';
		panelProvider = null;
		panelModelId = null;
		providerForm = emptyProviderForm();
		providerErrors = { name: '', url: '' };
	}

	function openEditProvider(provider) {
		panelMode = 'provider-edit';
		panelProvider = provider.provider;
		panelModelId = null;
		providerForm = {
			provider: provider.provider,
			adapter: provider.adapter,
			official_url: provider.official_url,
			session_header: provider.session_header ?? ''
		};
		providerErrors = { name: '', url: '' };
	}

	function openAddModel(providerName) {
		panelMode = 'model-add';
		panelProvider = providerName;
		panelModelId = null;
		modelForm = emptyModelForm();
		editModelBase = null;
		modelErrors = { id: '' };
	}

	function openEditModel(model) {
		panelMode = 'model-edit';
		panelProvider = model.provider;
		panelModelId = model.model_id;
		modelForm = modelFormFrom(model);
		editModelBase = model;
		modelErrors = { id: '' };
	}

	function closePanel() {
		panelMode = null;
		panelProvider = null;
		panelModelId = null;
		editModelBase = null;
	}

	function submitProvider(event) {
		event.preventDefault();
		const name = providerForm.provider.trim();
		const url = providerForm.official_url.trim();
		const duplicate =
			panelMode === 'provider-add' &&
			catalog.providers.some((provider) => provider.provider === name);
		providerErrors = {
			name: !name
				? 'Enter a provider name.'
				: duplicate
					? 'Use a different name; this provider already exists.'
					: '',
			url: !url ? 'Enter the provider URL.' : ''
		};
		if (providerErrors.name || providerErrors.url) {
			const first = providerErrors.name ? 'provider-name' : 'provider-url';
			document.getElementById(first)?.focus();
			return;
		}
		const payload = {
			provider: name,
			adapter: providerForm.adapter.trim(),
			official_url: url,
			session_header: providerForm.session_header.trim()
		};
		if (panelMode === 'provider-add') {
			onAddProvider(payload);
			selectProvider(name);
		} else {
			onUpdateProvider(panelProvider, {
				adapter: payload.adapter,
				official_url: payload.official_url,
				session_header: payload.session_header
			});
		}
		toastStore.success(
			panelMode === 'provider-add' ? 'Provider staged.' : 'Provider update staged.'
		);
		closePanel();
	}

	function submitModel(event) {
		event.preventDefault();
		const body = modelBody(modelForm);
		const duplicate =
			panelMode === 'model-add' &&
			modelsFor(panelProvider).some((model) => model.model_id === body.model_id);
		modelErrors = {
			id: !body.model_id
				? 'Enter a model ID.'
				: duplicate
					? 'Use a different ID; this model already exists on this provider.'
					: ''
		};
		if (modelErrors.id) {
			document.getElementById('model-id')?.focus();
			return;
		}
		if (panelMode === 'model-add') {
			onAddModel(body);
			toastStore.success('Model staged.');
		} else {
			onUpdateModel(panelProvider, panelModelId, {
				...(editModelBase ?? {}),
				...body
			});
			toastStore.success('Model update staged.');
		}
		closePanel();
	}

	async function removeProvider(provider) {
		const count = modelsFor(provider.provider).length;
		const ok = await confirmStore.confirm({
			title: 'Remove Provider',
			message:
				count > 0
					? `Remove provider "${provider.provider}" and its ${count} model(s) from the staged catalog? Nothing is applied until you apply changes.`
					: `Remove provider "${provider.provider}" from the staged catalog? Nothing is applied until you apply changes.`,
			danger: true
		});
		if (!ok) return;
		const next = catalog.providers.find((item) => item.provider !== provider.provider);
		onRemoveProvider(provider.provider, count > 0);
		selectProvider(next?.provider ?? null);
		toastStore.success('Provider removal staged.');
	}

	async function removeModel(model) {
		const ok = await confirmStore.confirm({
			title: 'Remove Model',
			message: `Remove model "${model.provider}/${model.model_id}" from the staged catalog? Nothing is applied until you apply changes.`,
			danger: true
		});
		if (!ok) return;
		onRemoveModel(model.provider, model.model_id);
		toastStore.success('Model removal staged.');
	}

	async function resetCatalogs() {
		if (busy) return;
		const ok = await confirmStore.confirm({
			title: 'Restore Default Catalogs',
			message:
				'Replace both catalog files with the embedded defaults? This applies immediately and discards staged catalog changes.',
			danger: true
		});
		if (!ok) return;
		resetting = true;
		await onReset();
		resetting = false;
	}

	function handleKeydown(event) {
		if (event.key === 'Escape' && panelMode) closePanel();
	}
</script>

<svelte:window onkeydown={handleKeydown} />

{#if pendingCount > 0}
	<div
		class="mb-4 flex items-center gap-2 rounded-lg border border-gold-800/60 bg-gold-500/5 px-3 py-2 text-xs text-gold-500"
	>
		<span class="size-1.5 rounded-full bg-gold-500"></span>
		{pendingCount === 1 ? '1 catalog change staged' : `${pendingCount} catalog changes staged`} — apply
		them with the rest of your settings.
	</div>
{/if}

<div class="grid gap-4 lg:grid-cols-[17rem_minmax(0,1fr)] lg:items-start">
	<aside class="rounded-xl border border-clay-800 bg-clay-900 p-2.5 lg:sticky lg:top-16">
		<div class="mb-2">
			<input
				type="search"
				name="provider-filter"
				autocomplete="off"
				bind:value={providerQuery}
				placeholder="Filter providers…"
				aria-label="Filter providers"
				class="w-full rounded-lg border border-clay-800 bg-clay-950 px-2.5 py-1.5 text-sm text-parchment-200 placeholder-parchment-500 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
			/>
		</div>

		{#if !loaded}
			<p class="px-2 py-3 text-sm text-parchment-500">Loading catalog…</p>
		{:else if error}
			<p class="px-2 py-3 text-sm text-terracotta-400">{error}</p>
		{:else if filteredProviders.length === 0}
			<p class="px-2 py-3 text-sm text-parchment-500">
				{providerQuery ? 'No providers match this filter.' : 'No providers cataloged.'}
			</p>
		{:else}
			<div class="flex max-h-[calc(100vh-19rem)] flex-col gap-0.5 overflow-y-auto">
				{#each filteredProviders as provider (provider.provider)}
					<button
						type="button"
						onclick={() => selectProvider(provider.provider)}
						class="flex items-center gap-2.5 rounded-lg border px-2.5 py-2 text-left focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none {selectedProvider?.provider ===
						provider.provider
							? 'border-clay-700 bg-clay-800 text-parchment-200'
							: 'border-transparent text-parchment-400 hover:border-clay-800 hover:bg-clay-800'}"
					>
						<span
							class="grid size-7 shrink-0 place-items-center rounded-md border border-clay-700 bg-clay-950 font-mono text-[0.6875rem] font-bold text-gold-500"
						>
							{provider.provider.slice(0, 1).toUpperCase()}
						</span>
						<span class="flex min-w-0 flex-col">
							<strong class="text-[0.8125rem] font-medium">{provider.provider}</strong>
							<span class="truncate font-mono text-[0.625rem] text-parchment-500">
								{provider.adapter} · {modelsFor(provider.provider).length}
								{modelsFor(provider.provider).length === 1 ? 'model' : 'models'}
							</span>
						</span>
						<span class="ml-auto font-mono text-[0.625rem] font-semibold text-parchment-500">
							{modelsFor(provider.provider).length}
						</span>
					</button>
				{/each}
			</div>
		{/if}

		<div
			class="mt-2 flex items-center justify-between gap-2 border-t border-dashed border-clay-700 px-1 pt-2"
		>
			<p class="text-[0.625rem] text-parchment-500">Providers own their endpoint and adapter.</p>
			<button
				type="button"
				onclick={openAddProvider}
				class="rounded-md bg-gold-500 px-2.5 py-1 text-xs font-semibold text-clay-950 hover:bg-gold-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
			>
				+ Add
			</button>
		</div>
	</aside>

	<section class="min-w-0 rounded-xl border border-clay-800 bg-clay-900 p-4">
		{#if !selectedProvider}
			<div class="py-12 text-center">
				<h2 class="text-lg font-semibold text-parchment-200">No Providers Yet</h2>
				<p class="mt-1 text-sm text-parchment-500">Add a provider to start building the catalog.</p>
				<button
					type="button"
					onclick={openAddProvider}
					class="mt-4 rounded-lg bg-gold-500 px-4 py-2 text-sm font-semibold text-clay-950 hover:bg-gold-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
				>
					Add Provider
				</button>
			</div>
		{:else}
			<div class="flex flex-wrap items-start justify-between gap-3">
				<div class="flex items-center gap-3">
					<span
						class="grid size-9 place-items-center rounded-lg border border-clay-700 bg-clay-950 font-mono text-xs font-bold text-gold-500"
					>
						{selectedProvider.provider.slice(0, 1).toUpperCase()}
					</span>
					<div>
						<h2 class="text-lg font-semibold text-parchment-200">
							{selectedProvider.provider}
						</h2>
						<div class="mt-1 flex flex-wrap items-center gap-1.5">
							<span
								class="rounded-full border border-clay-700 px-2 py-0.5 font-mono text-[0.625rem] text-parchment-500"
								>{selectedProvider.adapter}</span
							>
							<span
								class="rounded-full border border-clay-700 px-2 py-0.5 text-[0.6875rem] text-parchment-500"
							>
								{modelsFor(selectedProvider.provider).length}
								{modelsFor(selectedProvider.provider).length === 1 ? 'model' : 'models'}
							</span>
							{#each providerUsedBy(selectedProvider.provider) as label (label)}
								<span
									class="rounded-full border border-gold-800 px-2 py-0.5 text-[0.6875rem] text-gold-500"
								>
									provider used by {label}
								</span>
							{/each}
						</div>
					</div>
				</div>
				<div class="flex gap-1.5">
					<button
						type="button"
						onclick={() => openEditProvider(selectedProvider)}
						class="rounded-lg border border-clay-700 px-2.5 py-1.5 text-xs font-medium text-parchment-300 hover:bg-clay-800 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					>
						Edit
					</button>
					<button
						type="button"
						onclick={() => removeProvider(selectedProvider)}
						class="rounded-lg border border-terracotta-700 px-2.5 py-1.5 text-xs font-medium text-terracotta-400 hover:bg-terracotta-950 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					>
						Remove
					</button>
				</div>
			</div>

			<div class="mt-4 grid gap-4 border-y border-clay-800 py-3 sm:grid-cols-[1.4fr_1fr_0.7fr]">
				<div>
					<span
						class="block font-mono text-[0.625rem] font-semibold tracking-[0.1em] text-parchment-500 uppercase"
						>Endpoint</span
					>
					<code class="block truncate font-mono text-xs text-parchment-300"
						>{selectedProvider.official_url}</code
					>
				</div>
				<div>
					<span
						class="block font-mono text-[0.625rem] font-semibold tracking-[0.1em] text-parchment-500 uppercase"
						>Session header</span
					>
					<code class="block truncate font-mono text-xs text-parchment-300"
						>{selectedProvider.session_header || '—'}</code
					>
				</div>
				<div>
					<span
						class="block font-mono text-[0.625rem] font-semibold tracking-[0.1em] text-parchment-500 uppercase"
						>Adapter</span
					>
					<code class="block truncate font-mono text-xs text-parchment-300"
						>{selectedProvider.adapter}</code
					>
				</div>
			</div>

			<div class="mt-4 flex items-center justify-between gap-3">
				<h3 class="text-sm font-semibold text-parchment-200">
					Models
					<span class="font-mono text-xs font-normal text-parchment-500"
						>({modelsFor(selectedProvider.provider).length})</span
					>
				</h3>
				<button
					type="button"
					onclick={() => openAddModel(selectedProvider.provider)}
					class="rounded-lg border border-clay-700 px-2.5 py-1 text-xs font-medium text-parchment-300 hover:bg-clay-800 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
				>
					+ Add Model
				</button>
			</div>

			{#if modelsFor(selectedProvider.provider).length === 0}
				<p class="py-6 text-center text-sm text-parchment-500">
					No models yet. Add the first model for this provider.
				</p>
			{:else}
				<div class="mt-2 overflow-x-auto">
					<table class="w-full border-collapse text-xs">
						<thead>
							<tr class="border-b border-clay-800 text-left">
								<th
									class="px-2 py-2 font-mono text-[0.625rem] font-semibold tracking-[0.09em] text-parchment-500 uppercase"
								>
									Model
								</th>
								<th
									class="px-2 py-2 font-mono text-[0.625rem] font-semibold tracking-[0.09em] text-parchment-500 uppercase"
								>
									Context
								</th>
								<th
									class="px-2 py-2 font-mono text-[0.625rem] font-semibold tracking-[0.09em] text-parchment-500 uppercase"
								>
									Output
								</th>
								<th
									class="px-2 py-2 font-mono text-[0.625rem] font-semibold tracking-[0.09em] text-parchment-500 uppercase"
								>
									Capabilities
								</th>
								<th
									class="px-2 py-2 font-mono text-[0.625rem] font-semibold tracking-[0.09em] text-parchment-500 uppercase"
								>
									Used by
								</th>
								<th class="px-2 py-2"></th>
							</tr>
						</thead>
						<tbody>
							{#each modelsFor(selectedProvider.provider) as model (model.model_id)}
								<tr class="border-b border-clay-800/60 last:border-b-0 hover:bg-clay-800/60">
									<td class="px-2 py-2.5">
										<span class="block font-mono text-xs text-parchment-200">{model.model_id}</span>
										{#if model.display_name}
											<span class="block text-[0.6875rem] text-parchment-500"
												>{model.display_name}</span
											>
										{/if}
									</td>
									<td class="px-2 py-2.5 text-[0.6875rem] text-parchment-500 tabular-nums">
										{formatTokens(model.max_input_tokens)}
									</td>
									<td class="px-2 py-2.5 text-[0.6875rem] text-parchment-500 tabular-nums">
										{formatTokens(model.max_output_tokens)}
									</td>
									<td class="px-2 py-2.5">
										<div class="flex flex-wrap gap-1">
											{#each capsSummary(model) as cap (cap)}
												<span
													class="rounded border border-clay-700 px-1 py-px font-mono text-[0.5625rem] text-parchment-500"
													>{cap}</span
												>
											{:else}
												<span class="text-[0.6875rem] text-parchment-500">—</span>
											{/each}
										</div>
									</td>
									<td class="px-2 py-2.5 text-[0.6875rem] text-parchment-500">
										{usedByFor(model.provider, model.model_id).join(', ') || '—'}
									</td>
									<td class="px-2 py-2.5 text-right whitespace-nowrap">
										<button
											type="button"
											onclick={() => openEditModel(model)}
											class="px-1.5 py-0.5 text-[0.6875rem] text-parchment-500 hover:text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
										>
											Edit
										</button>
										<button
											type="button"
											onclick={() => removeModel(model)}
											class="px-1.5 py-0.5 text-[0.6875rem] text-parchment-500 hover:text-terracotta-400 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
										>
											Remove
										</button>
									</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
			{/if}
		{/if}
	</section>
</div>

<section class="mt-4 rounded-xl border border-terracotta-700/40 bg-terracotta-950/20 p-4">
	<div class="flex flex-wrap items-center justify-between gap-3">
		<div>
			<h3 class="text-sm font-semibold text-parchment-200">Restore Default Catalogs</h3>
			<p class="mt-0.5 text-xs text-parchment-500">
				Replaces both catalog files with the embedded defaults. Applies immediately and discards
				staged catalog changes.
			</p>
		</div>
		<button
			type="button"
			onclick={resetCatalogs}
			disabled={resetting || busy}
			class="rounded-lg border border-terracotta-700 px-3 py-1.5 text-sm font-medium text-terracotta-400 hover:bg-terracotta-950 focus-visible:ring-2 focus-visible:ring-terracotta-500 focus-visible:outline-none disabled:opacity-50"
		>
			{resetting ? 'Restoring…' : 'Restore Defaults'}
		</button>
	</div>
</section>

{#if panelMode}
	<div
		class="fixed inset-0 z-[90] bg-[rgba(10,8,7,0.62)]"
		onclick={closePanel}
		role="presentation"
	></div>
	<div
		class="fixed inset-y-0 right-0 z-[100] flex w-[min(28rem,100vw)] flex-col border-l border-clay-800 bg-clay-900 pr-[env(safe-area-inset-right)] pb-[env(safe-area-inset-bottom)] shadow-[-24px_0_48px_rgba(0,0,0,0.35)]"
		role="dialog"
		aria-modal="true"
		aria-labelledby="catalog-panel-title"
	>
		<div class="flex items-start justify-between gap-4 border-b border-clay-800 px-5 py-4">
			<div>
				<p
					class="font-mono text-[0.625rem] font-semibold tracking-[0.14em] text-gold-500 uppercase"
				>
					{panelMode.startsWith('provider') ? 'Catalog · provider' : 'Catalog · model'}
				</p>
				<h2 id="catalog-panel-title" class="mt-1 text-lg font-semibold text-parchment-200">
					{panelMode === 'provider-add'
						? 'Add Provider'
						: panelMode === 'provider-edit'
							? 'Edit Provider'
							: panelMode === 'model-add'
								? 'Add Model'
								: 'Edit Model'}
				</h2>
			</div>
			<button
				type="button"
				onclick={closePanel}
				aria-label="Close catalog editor"
				class="text-xl leading-none text-parchment-500 hover:text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
			>
				×
			</button>
		</div>

		{#if panelMode === 'provider-add' || panelMode === 'provider-edit'}
			<form class="flex flex-1 flex-col overflow-hidden" onsubmit={submitProvider}>
				<div class="flex-1 overflow-y-auto overscroll-contain px-5 py-4">
					<div class="grid gap-3 sm:grid-cols-2">
						<div class="field">
							<label for="provider-name" class="text-[0.8125rem] font-medium text-parchment-200"
								>Provider name</label
							>
							<input
								id="provider-name"
								name="provider"
								type="text"
								autocomplete="off"
								bind:value={providerForm.provider}
								disabled={panelMode === 'provider-edit'}
								placeholder="groq…"
								spellcheck="false"
								aria-invalid={!!providerErrors.name}
								aria-describedby={providerErrors.name ? 'provider-name-error' : undefined}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-2.5 py-[0.45rem] text-sm text-parchment-200 placeholder-parchment-500 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-60"
							/>
							{#if providerErrors.name}
								<p
									id="provider-name-error"
									class="text-[0.6875rem] text-terracotta-400"
									role="alert"
								>
									{providerErrors.name}
								</p>
							{/if}
						</div>
						<div class="field">
							<label for="provider-adapter" class="text-[0.8125rem] font-medium text-parchment-200"
								>Adapter</label
							>
							<select
								id="provider-adapter"
								name="adapter"
								bind:value={providerForm.adapter}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-2.5 py-[0.45rem] text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							>
								{#each adapterOptions as adapter (adapter)}
									<option value={adapter}>{adapter}</option>
								{/each}
							</select>
						</div>
						<div class="field sm:col-span-2">
							<label for="provider-url" class="text-[0.8125rem] font-medium text-parchment-200"
								>Official URL</label
							>
							<input
								id="provider-url"
								name="official_url"
								type="url"
								autocomplete="off"
								bind:value={providerForm.official_url}
								placeholder="https://api.example.com/v1…"
								spellcheck="false"
								aria-invalid={!!providerErrors.url}
								aria-describedby={providerErrors.url ? 'provider-url-error' : undefined}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-2.5 py-[0.45rem] text-sm text-parchment-200 placeholder-parchment-500 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							/>
							{#if providerErrors.url}
								<p
									id="provider-url-error"
									class="text-[0.6875rem] text-terracotta-400"
									role="alert"
								>
									{providerErrors.url}
								</p>
							{/if}
						</div>
						<div class="field sm:col-span-2">
							<label for="provider-session" class="text-[0.8125rem] font-medium text-parchment-200"
								>Session header <span class="font-normal text-parchment-500">(optional)</span
								></label
							>
							<input
								id="provider-session"
								name="session_header"
								type="text"
								autocomplete="off"
								bind:value={providerForm.session_header}
								placeholder="x-session-id…"
								spellcheck="false"
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-2.5 py-[0.45rem] text-sm text-parchment-200 placeholder-parchment-500 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							/>
						</div>
					</div>
					<p class="mt-4 text-[0.6875rem] text-parchment-500">
						Provider changes stay staged until you apply them. Removing a provider also stages the
						removal of its models.
					</p>
				</div>
				<div class="flex justify-end gap-2 border-t border-clay-800 px-5 py-3.5">
					<button
						type="button"
						onclick={closePanel}
						class="rounded-lg border border-clay-700 px-3 py-1.5 text-[0.8125rem] font-medium text-parchment-400 hover:bg-clay-800 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					>
						Cancel
					</button>
					<button
						type="submit"
						class="rounded-lg bg-gold-500 px-4 py-1.5 text-sm font-semibold text-clay-950 hover:bg-gold-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					>
						{panelMode === 'provider-add' ? 'Add Provider' : 'Save Provider'}
					</button>
				</div>
			</form>
		{:else}
			<form class="flex flex-1 flex-col overflow-hidden" onsubmit={submitModel}>
				<div class="flex-1 overflow-y-auto overscroll-contain px-5 py-4">
					<div class="grid gap-3 sm:grid-cols-2">
						<div class="field sm:col-span-2">
							<label for="model-id" class="text-[0.8125rem] font-medium text-parchment-200"
								>Model ID</label
							>
							<input
								id="model-id"
								name="model_id"
								type="text"
								autocomplete="off"
								bind:value={modelForm.model_id}
								disabled={panelMode === 'model-edit'}
								placeholder="llama-4-scout…"
								spellcheck="false"
								aria-invalid={!!modelErrors.id}
								aria-describedby={modelErrors.id ? 'model-id-error' : undefined}
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-2.5 py-[0.45rem] font-mono text-sm text-parchment-200 placeholder-parchment-500 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-60"
							/>
							{#if modelErrors.id}
								<p id="model-id-error" class="text-[0.6875rem] text-terracotta-400" role="alert">
									{modelErrors.id}
								</p>
							{/if}
						</div>
						<div class="field sm:col-span-2">
							<label for="model-name" class="text-[0.8125rem] font-medium text-parchment-200"
								>Display name</label
							>
							<input
								id="model-name"
								name="display_name"
								type="text"
								autocomplete="off"
								bind:value={modelForm.display_name}
								placeholder="Llama 4 Scout…"
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-2.5 py-[0.45rem] text-sm text-parchment-200 placeholder-parchment-500 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							/>
						</div>
						<div class="field">
							<label
								for="model-input-tokens"
								class="text-[0.8125rem] font-medium text-parchment-200">Max input tokens</label
							>
							<input
								id="model-input-tokens"
								name="max_input_tokens"
								type="number"
								min="0"
								bind:value={modelForm.max_input_tokens}
								placeholder="131072…"
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-2.5 py-[0.45rem] text-sm text-parchment-200 placeholder-parchment-500 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							/>
						</div>
						<div class="field">
							<label
								for="model-output-tokens"
								class="text-[0.8125rem] font-medium text-parchment-200">Max output tokens</label
							>
							<input
								id="model-output-tokens"
								name="max_output_tokens"
								type="number"
								min="0"
								bind:value={modelForm.max_output_tokens}
								placeholder="8192…"
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-2.5 py-[0.45rem] text-sm text-parchment-200 placeholder-parchment-500 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							/>
						</div>
						<div class="field sm:col-span-2">
							<label for="model-efforts" class="text-[0.8125rem] font-medium text-parchment-200"
								>Reasoning efforts</label
							>
							<input
								id="model-efforts"
								name="reasoning_efforts"
								type="text"
								autocomplete="off"
								bind:value={modelForm.reasoning_efforts}
								placeholder="low, medium, high…"
								class="w-full rounded-lg border border-clay-800 bg-clay-950 px-2.5 py-[0.45rem] text-sm text-parchment-200 placeholder-parchment-500 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							/>
							<p class="text-[0.6875rem] text-parchment-500">Comma separated; empty = none</p>
						</div>
					</div>

					<details class="mt-4 border-t border-dashed border-clay-700 pt-3" open>
						<summary
							class="cursor-pointer list-none text-[0.8125rem] font-medium text-parchment-300 before:font-mono before:text-gold-500 before:content-['−'] open:before:content-['+']"
						>
							Capabilities
						</summary>
						<div class="mt-3 grid gap-2 sm:grid-cols-2">
							<label class="flex items-center gap-2 text-xs text-parchment-400">
								<input
									type="checkbox"
									name="supports_structured_output"
									bind:checked={modelForm.supports_structured_output}
								/>
								Structured output
							</label>
							<label class="flex items-center gap-2 text-xs text-parchment-400">
								<input
									type="checkbox"
									name="supports_temperature"
									bind:checked={modelForm.supports_temperature}
								/>
								Temperature
							</label>
							<label class="flex items-center gap-2 text-xs text-parchment-400">
								<input
									type="checkbox"
									name="supports_prompt_caching"
									bind:checked={modelForm.supports_prompt_caching}
								/>
								Prompt caching
							</label>
							<label class="flex items-center gap-2 text-xs text-parchment-400">
								<input
									type="checkbox"
									name="uses_responses_endpoint"
									bind:checked={modelForm.uses_responses_endpoint}
								/>
								Responses API
							</label>
							<label class="flex items-center gap-2 text-xs text-parchment-400">
								<input
									type="checkbox"
									name="disable_reasoning"
									bind:checked={modelForm.disable_reasoning}
								/>
								Disable reasoning
							</label>
							<label class="flex items-center gap-2 text-xs text-parchment-400">
								<input
									type="checkbox"
									name="uses_max_completion_tokens"
									bind:checked={modelForm.uses_max_completion_tokens}
								/>
								max_completion_tokens
							</label>
						</div>
					</details>

					<p class="mt-4 text-[0.6875rem] text-parchment-500">
						Capabilities are hints for the enricher; they do not change the provider endpoint.
					</p>
				</div>
				<div class="flex justify-end gap-2 border-t border-clay-800 px-5 py-3.5">
					<button
						type="button"
						onclick={closePanel}
						class="rounded-lg border border-clay-700 px-3 py-1.5 text-[0.8125rem] font-medium text-parchment-400 hover:bg-clay-800 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					>
						Cancel
					</button>
					<button
						type="submit"
						class="rounded-lg bg-gold-500 px-4 py-1.5 text-sm font-semibold text-clay-950 hover:bg-gold-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					>
						{panelMode === 'model-add' ? 'Add Model' : 'Save Model'}
					</button>
				</div>
			</form>
		{/if}
	</div>
{/if}
