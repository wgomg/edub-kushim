<!--
  The in-app tag review reference: a hand-built digest of docs/manual/tags.md. Both artifacts are
  hand-synced — a badge, hold reason, or action change must land in this component and that document
  in the same change. The actionability classes come from the API (tags.CompositionActionFor); the
  prose tables here are documentation, not behavior.
-->
<script>
	import Icon from '#lib/components/Icon.svelte';
	import { demotionReasonLabels } from '#lib/utils/tagReconcile.js';

	let { section = 'overview' } = $props();

	let body = $state();

	const pillBase =
		'rounded-full px-2 py-0.5 font-mono text-[0.6875rem] font-medium whitespace-nowrap';
	const pillPlain = `${pillBase} bg-clay-800 text-parchment-400`;
	const pillOk = `${pillBase} bg-emerald-600/20 text-emerald-500`;
	const pillWarn = `${pillBase} bg-amber-600/20 text-amber-400`;

	const sectionTitle = 'mb-1.5 text-sm font-semibold text-parchment-200';
	const text = 'text-[0.8125rem] leading-relaxed text-parchment-400';
	const table = 'mt-2 w-full border-collapse text-left text-[0.75rem]';
	const th = 'border-b border-clay-800 py-1.5 pe-3 font-medium text-parchment-300';
	const td = 'border-b border-clay-800/60 py-1.5 pe-3 align-top text-parchment-400';
	const code = 'font-mono text-[0.6875rem] text-parchment-300';

	const holdReasons = [
		{
			reason: 'split_defer',
			meaning: 'the split judge abstained or the parts pass deferred the call',
			resolve: 'Split now, or keep whole'
		},
		{
			reason: 'conflicting_evidence',
			meaning: 'the parts and the compound point in opposite directions',
			resolve: 'Split now, or keep whole'
		},
		{
			reason: 'sample_disagreement',
			meaning: 'two samples of the corpus disagree about the compound',
			resolve: 'Split now, or keep whole'
		},
		{
			reason: 'relational_keep',
			meaning: 'the compound selects more than its parts share, so it is not their product',
			resolve: 'Apply the parts alongside, or keep whole'
		},
		{
			reason: 'judge_unavailable',
			meaning: 'no split judge answered, so nothing was decided',
			resolve: 'Re-judge when a judge is configured'
		},
		{
			reason: 'no_dimension',
			meaning: 'the compound was decided to name no dimension',
			resolve: 'Retire it — approving does not stick for a decided drop'
		},
		{
			reason: 'part_rejected',
			meaning: 'every part of the split was rejected, so the compound goes with them',
			resolve: 'Retire it — approving does not stick for a decided drop'
		}
	];

	const demotedReasons = [
		'mass_decay',
		'no_dimension',
		'part_rejected',
		'judge_defer',
		'corroborated_failures',
		'person_facet',
		'person_review',
		'deterministic_only',
		'single_signal',
		'operator'
	];

	const actions = [
		{
			icon: 'check',
			name: 'Approve',
			text: 'Promotes the tag into the shared vocabulary. It overrides an undecided verdict or hold, but a decided drop (no_dimension, part_rejected) is re-applied by the next reconciliation run — retire those instead.'
		},
		{
			icon: 'close',
			name: 'Reject',
			text: 'Deletes the tag and records its name in the blocklist, so enrichment never proposes it again. Restorable from the Deleted tab.'
		},
		{
			icon: 'retire',
			name: 'Retire',
			text: 'Deletes the tag without blocklisting the name: the assignments are removed now, but enrichment may propose the value again.'
		},
		{
			icon: 'circle-check',
			name: 'Accept as provisional',
			text: 'Waives a review deferral: the tag is no longer deferred and waits for the promotion floor like any admitted value, promoting once enough live documents carry it. A newer verdict (a rubric version change or a re-judge) re-opens the deferral so the action can be used again. Offered only when no identity or split hold still withholds the tag.'
		},
		{
			icon: 'edit',
			name: 'Rename',
			text: 'Changes the tag name in place. The verdict and hold journals are keyed by name, so a renamed tag shows no verdict until it is judged again under the new name.'
		},
		{
			icon: 'fold',
			name: 'Fold into the existing value',
			text: 'Collapses this tag onto the value named by its identity hold. The documents move onto the kept value and this name becomes an alias of it, disappearing from the vocabulary.'
		},
		{
			icon: 'fold',
			name: 'Fold into…',
			text: 'Folds this tag into any other live tag you pick. Available on every entry in this queue and on every Vocabulary row; the documents move onto the kept value and this name becomes an alias of it. Seeded values cannot be folded away.'
		},
		{
			icon: 'make-canonical',
			name: 'Make canonical',
			text: 'The reverse fold: the held store value is collapsed onto this tag. The old value loses its row and standing, its documents move here, and this tag stays provisional until approved.'
		},
		{
			icon: 'keep-separate',
			name: 'Keep separate',
			text: 'Records that this tag names its own value: the identity hold clears, both values stay in the vocabulary, and this tag keeps waiting on its own verdict and the promotion floor.'
		},
		{
			icon: 'split',
			name: 'Split now',
			text: 'Replaces the compound with the components you confirm: the components are created and the compound\u2019s documents are attached to them, then the compound is deleted.'
		},
		{
			icon: 'split',
			name: 'Split…',
			text: 'Splits a compound tag into components you choose, whatever the split judge decided: the components are created and the tag\u2019s documents are attached to them, then the tag is deleted. Offered on every compound entry in this queue and on every compound Vocabulary row; seeded values cannot be split.'
		},
		{
			icon: 'keep-whole',
			name: 'Keep whole',
			text: 'Records your decision that the compound is a value in its own right. The hold clears and the tag stays as it is.'
		},
		{
			icon: 'apply-parts',
			name: 'Apply the parts alongside',
			text: 'Keeps the compound and attaches its parts to the same documents, so both the whole and its components are searchable. No documents are re-pointed.'
		},
		{
			icon: 'retry',
			name: 'Re-judge',
			text: 'Queues a fresh judgment by the configured judge. The queue refreshes when the background task finishes; the badge updates on the next load.'
		},
		{
			icon: 'judge-now',
			name: 'Judge now',
			text: 'Queues the first quality judgment for a tag that was never judged, instead of waiting for the next enrichment or maintenance pass.'
		}
	];

	$effect(() => {
		if (!body) return;
		const target = body.querySelector(`#help-${section}`);
		if (target) target.scrollIntoView({ block: 'start' });
	});
