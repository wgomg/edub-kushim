<script>
	import MenuPopover from '#lib/components/MenuPopover.svelte';

	let {
		q = '',
		criteria = [],
		criteriaStates = {},
		reviewStates = [],
		states = [],
		facets = null,
		onquerychange = null,
		onchange = null,
		onfacets = null,
		onfacetsclose = null
	} = $props();

	const SEGMENTS = [
		{ value: '', label: 'Any', text: 'any' },
		{ value: 'pass', label: '✓', text: 'pass' },
		{ value: 'fail', label: '✗', text: 'fail' },
		{ value: 'absent', label: '—', text: 'absent' }
	];

	const PRESETS = [
		{
			value: 'needs-decision',
			label: 'Needs decision',
			states: ['identity_hold', 'split_hold', 'degraded', 'needs_decision']
		},
		{
			value: 'waiting',
			label: 'Waiting on system',
			states: ['identity_pending', 'accepted']
		},
		{ value: 'demoted', label: 'Demoted', states: ['demoted'] },
		{ value: 'unblocked', label: 'Awaiting floor', states: ['unblocked'] }
	];

	const searchClass =
		'w-full max-w-xs rounded-md border border-clay-700 bg-clay-900 px-3 py-1.5 text-sm text-parchment-200 placeholder-parchment-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none';
	const btnGhost =
		'rounded-lg px-3 py-1.5 text-xs text-parchment-400 hover:bg-clay-800 hover:text-terracotta-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-50';
	const presetClass =
		'rounded-full border px-2.5 py-0.5 text-xs focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none';
	const chipClass =
		'inline-flex items-center gap-1 rounded-full border border-gold-500/40 bg-gold-500/10 px-2 py-0.5 text-xs text-gold-400';
	const chipRemoveClass =
		'rounded-full px-1 text-parchment-400 hover:bg-clay-800 hover:text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none';
	const groupClass =
		'mt-2 mb-1 px-2 text-[0.59375rem] font-semibold tracking-[0.09em] text-parchment-600 uppercase first:mt-0';
	const optionClass =
		'flex cursor-pointer items-center gap-2 rounded px-2 py-1 text-xs text-parchment-300 hover:bg-clay-800';
	const countClass = 'ml-auto font-mono text-[0.6875rem] text-parchment-600 tabular-nums';
	const segmentClass =
		'min-w-7 px-1.5 py-0.5 font-mono text-[0.6875rem] focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none';
	const segmentActive = 'bg-gold-500 font-bold text-clay-950';
	const segmentIdle = 'bg-clay-900 text-parchment-500 hover:bg-clay-800 hover:text-parchment-200';

	let groups = $derived(
		reviewStates.reduce((acc, state) => {
			let group = acc.find((g) => g.name === state.group);
			if (!group) {
				group = { name: state.group, states: [] };
				acc.push(group);
			}
			group.states.push(state);
			return acc;
		}, [])
	);

	let stateMenuOpen = $state(false);
	let rubricMenuOpen = $state(false);
	let stateMenuWasOpen = false;

	$effect(() => {
		if (stateMenuOpen) {
			rubricMenuOpen = false;
			stateMenuWasOpen = true;
		} else if (stateMenuWasOpen) {
			stateMenuWasOpen = false;
			onfacetsclose?.();
		}
	});

	$effect(() => {
		if (rubricMenuOpen) stateMenuOpen = false;
	});

	let selected = $derived(reviewStates.filter((state) => states.includes(state.value)));
	let activeCriteria = $derived(criteria.filter((c) => criteriaStates[c.key]));
	let hasActiveChips = $derived(states.length > 0 || activeCriteria.length > 0);

	const mark = (value) => (value === 'pass' ? '✓' : value === 'fail' ? '✗' : '—');

	function emitStates(nextStates) {
		if (onchange) onchange({ q, criteria: criteriaStates, states: nextStates });
	}

	function toggleState(value) {
		emitStates(states.includes(value) ? states.filter((s) => s !== value) : [...states, value]);
	}

	function setCriterion(key, value) {
		const current = criteriaStates[key] ?? '';
		const next = current === value ? '' : value;
		if (onchange) onchange({ q, criteria: { ...criteriaStates, [key]: next }, states });
	}

	function removeCriterion(key) {
		if (onchange) onchange({ q, criteria: { ...criteriaStates, [key]: '' }, states });
	}

	function applyPreset(preset) {
		const active = presetActive(preset);
		emitStates(active ? [] : [...preset.states]);
	}

	function presetActive(preset) {
		return (
			preset.states.length === states.length &&
			preset.states.every((state) => states.includes(state))
		);
	}

	function clear() {
		if (onchange) onchange({ q: '', criteria: {}, states: [] });
	}
