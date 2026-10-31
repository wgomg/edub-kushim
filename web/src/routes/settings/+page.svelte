<script>
	import { resolve } from '$app/paths';
	import { settingsStore } from '#lib/stores/settingsStore.svelte.js';
	import { SETTINGS_SECTION_BY_ID } from '#lib/settingsSections.js';
	import OverviewSection from '#lib/components/settings/OverviewSection.svelte';

	const TONE_TEXT = {
		ok: 'text-lapis-300',
		warn: 'text-gold-500',
		off: 'text-parchment-500'
	};
	const TONE_BADGE = {
		ok: 'border-lapis-300/35 bg-lapis-300/10 text-lapis-300',
		warn: 'border-gold-500/35 bg-gold-500/10 text-gold-500',
		off: 'border-clay-700 text-parchment-500',
		gold: 'border-gold-800 text-gold-500'
	};

	let cfg = $derived(settingsStore.cfg);
	let summary = $derived(settingsStore.toolSummary);

	let missingTools = $derived(settingsStore.toolStatus.filter((tool) => !tool.available).length);

	let scheduledJobs = $derived(
		[
			cfg.enricher.tag_reconcile.enabled,
			cfg.enricher.tag_measure.enabled,
			cfg.enricher.tag_audit.enabled
		].filter(Boolean).length
	);

	let previewChanges = $derived(
		[
			...settingsStore.configChanges.map((change) => ({
				id: change.key,
				label: change.label,
				detail: 'configuration'
			})),
			...settingsStore.catalogOps.map((op) => ({
				id: op.id,
				label: op.label,
				detail: 'catalog'
			}))
		].slice(0, 4)
	);

	let accessSummary = $derived(cfg.server.auth_enabled ? 'sign-in on' : 'open server');
	let systemSummary = $derived(`${cfg.app.log_level} · ${cfg.app.logging.max_backups} log files`);
	let safetySummary = $derived(
		`backups ${cfg.backup.enabled ? 'on' : 'off'} · mirror ${cfg.mirror.enabled ? 'on' : 'off'}`
	);
	let storageSummary = $derived(
		`${cfg.storage.migration_mode} · trash ${cfg.storage.trash.retention_days}d`
	);
	let enrichmentModel = $derived(
		`${cfg.enricher.contentanalyzer.llm.provider || 'no provider'} / ${cfg.enricher.contentanalyzer.llm.model || 'no model'}`
	);
	let processingSummary = $derived(`${cfg.consumer.ocr.engine} · ${cfg.consumer.workers} workers`);
	let intakeSummary = $derived(
		`:${cfg.server.port} · inbox ${cfg.consumer.polling.interval}s · ${(cfg.consumer.supported_files ?? []).length} formats`
	);

	let stages = $derived.by(() => {
		const processingStatus =
			missingTools === 1 ? '1 tool missing' : `${missingTools} tools missing`;
		const analyzerOn = cfg.enricher.contentanalyzer.enabled;
		return [
			{
				...SETTINGS_SECTION_BY_ID.intake,
				tone: 'ok',
				status: 'ready',
				summary: intakeSummary
			},
			{
				...SETTINGS_SECTION_BY_ID.processing,
				tone: missingTools > 0 ? 'warn' : 'ok',
				status: missingTools > 0 ? processingStatus : 'ready',
				summary: processingSummary
			},
			{
				...SETTINGS_SECTION_BY_ID.enrichment,
				tone: analyzerOn ? 'ok' : 'off',
				status: analyzerOn ? 'analyzer on' : 'analyzer off',
				summary: enrichmentModel
			},
			{
				...SETTINGS_SECTION_BY_ID.storage,
				tone: 'ok',
				status: 'healthy',
				summary: storageSummary
			}
		];
	});
</script>

<div class="mb-7 max-w-3xl">
	<h1 class="text-[1.625rem] font-semibold tracking-[-0.01em] text-parchment-200">Settings</h1>
	<p class="mt-1.5 text-parchment-500">
		Set up the library, connect AI, and keep the pipeline healthy — one focused workspace at a time.
	</p>
</div>