</script>

<div bind:this={body} class="flex flex-col gap-6">
	<section id="help-overview">
		<h3 class={sectionTitle}>The review queue</h3>
		<p class={text}>
			A new tag stays <b class="text-parchment-300">provisional</b> until the admission decision admits
			it and enough live documents carry it to clear the promotion floor. Values the judges are unsure
			about — or that identity and split decisions left held — wait here for a person to decide. Approve
			promotes a tag into the shared vocabulary; reject deletes it and blocks the name.
		</p>
		<p class="{text} mt-2">
			A fresh collection fills this queue quickly — every name is new and the judges have no stored
			decision yet. Most entries settle without review: the scheduled passes promote values that
			clear the recurrence floor, merge near-duplicates, and retire decided non-values, while later
			emissions reuse the recorded decisions. The queue shrinks as the vocabulary grows, leaving the
			genuinely novel or unresolved names.
		</p>
	</section>

	<section id="help-verdict">
		<h3 class={sectionTitle}>Verdict badge</h3>
		<p class={text}>The latest quality verdict recorded for this tag by the tag judge.</p>
		<ul class="mt-2 flex flex-col gap-1.5">
			<li>
				<span class={pillPlain}>no verdict</span>
				<span class="ms-1.5 {text}">never judged under the current rules.</span>
			</li>
			<li>
				<span class={pillOk}>awaiting promotion floor</span>
				<span class="ms-1.5 {text}">admitted, but not enough live documents carry it yet.</span>
			</li>
			<li>
				<span class={pillWarn}>deferred to review</span>
				<span class="ms-1.5 {text}">the judge abstained or the evidence was inconclusive.</span>
			</li>
			<li>
				<span class={pillOk}>accepted — awaiting promotion floor</span>
				<span class="ms-1.5 {text}"
					>an operator waived the deferral; the value waits for the promotion floor.</span
				>
			</li>
			<li>
				<span class={pillWarn}>judge rejected</span>
				<span class="ms-1.5 {text}">the judge found the value failing the rubric.</span>
			</li>
		</ul>
	</section>

	<section id="help-identity-hold">
		<h3 class={sectionTitle}>Identity hold</h3>
		<p class={text}>
			The identity judge refused to fold this tag onto an existing store value, so the two names
			stay separate until someone decides. Use
			<b class="text-parchment-300">Fold into the existing value</b> when they mean the same thing
			and the established value should win, <b class="text-parchment-300">Make canonical</b> when
			this tag is the better name, or <b class="text-parchment-300">Keep separate</b> when the two are
			genuinely different values — the hold clears and this tag keeps answering to its own verdict and
			promotion floor. Approving promotes it into the vocabulary immediately instead.
		</p>
		<p class={text}>
			When the judge labelled the term's fold relation, the pill also shows the class: a
			<b class="text-parchment-300">relational adjective</b> (its noun denotes the same thing), an
			<b class="text-parchment-300">inflection</b> (a plural or other inflected form), a
			<b class="text-parchment-300">derivation</b> (a lexicalized derivation or gerund naming its
			own concept), or <b class="text-parchment-300">no fold class</b>. The class is evidence for
			the review decision, not the decision itself: a refusal beside a recognised relational
			adjective may still deserve the fold, while a refusal that saw no relation at all points the
			other way.
		</p>
	</section>

	<section id="help-identity-pending">
		<h3 class={sectionTitle}>Identity pending</h3>
		<p class={text}>
			A same-intent claim about this tag is waiting for the identity pass. The daily pass resolves
			it on its own; <b class="text-parchment-300">Reevaluate identity</b> in the queue header runs it
			now.
		</p>
	</section>

	<section id="help-split-hold">
		<h3 class={sectionTitle}>Split hold</h3>
		<p class={text}>
			The composition judge could not settle this compound, so promotion waits. The reason says why,
			and each reason suggests its own resolution.
		</p>
		<table class={table}>
			<thead>
				<tr>
					<th class={th}>Reason</th>
					<th class={th}>Meaning</th>
					<th class={th}>Usual resolution</th>
				</tr>
			</thead>
			<tbody>
				{#each holdReasons as row (row.reason)}
					<tr>
						<td class={td}><span class={code}>{row.reason}</span></td>
						<td class={td}>{row.meaning}</td>
						<td class={td}>{row.resolve}</td>
					</tr>
				{/each}
			</tbody>
		</table>
	</section>

	<section id="help-composition-degraded">
		<h3 class={sectionTitle}>Degraded composition</h3>
		<p class={text}>
			The last split decision was recorded without a judge answer. The hold no longer applies
			because the recorded decision predates the current criteria, so the compound waits to be
			judged again. Use <b class="text-parchment-300">Re-judge</b> once a split judge is configured.
		</p>
	</section>

	<section id="help-demoted">
		<h3 class={sectionTitle}>Demoted</h3>
		<p class={text}>
			The value's standing was revoked, so it is provisional again. Approve restores it; reject
			deletes it. The reason names what revoked it:
		</p>
		<ul class="mt-2 flex flex-col gap-1">
			{#each demotedReasons as reason (reason)}
				<li class={text}>
					<span class={code}>{reason}</span>
					<span class="ms-1.5">— {demotionReasonLabels[reason] ?? 'the recorded trigger'}</span>
				</li>
			{/each}
		</ul>
	</section>

	<section id="help-actions">
		<h3 class={sectionTitle}>Actions</h3>
		<ul class="mt-2 flex flex-col gap-3">
			{#each actions as action (action.name)}
				<li class="flex gap-2.5">
					<span class="mt-0.5 text-parchment-500">
						<Icon name={action.icon} size={14} />
					</span>
					<span>
						<b class="text-[0.8125rem] font-medium text-parchment-300">{action.name}</b>
						<span class="block {text}">{action.text}</span>
					</span>
				</li>
			{/each}
		</ul>
	</section>

	<p class="border-t border-clay-800 pt-3 text-[0.75rem] text-parchment-600">
		Full reference: docs/manual/tags.md — this panel is its in-app digest.
	</p>
</div>