</script>

<div class="mb-4 flex flex-col gap-2">
	<div class="flex flex-wrap items-center gap-2">
		<label for="provisional-filter" class="sr-only">Filter new tags by name</label>
		<input
			id="provisional-filter"
			name="provisional-filter"
			type="text"
			value={q}
			oninput={(e) => onquerychange?.(e.target.value)}
			autocomplete="off"
			placeholder="Filter new tags…"
			class={searchClass}
		/>

		<MenuPopover
			bind:open={stateMenuOpen}
			label="Review state"
			badge={states.length}
			onopen={onfacets}
		>
			{#each groups as group (group.name)}
				<p class={groupClass}>{group.name}</p>
				{#each group.states as state (state.value)}
					<label class={optionClass}>
						<input
							type="checkbox"
							class="h-3.5 w-3.5 cursor-pointer accent-gold-500"
							checked={states.includes(state.value)}
							onchange={() => toggleState(state.value)}
						/>
						<span>{state.label}</span>
						{#if facets && facets[state.value] !== undefined}
							<span class={countClass}>{facets[state.value]}</span>
						{/if}
					</label>
				{/each}
			{/each}
		</MenuPopover>

		<MenuPopover bind:open={rubricMenuOpen} label="Rubric" badge={activeCriteria.length}>
			{#each criteria as c (c.key)}
				<div class="flex items-center justify-between gap-2 px-2 py-1">
					<span class="text-xs text-parchment-300">{c.label}</span>
					<div class="inline-flex overflow-hidden rounded border border-clay-700">
						{#each SEGMENTS as segment, index (segment.value)}
							<button
								type="button"
								class="{segmentClass} {index > 0
									? 'border-l border-clay-800'
									: ''} {(criteriaStates[c.key] ?? '') === segment.value
									? segmentActive
									: segmentIdle}"
								aria-pressed={(criteriaStates[c.key] ?? '') === segment.value}
								aria-label="{c.label}: {segment.text}"
								onclick={() => setCriterion(c.key, segment.value)}
							>
								{segment.label}
							</button>
						{/each}
					</div>
				</div>
			{/each}
		</MenuPopover>

		{#if q !== '' || hasActiveChips}
			<button type="button" onclick={clear} class={btnGhost}>Clear</button>
		{/if}
	</div>

	<div class="flex flex-wrap items-center gap-1.5 border-t border-dashed border-clay-800 pt-2">
		<span
			class="mr-0.5 text-[0.59375rem] font-semibold tracking-[0.09em] text-parchment-600 uppercase"
			>Quick views</span
		>
		{#each PRESETS as preset (preset.value)}
			<button
				type="button"
				class="{presetClass} {presetActive(preset)
					? 'border-gold-500 bg-gold-500/10 text-gold-400'
					: 'border-clay-700 text-parchment-400 hover:border-gold-500 hover:text-gold-400'}"
				aria-pressed={presetActive(preset)}
				onclick={() => applyPreset(preset)}
			>
				{preset.label}
			</button>
		{/each}
		<span class="ml-auto hidden text-[0.6875rem] text-parchment-600 md:block"
			>presets fill the state menu; the chips refine</span
		>
	</div>

	{#if hasActiveChips}
		<div class="flex flex-wrap items-center gap-1.5">
			<span
				class="mr-0.5 text-[0.59375rem] font-semibold tracking-[0.09em] text-parchment-600 uppercase"
				>Filters</span
			>
			{#each selected as state (state.value)}
				<span class={chipClass}>
					{state.label}
					<button
						type="button"
						class={chipRemoveClass}
						aria-label="Remove {state.label} filter"
						onclick={() => toggleState(state.value)}>×</button
					>
				</span>
			{/each}
			{#each activeCriteria as c (c.key)}
				<span class={chipClass}>
					{c.label}
					{mark(criteriaStates[c.key])}
					<button
						type="button"
						class={chipRemoveClass}
						aria-label="Remove {c.label} filter"
						onclick={() => removeCriterion(c.key)}>×</button
					>
				</span>
			{/each}
		</div>
	{/if}
</div>
