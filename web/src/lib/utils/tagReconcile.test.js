import { describe, it, expect } from 'vitest';
import {
	splitKeepFlips,
	sumReassigned,
	worklistTotal,
	changedDecisions,
	affectedDocuments,
	notActedGroups
} from './tagReconcile.js';

describe('splitKeepFlips', () => {
	it('returns empty arrays for empty report', () => {
		const { creates, retags } = splitKeepFlips({});
		expect(creates).toEqual([]);
		expect(retags).toEqual([]);
	});

	it('classifies creates_tag true entries into creates', () => {
		const report = {
			planned_keep_flips: [
				{ query: 'a', target: 'b', reassigned: 5, creates_tag: true },
				{ query: 'c', target: 'd', reassigned: 3 }
			]
		};
		const { creates, retags } = splitKeepFlips(report);
		expect(creates).toHaveLength(1);
		expect(creates[0].query).toBe('a');
		expect(retags).toHaveLength(1);
		expect(retags[0].query).toBe('c');
	});

	it('falls back to creates when creates_tag absent and reassigned is 0 (old reports)', () => {
		const report = {
			planned_keep_flips: [
				{ query: 'a', target: 'b', reassigned: 0 },
				{ query: 'c', target: 'd', reassigned: 0 }
			]
		};
		const { creates, retags } = splitKeepFlips(report);
		expect(creates).toHaveLength(2);
		expect(retags).toHaveLength(0);
	});

	it('does not fall back when creates_tag absent but reassigned > 0', () => {
		const report = {
			planned_keep_flips: [{ query: 'a', target: 'b', reassigned: 5 }]
		};
		const { creates, retags } = splitKeepFlips(report);
		expect(creates).toHaveLength(0);
		expect(retags).toHaveLength(1);
	});

	it('handles retag_entries (applied reports)', () => {
		const report = {
			retag_entries: [
				{ query: 'x', target: 'y', reassigned: 10, creates_tag: true },
				{ query: 'p', target: 'q', reassigned: 7 }
			]
		};
		const { creates, retags } = splitKeepFlips(report);
		expect(creates).toHaveLength(1);
		expect(creates[0].query).toBe('x');
		expect(retags).toHaveLength(1);
		expect(retags[0].query).toBe('p');
	});

	it('concatenates both arrays when both present', () => {
		const report = {
			planned_keep_flips: [{ query: 'a', target: 'b', reassigned: 0 }],
			retag_entries: [{ query: 'c', target: 'd', reassigned: 5 }]
		};
		const { creates, retags } = splitKeepFlips(report);
		expect(creates).toHaveLength(1);
		expect(retags).toHaveLength(1);
	});

	it('falls back to creates only when the field is absent (legacy reports)', () => {
		const report = {
			planned_keep_flips: [{ query: 'a', target: 'b', reassigned: 0 }]
		};
		const { creates, retags } = splitKeepFlips(report);
		expect(creates).toHaveLength(1);
		expect(retags).toHaveLength(0);
	});

	it('keeps creates_tag false with zero documents out of creates', () => {
		const report = {
			planned_keep_flips: [{ query: 'a', target: 'b', reassigned: 0, creates_tag: false }]
		};
		const { creates, retags } = splitKeepFlips(report);
		expect(creates).toHaveLength(0);
		expect(retags).toHaveLength(1);
	});
});

describe('sumReassigned', () => {
	it('returns 0 for empty input', () => {
		expect(sumReassigned([])).toBe(0);
	});

	it('sums reassigned values', () => {
		expect(sumReassigned([{ reassigned: 3 }, { reassigned: 7 }])).toBe(10);
	});

	it('handles null entries', () => {
		expect(sumReassigned(null)).toBe(0);
	});
});

describe('worklistTotal', () => {
	it('sums all worklist fields', () => {
		expect(worklistTotal({ stale: 1, unjudged: 2, drifted: 3, aged: 4 })).toBe(10);
	});

	it('handles missing fields', () => {
		expect(worklistTotal({ stale: 5 })).toBe(5);
	});
});

describe('changedDecisions', () => {
	it('sums to_merge and to_keep', () => {
		expect(changedDecisions({ flips: { to_merge: 2, to_keep: 3 } })).toBe(5);
	});

	it('handles missing flips', () => {
		expect(changedDecisions({})).toBe(0);
	});
});

describe('affectedDocuments', () => {
	it('sums reassigned across all entry arrays', () => {
		const report = {
			planned_merges: [{ reassigned: 5 }],
			planned_keep_flips: [{ reassigned: 3 }],
			merges: [{ reassigned: 10 }],
			retag_entries: [{ reassigned: 2 }]
		};
		expect(affectedDocuments(report)).toBe(20);
	});
});

describe('notActedGroups', () => {
	it('merges skip reasons and no_documents', () => {
		const groups = notActedGroups({ below_floor: 3, ambiguous: 2 }, 0, 5);
		expect(groups).toHaveLength(3);
		const labels = groups.map((g) => g.label);
		expect(labels).toContain('the tags are no longer similar enough to confuse');
		expect(labels).toContain(
			"the checker couldn't decide confidently — kept as-is, retried next run"
		);
		expect(labels).toContain('the tag was restored, but no documents carried these tags');
	});

	it('includes capped label', () => {
		const groups = notActedGroups({}, 4, 0);
		expect(groups).toHaveLength(1);
		expect(groups[0].label).toBe('run limit reached — remaining pairs wait for the next run');
		expect(groups[0].count).toBe(4);
	});
});