<div class="mb-7 grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
	<div class="rounded-xl border border-clay-800 bg-clay-900 px-4 py-3.5">
		<div
			class="flex items-center gap-1.5 font-mono text-[0.625rem] font-semibold tracking-[0.12em] text-parchment-500 uppercase"
		>
			<span class="size-[0.45rem] rounded-full bg-gold-500"></span> Toolchain
		</div>
		<div class="mt-1 text-[1.05rem] font-semibold text-parchment-200">
			{summary.known ? `${summary.available} / ${summary.total} ready` : 'checking…'}
		</div>
		<div class="mt-0.5 text-xs text-parchment-500">
			{missingTools > 0 ? `${missingTools} tool(s) missing` : 'all tools available'}
		</div>
	</div>
	<div class="rounded-xl border border-clay-800 bg-clay-900 px-4 py-3.5">
		<div
			class="flex items-center gap-1.5 font-mono text-[0.625rem] font-semibold tracking-[0.12em] text-parchment-500 uppercase"
		>
			<span class="size-[0.45rem] rounded-full bg-lapis-300"></span> AI providers
		</div>
		<div class="mt-1 text-[1.05rem] font-semibold text-parchment-200">
			{settingsStore.providerCount} providers · {settingsStore.modelCount} models
		</div>
		<div class="mt-0.5 text-xs text-parchment-500">one catalog for every stage</div>
	</div>
	<div class="rounded-xl border border-clay-800 bg-clay-900 px-4 py-3.5">
		<div
			class="flex items-center gap-1.5 font-mono text-[0.625rem] font-semibold tracking-[0.12em] text-parchment-500 uppercase"
		>
			<span
				class="size-[0.45rem] rounded-full {settingsStore.pendingCount > 0
					? 'bg-gold-500'
					: 'bg-parchment-600'}"
			></span>
			Staged changes
		</div>
		<div class="mt-1 text-[1.05rem] font-semibold text-parchment-200">
			{settingsStore.pendingCount} pending
		</div>
		<div class="mt-0.5 text-xs text-parchment-500">review before applying</div>
	</div>
	<div class="rounded-xl border border-clay-800 bg-clay-900 px-4 py-3.5">
		<div
			class="flex items-center gap-1.5 font-mono text-[0.625rem] font-semibold tracking-[0.12em] text-parchment-500 uppercase"
		>
			<span class="size-[0.45rem] rounded-full bg-lapis-300"></span> Scheduled jobs
		</div>
		<div class="mt-1 text-[1.05rem] font-semibold text-parchment-200">
			{scheduledJobs} enabled
		</div>
		<div class="mt-0.5 text-xs text-parchment-500">tag maintenance</div>
	</div>
</div>

<div class="mb-3 flex flex-wrap items-end justify-between gap-3">
	<div>
		<p class="font-mono text-[0.625rem] font-semibold tracking-[0.14em] text-gold-500 uppercase">
			The pipeline
		</p>
		<h2 class="mt-1 text-xl font-semibold text-balance text-parchment-200">How a Document Moves</h2>
	</div>
	<span class="text-xs text-parchment-500">Click a stage to configure it</span>
</div>

