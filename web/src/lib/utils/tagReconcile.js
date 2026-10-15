export const worklistReasonLabels = {
	stale: 'the decision was made under older rules',
	unjudged: 'never actually reviewed by the tag adjudicator',
	drifted: 'how documents use these tags has changed',
	aged: 'not re-checked in over 90 days',
	compounds: 'stored compounds to reconsider for decomposition',
	twins: 'near-identical label pairs proposed by the measurement sweep'
};

const skipReasonLabels = {
	rank_error: 'automatic judging failed this run — retried next run',
	adjudication_error: 'automatic judging failed this run — retried next run',
	confirmation_error: 'automatic judging failed this run — retried next run',
	alias_error: 'applying the alias failed this run — retried next run',
	alias_skip: 'the alias collapse no longer applied — skipped',
	ambiguous: "the checker couldn't decide confidently — kept as-is, retried next run",
	confirmation_ambiguous: "the checker couldn't decide confidently — kept as-is, retried next run",
	confirmation_disagreement:
		"the checker couldn't decide confidently — kept as-is, retried next run",
	below_floor: 'the tags are no longer similar enough to confuse',
	query_reborn: 'the tag already exists — nothing left to do',
	no_documents: 'the tag was restored, but no documents carried these tags',
	undecided_no_judge: 'no adjudicator is configured — the compound stays as it is',
	decided: 'already decided — nothing to do',
	identity_hold: 'held for review beside a live store value',
	composition_hold: 'held for review',
	period_store_application: 'a seeded period — applied whole by design',
	seeded: 'a seeded value — operator-owned and never swept',
	split_invalid: 'the components did not validate — kept whole',
	split_error: 'the split failed this run — retried next run',
	identity_evidence_error: 'recording the candidate pair failed this run — retried next run'
};

// One vocabulary for demotion triggers and outcomes, read both as a demoted value's reason label and as
// the run tally's count labels, so a new trigger is named once
export const demotionReasonLabels = {
	worklist: 'checked',
	demoted: 'revoked',
	mass_decay: 'below the recurrence floor',
	no_dimension: 'decided drops',
	part_rejected: 'every part of it was rejected',
	rejudged: 're-judged',
	corroborated_failures: 'rejected by the current criteria',
	judge_defer: 'deferred by the current criteria',
	operator: 'an operator revocation',
	held: 'held for review',
	no_judge: 'kept without a judge',
	skipped: 'skipped',
	failed: 'failed'
};

export function demotionLabel(reason) {
	return demotionReasonLabels[reason] ?? reason ?? 'influence revoked';
}

const cappedLabel = 'run limit reached — remaining pairs wait for the next run';

export function worklistTotal(worklist = {}) {
	return (
		(worklist?.stale ?? 0) +
		(worklist?.unjudged ?? 0) +
		(worklist?.drifted ?? 0) +
		(worklist?.aged ?? 0) +
		(worklist?.compounds ?? 0) +
		(worklist?.twins ?? 0)
	);
}

export function changedDecisions(report = {}) {
	return (report?.flips?.to_alias ?? 0) + (report?.flips?.to_keep ?? 0);
}

export function demotedValues(report = {}) {
	return (report?.report_only ? report?.planned_demotions : report?.demoted) ?? [];
}

export function demotionCountGroups(report = {}) {
	const counts = report?.demotion ?? {};
	return Object.entries(demotionReasonLabels)
		.filter(([key]) => (counts[key] ?? 0) > 0)
		.map(([key, label]) => ({ label, count: counts[key] }));
}

export function affectedDocuments(report = {}) {
	return [
		...(report?.planned_aliases ?? []),
		...(report?.planned_keep_flips ?? []),
		...(report?.aliases ?? []),
		...(report?.retag_entries ?? [])
	].reduce((sum, entry) => sum + (entry?.reassigned ?? 0), 0);
}

export function sumReassigned(entries = []) {
	return (entries ?? []).reduce((sum, entry) => sum + (entry?.reassigned ?? 0), 0);
}

export function splitKeepFlips(report = {}) {
	const entries = [...(report?.planned_keep_flips ?? []), ...(report?.retag_entries ?? [])];
	const creates = [];
	const retags = [];
	for (const entry of entries) {
		// reports persisted before creates_tag existed fall back to the document
		// count, where a zero-document keep flip can only mean a tag creation
		const createsTag =
			entry?.creates_tag === true ||
			(entry?.creates_tag === undefined && (entry?.reassigned ?? 0) === 0);
		if (createsTag) creates.push(entry);
		else retags.push(entry);
	}
	return { creates, retags };
}

export function worklistReasons(worklist = {}) {
	return Object.entries(worklistReasonLabels)
		.filter(([key]) => (worklist?.[key] ?? 0) > 0)
		.map(([, label]) => label);
}

export function notActedGroups(skips = {}, capped = 0, noDocuments = 0) {
	const counts = new Map();
	for (const [key, count] of Object.entries(skips ?? {})) {
		if (!count) continue;
		const label = skipReasonLabels[key] ?? key;
		counts.set(label, (counts.get(label) ?? 0) + count);
	}
	if (noDocuments > 0) {
		const label = skipReasonLabels.no_documents;
		counts.set(label, (counts.get(label) ?? 0) + noDocuments);
	}
	if (capped > 0) counts.set(cappedLabel, (counts.get(cappedLabel) ?? 0) + capped);
	return Array.from(counts, ([label, count]) => ({ label, count }));
}
