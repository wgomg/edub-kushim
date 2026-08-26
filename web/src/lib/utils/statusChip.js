const statusClasses = {
	waiting: 'bg-amber-600/20 text-amber-400',
	pending: 'bg-parchment-500/20 text-parchment-400',
	processing: 'bg-lapis-600/20 text-lapis-600',
	completed: 'bg-emerald-600/20 text-emerald-500',
	failed: 'bg-terracotta-600/20 text-terracotta-500',
	cancelled: 'bg-parchment-500/10 text-parchment-500',
	discarded: 'bg-terracotta-600/10 text-terracotta-400'
};

export function statusChipClasses(status) {
	return statusClasses[status] ?? 'bg-parchment-500/10 text-parchment-500';
}
