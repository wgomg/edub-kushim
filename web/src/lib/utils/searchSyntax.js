const SIZE_UNITS = { b: 1, kb: 1024, mb: 1048576, gb: 1073741824 };

/**
 * Parses the `size:` filter syntax (`>10MB`, `<=500kb`, `2mb`) into an operator and a byte count.
 * Returns null when the text is not size syntax.
 */
export function parseSize(raw) {
	const match = raw.match(/^([<>]=?)?\s*(-?[\d.]+)\s*(kb|mb|gb|b)?$/i);
	if (!match) return null;
	const op = match[1] || '';
	const num = parseFloat(match[2]);
	const unit = (match[3] || 'b').toLowerCase();
	return { op, bytes: Math.round(num * (SIZE_UNITS[unit] || 1)) };
}

/**
 * Parses the `created:`/`modified:` filter syntax (`2024`, `2024-01..2024-06`, `>2024-01-01`) into a
 * from/to range. Returns null when the text is not date range syntax.
 */
export function parseDateRange(raw) {
	const rangeMatch = raw.match(/^([^.]*)\.\.(.+)$/);
	if (rangeMatch) return { from: rangeMatch[1] || null, to: rangeMatch[2] || null };
	const opMatch = raw.match(/^([<>]=?)\s*(.+)$/);
	if (opMatch) {
		if (opMatch[1] === '>' || opMatch[1] === '>=') return { from: opMatch[2], to: null };
		if (opMatch[1] === '<' || opMatch[1] === '<=') return { from: null, to: opMatch[2] };
	}
	const dateMatch = raw.match(/^\d{4}(-\d{2}(-\d{2})?)?$/);
	if (dateMatch) return { from: raw, to: null };
	return null;
}
