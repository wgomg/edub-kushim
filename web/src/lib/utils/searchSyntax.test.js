import { describe, it, expect } from 'vitest';
import { parseDateRange, parseSize } from './searchSyntax.js';

describe('parseSize', () => {
	it('parses operators, units and bare sizes', () => {
		expect(parseSize('>10MB')).toEqual({ op: '>', bytes: 10 * 1048576 });
		expect(parseSize('<=500kb')).toEqual({ op: '<=', bytes: 500 * 1024 });
		expect(parseSize('2mb')).toEqual({ op: '', bytes: 2 * 1048576 });
	});

	it('rejects text that is not size syntax', () => {
		expect(parseSize('hello')).toBeNull();
		expect(parseSize('10zz')).toBeNull();
	});
});

describe('parseDateRange', () => {
	it('parses ranges, operators and plain years', () => {
		expect(parseDateRange('2024-01..2024-06')).toEqual({ from: '2024-01', to: '2024-06' });
		expect(parseDateRange('>2024-01-01')).toEqual({ from: '2024-01-01', to: null });
		expect(parseDateRange('<=2024')).toEqual({ from: null, to: '2024' });
		expect(parseDateRange('2024')).toEqual({ from: '2024', to: null });
	});

	it('rejects text that is not a date range', () => {
		expect(parseDateRange('yesterday')).toBeNull();
	});
});
