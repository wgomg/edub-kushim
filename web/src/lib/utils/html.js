export function escapeHtml(str) {
	if (str == null) return '';
	return String(str)
		.replace(/&/g, '&amp;')
		.replace(/</g, '&lt;')
		.replace(/>/g, '&gt;')
		.replace(/"/g, '&quot;')
		.replace(/'/g, '&#39;');
}

export function formatSize(bytes) {
	if (bytes == null) return '0 B';
	const nf = new Intl.NumberFormat(undefined, { maximumFractionDigits: 1 });
	if (bytes < 1024) return nf.format(bytes) + '\u00A0B';
	if (bytes < 1024 * 1024) return nf.format(bytes / 1024) + '\u00A0KB';
	if (bytes < 1024 * 1024 * 1024) return nf.format(bytes / (1024 * 1024)) + '\u00A0MB';
	return nf.format(bytes / (1024 * 1024 * 1024)) + '\u00A0GB';
}

export function formatNumber(n) {
	if (n == null) return '0';
	return Number(n).toLocaleString();
}

export function formatDuration(ms) {
	if (ms == null) return '—';
	const totalSec = Math.floor(ms / 1000);
	const h = Math.floor(totalSec / 3600);
	const m = Math.floor((totalSec % 3600) / 60);
	const s = totalSec % 60;
	const parts = [];
	if (h > 0) parts.push(h + '\u00A0h');
	if (m > 0) parts.push(m + '\u00A0m');
	if (s > 0 || parts.length === 0) parts.push(s + '\u00A0s');
	return parts.join(' ');
}

export function formatRelative(dateStr) {
	if (!dateStr) return '—';
	const t = new Date(dateStr).getTime();
	if (Number.isNaN(t)) return '—';
	const sec = Math.floor((Date.now() - t) / 1000);
	if (sec < 0) return '—';
	if (sec < 60) return sec + 's';
	const min = Math.floor(sec / 60);
	if (min < 60) return min + 'm';
	const h = Math.floor(min / 60);
	if (h < 24) return h + 'h';
	return Math.floor(h / 24) + 'd';
}
