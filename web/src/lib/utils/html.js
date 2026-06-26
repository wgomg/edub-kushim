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
	if (bytes < 1024) return bytes + ' B';
	if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
	if (bytes < 1024 * 1024 * 1024) return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
	return (bytes / (1024 * 1024 * 1024)).toFixed(1) + ' GB';
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
	if (h > 0) parts.push(h + 'h');
	if (m > 0) parts.push(m + 'm');
	if (s > 0 || parts.length === 0) parts.push(s + 's');
	return parts.join(' ');
}