<div class="mb-8 grid gap-3 md:grid-cols-2 xl:grid-cols-4">
	{#each stages as stage (stage.id)}
		<a
			href={resolve(stage.path)}
			class="group flex min-h-28 flex-col rounded-xl border border-clay-800 bg-clay-900 p-4 transition-colors hover:border-gold-800 hover:bg-clay-800 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
		>
			<div class="flex items-center justify-between">
				<span
					class="grid size-5 place-items-center rounded-full border border-clay-700 bg-clay-950 font-mono text-[0.6875rem] font-semibold text-parchment-500"
					>{stage.number}</span
				>
				<span class="font-mono text-[0.6875rem] {TONE_TEXT[stage.tone]}">{stage.status}</span>
			</div>
			<span class="mt-2 text-sm font-semibold text-parchment-200">{stage.title}</span>
			<span class="text-xs text-parchment-500">{stage.short}</span>
		</a>
	{/each}
</div>

<div class="grid gap-8 xl:grid-cols-[minmax(0,1fr)_20rem] xl:items-start">
	<div>
		{#snippet destination({ section, icon, value, status, tone, group })}
			<a
				href={resolve(section.path)}
				class="group grid grid-cols-[auto_minmax(0,1fr)_auto_auto] items-center gap-3.5 rounded-xl border border-clay-800 bg-clay-900 px-3.5 py-3 transition-colors hover:border-gold-800 hover:bg-clay-800 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
			>
				<span
					class="grid size-8 place-items-center rounded-lg border border-clay-700 bg-clay-950 font-mono text-xs font-semibold text-parchment-500"
					>{icon}</span
				>
				<span class="flex min-w-0 flex-col">
					<span class="text-sm font-semibold text-parchment-200">{section.title}</span>
					<span class="truncate text-xs text-parchment-500">{section.description}</span>
				</span>
				<span class="flex flex-wrap items-center justify-end gap-2">
					<span class="hidden font-mono text-[0.6875rem] text-parchment-500 lg:inline">{value}</span
					>
					<span
						class="rounded-full border px-2 py-0.5 text-[0.6875rem] whitespace-nowrap {TONE_BADGE[
							tone
						]}">{status}</span
					>
					{#if settingsStore.groupCounts[group]}
						<span
							class="grid h-[1.1rem] min-w-[1.1rem] place-items-center rounded-full bg-gold-500 px-1 font-mono text-[0.625rem] font-bold text-clay-950"
							>{settingsStore.groupCounts[group]}</span
						>
					{/if}
				</span>
				<span class="text-clay-600 transition-colors group-hover:text-gold-500" aria-hidden="true"
					>→</span
				>
			</a>
		{/snippet}

		<div
			class="mb-2 font-mono text-[0.625rem] font-semibold tracking-[0.14em] text-gold-500 uppercase"
		>
			The pipeline
		</div>
		<div class="mb-6 flex flex-col gap-2">
			{#each stages as stage (stage.id)}
				{@render destination({
					section: stage,
					icon: stage.number,
					value: stage.summary,
					status: stage.status,
					tone: stage.tone,
					group: stage.id
				})}
			{/each}
		</div>

		<div
			class="mb-2 font-mono text-[0.625rem] font-semibold tracking-[0.14em] text-gold-500 uppercase"
		>
			Catalog
		</div>
		<div class="mb-6 flex flex-col gap-2">
			{@render destination({
				section: SETTINGS_SECTION_BY_ID.models,
				icon: 'AI',
				value: `${settingsStore.providerCount} providers · ${settingsStore.modelCount} models`,
				status: 'catalog',
				tone: 'gold',
				group: 'models'
			})}
		</div>

		<div
			class="mb-2 font-mono text-[0.625rem] font-semibold tracking-[0.14em] text-parchment-500 uppercase"
		>
			Operations
		</div>
		<div class="flex flex-col gap-2">
			{@render destination({
				section: SETTINGS_SECTION_BY_ID.safety,
				icon: 'S',
				value: safetySummary,
				status: 'healthy',
				tone: 'ok',
				group: 'safety'
			})}
			{@render destination({
				section: SETTINGS_SECTION_BY_ID.access,
				icon: 'A',
				value: accessSummary,
				status: cfg.server.auth_enabled ? 'protected' : 'open',
				tone: cfg.server.auth_enabled ? 'ok' : 'warn',
				group: 'access'
			})}
			{@render destination({
				section: SETTINGS_SECTION_BY_ID.system,
				icon: 'Y',
				value: systemSummary,
				status: 'healthy',
				tone: 'ok',
				group: 'system'
			})}
		</div>
	</div>

	<aside class="flex flex-col gap-3 xl:sticky xl:top-16">
		<div class="rounded-xl border border-clay-800 bg-clay-900 px-4 py-3.5">
			<div class="flex items-center justify-between gap-3">
				<h3 class="text-sm font-semibold text-parchment-200">Staged Changes</h3>
				<button
					type="button"
					onclick={() => settingsStore.openTray()}
					class="text-xs text-lapis-300 hover:text-lapis-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
				>
					Review
				</button>
			</div>
			{#if previewChanges.length === 0}
				<p class="mt-2 text-xs text-parchment-500">
					Nothing staged. Changes appear here as you edit.
				</p>
			{:else}
				<ul class="mt-2 flex flex-col gap-2">
					{#each previewChanges as change (change.id)}
						<li class="border-l-2 border-gold-800 pl-2 text-xs">
							<strong
								class="block font-mono text-[0.6875rem] font-medium break-all text-parchment-300"
								>{change.label}</strong
							>
							<span class="font-mono text-[0.6875rem] text-parchment-500">{change.detail}</span>
						</li>
					{/each}
				</ul>
				<button
					type="button"
					onclick={() => settingsStore.applyAll()}
					disabled={settingsStore.busy}
					class="mt-3 w-full rounded-lg bg-gold-500 px-3 py-1.5 text-sm font-semibold text-clay-950 hover:bg-gold-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-50"
				>
					{settingsStore.saving ? 'Applying…' : 'Apply Changes'}
				</button>
			{/if}
		</div>

		<div class="rounded-xl border border-gold-500/20 bg-gold-500/5 px-4 py-3.5">
			<p class="text-xs text-parchment-400">
				<strong class="text-parchment-200">One rule for saving.</strong> Configuration and catalog edits
				are staged first. Apply them together when you are ready; nothing reaches the server before that.
			</p>
		</div>
	</aside>
</div>

<section class="mt-8">
	<OverviewSection
		tools={settingsStore.toolStatus}
		statusKnown={settingsStore.statusLoaded}
		showHead={false}
	/>
</section>
