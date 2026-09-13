<script>
	import { hintsForEngine } from '$lib/tools.js';
	import Card from './Card.svelte';
	import GroupHead from './GroupHead.svelte';
	import Note from './Note.svelte';
	import StatusBadge from './StatusBadge.svelte';

	let { tools = [], statusKnown = false } = $props();

	const CATEGORY_PURPOSE = {
		prerequisite: 'Required for model and language file downloads',
		ocr: 'Alternative OCR engine',
		pdfoptimizer: 'PDF optimization',
		textextractor: 'Text extraction',
		thumbnail: 'Thumbnail rendering',
		mirror: 'Mirror synchronization',
		converter: 'DOCX / ODT → PDF conversion'
	};

	let rows = $derived(
		tools.map((t) => ({
			...t,
			label: t.command || t.engine,
			purpose: t.purpose || CATEGORY_PURPOSE[t.category] || 'Pipeline tool'
		}))
	);

	let availableCount = $derived(tools.filter((t) => t.available).length);

	function hintsFor(tool) {
		const frontend = hintsForEngine(tool.engine);
		const source = Object.keys(frontend).length > 0 ? frontend : (tool.install_hints ?? {});
		return Object.entries(source);
	}
</script>

<section id="overview" class="group scroll-mt-6 pb-12">
	<GroupHead
		eyebrow="Overview"
		variant="op"
		title="State of the machine"
		description="The engines, tools and services the pipeline depends on — before you touch anything."
	/>

	<div class="space-y-4">
		<Card
			title="Toolchain"
			name="toolchain tools availability install"
			description="External programs the pipeline calls. Missing tools pause the stage that needs them."
		>
			{#snippet actions()}
				{#if statusKnown && tools.length > 0}
					<StatusBadge label="{availableCount} of {tools.length}" />
				{/if}
			{/snippet}

			{#if !statusKnown}
				<Note variant="warn" title="Tool status unavailable.">
					Availability could not be checked — restart the server or reload the page to retry.
				</Note>
			{:else if rows.length === 0}
				<p class="text-[0.8125rem] text-parchment-500">No external tools are configured.</p>
			{:else}
				<div>
					{#each rows as tool (tool.engine)}
						<div
							class="flex items-center gap-3 border-b border-dashed border-clay-800 py-2 last:border-b-0"
						>
							<span class="w-28 shrink-0 font-mono text-[0.8125rem] text-parchment-200"
								>{tool.label}</span
							>
							<span class="min-w-0 flex-1 text-[0.8125rem] text-parchment-500">{tool.purpose}</span>
							<StatusBadge
								variant={tool.available ? 'ok' : 'miss'}
								label={tool.available ? 'available' : 'not installed'}
							/>
						</div>
					{/each}
				</div>
				{#each rows.filter((t) => !t.available) as tool (tool.engine)}
					<details class="adv mt-4 border-t border-dashed border-clay-700 pt-3">
						<summary
							class="flex cursor-pointer list-none items-center gap-1.5 text-[0.8125rem] font-medium text-parchment-500 before:font-mono before:text-gold-500 before:content-['+'] open:before:content-['−'] focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none [&::-webkit-details-marker]:hidden"
						>
							Install {tool.label}
						</summary>
						<div class="mt-3">
							<Note variant="danger" title={`“${tool.engine}” is not installed.`}>
								{tool.purpose}.
								{#each hintsFor(tool) as [system, cmd] (system)}
									<pre class="mt-1 overflow-x-auto text-xs text-parchment-300">{system}: {cmd}</pre>
								{/each}
							</Note>
						</div>
					</details>
				{/each}
			{/if}
		</Card>
	</div>
</section>
