import { describe, it, expect, vi, afterEach } from 'vitest';

vi.mock('$app/paths', () => ({
	resolve: (path) => path
}));

import {
	formatSize,
	formatDuration,
	formatDate,
	formatDateTime,
	formatRelative,
	formatNumber,
	documentsLabel,
	documentsForTagHref
} from './html.js';

describe('formatSize', () => {
	const nf = new Intl.NumberFormat(undefined, { maximumFractionDigits: 1 });

	it('returns 0 B for null', () => {
		expect(formatSize(null)).toBe('0 B');
	});

	it('formats byte, KB, MB and GB boundaries', () => {
		expect(formatSize(0)).toBe(nf.format(0) + '\u00A0B');
		expect(formatSize(1023)).toBe(nf.format(1023) + '\u00A0B');
		expect(formatSize(1024)).toBe(nf.format(1) + '\u00A0KB');
		expect(formatSize(1536)).toBe(nf.format(1.5) + '\u00A0KB');
		expect(formatSize(1048576)).toBe(nf.format(1) + '\u00A0MB');
		expect(formatSize(1073741824)).toBe(nf.format(1) + '\u00A0GB');
	});
});

describe('documentsLabel', () => {
	it('labels zero, one and many documents', () => {
		expect(documentsLabel(0)).toBe('0 documents');
		expect(documentsLabel(1)).toBe('1 document');
		expect(documentsLabel(12)).toBe(`${formatNumber(12)} documents`);
	});

	it('treats null and undefined as zero', () => {
		expect(documentsLabel(null)).toBe('0 documents');
		expect(documentsLabel(undefined)).toBe('0 documents');
	});
});

describe('formatDuration', () => {
	it('returns an em dash for null', () => {
		expect(formatDuration(null)).toBe('—');
	});

	it('composes h/m/s parts, omitting zero leading parts', () => {
		expect(formatDuration(0)).toBe('0\u00A0s');
		expect(formatDuration(5000)).toBe('5\u00A0s');
		expect(formatDuration(60000)).toBe('1\u00A0m');
		expect(formatDuration(61000)).toBe('1\u00A0m 1\u00A0s');
		expect(formatDuration(3600000)).toBe('1\u00A0h');
		expect(formatDuration(3661000)).toBe('1\u00A0h 1\u00A0m 1\u00A0s');
	});
});

describe('documentsForTagHref', () => {
	it('produces a URL-encoded documents filter link for a tag name', () => {
		expect(documentsForTagHref('software development')).toBe(
			'/documents?tag=software%20development'
		);
	});

	it('encodes special characters in tag names', () => {
		expect(documentsForTagHref('C++')).toBe('/documents?tag=C%2B%2B');
	});
});

describe('formatRelative', () => {
	afterEach(() => {
		vi.useRealTimers();
	});

	it('formats seconds, minutes, hours and days against a fixed now', () => {
		vi.useFakeTimers();
		vi.setSystemTime(new Date('2026-01-01T00:00:00Z'));
		expect(formatRelative('2025-12-31T23:59:30Z')).toBe('30s');
		expect(formatRelative('2025-12-31T23:59:00Z')).toBe('1m');
		expect(formatRelative('2025-12-31T23:00:00Z')).toBe('1h');
		expect(formatRelative('2025-12-01T00:00:00Z')).toBe('31d');
	});

	it('returns an em dash for missing, invalid or future dates', () => {
		vi.useFakeTimers();
		vi.setSystemTime(new Date('2026-01-01T00:00:00Z'));
		expect(formatRelative('')).toBe('—');
		expect(formatRelative('not-a-date')).toBe('—');
		expect(formatRelative('2026-01-02T00:00:00Z')).toBe('—');
	});
});

describe('formatDate', () => {
	it('renders an em dash for missing input', () => {
		expect(formatDate('')).toBe('—');
		expect(formatDate(null)).toBe('—');
	});

	it('formats an instant as a date', () => {
		const instant = '2024-05-06T10:30:00Z';
		const expected = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium' }).format(
			new Date(instant)
		);
		expect(formatDate(instant)).toBe(expected);
	});
});

describe('formatDateTime', () => {
	it('renders an em dash for missing input', () => {
		expect(formatDateTime(undefined)).toBe('—');
	});

	it('formats an instant as a date with time', () => {
		const instant = '2024-05-06T10:30:00Z';
		const expected = new Intl.DateTimeFormat(undefined, {
			dateStyle: 'medium',
			timeStyle: 'short'
		}).format(new Date(instant));
		expect(formatDateTime(instant)).toBe(expected);
	});
});
