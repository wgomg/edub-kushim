const statusClasses = {
	waiting: 'bg-amber-600/20 text-amber-400',
	pending: 'bg-parchment-500/20 text-parchment-400',
	processing: 'bg-lapis-600/20 text-lapis-600',
	completed: 'bg-emerald-600/20 text-emerald-500',
	failed: 'bg-terracotta-600/20 text-terracotta-500',
	cancelled: 'bg-parchment-500/10 text-parchment-500',
	discarded: 'bg-terracotta-600/10 text-terracotta-400'
};

const sourceClasses = {
	polling: 'bg-lapis-600/20 text-lapis-600',
	cli: 'bg-emerald-600/20 text-emerald-500',
	config: 'bg-amber-600/20 text-amber-400',
	backup: 'bg-terracotta-600/20 text-terracotta-500',
	mirror: 'bg-violet-600/20 text-violet-400',
	thumbbackfill: 'bg-parchment-500/20 text-parchment-400'
};

export function statusChipClasses(status) {
	return statusClasses[status] ?? 'bg-parchment-500/10 text-parchment-500';
}

export function sourceBadgeClasses(source) {
	return sourceClasses[source] ?? 'bg-parchment-500/10 text-parchment-500';
}
