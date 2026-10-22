const statusClasses = {
	waiting: 'bg-amber-600/20 text-amber-400',
	pending: 'bg-parchment-500/20 text-parchment-400',
	processing: 'bg-lapis-600/20 text-lapis-600',
	completed: 'bg-emerald-600/20 text-emerald-500',
	failed: 'bg-terracotta-600/20 text-terracotta-500',
	cancelled: 'bg-parchment-500/10 text-parchment-500',
	discarded: 'bg-terracotta-600/10 text-terracotta-400'
};

const originClasses = {
	cli: 'bg-emerald-600/20 text-emerald-500',
	api: 'bg-lapis-600/20 text-lapis-600',
	polling: 'bg-cyan-600/20 text-cyan-400',
	schedule: 'bg-amber-600/20 text-amber-400',
	setup: 'bg-terracotta-600/20 text-terracotta-500',
	wizard: 'bg-violet-600/20 text-violet-400'
};

const bandClasses = {
	waiting: 'bg-amber-600/15 text-amber-400 border-b-amber-600/35',
	pending: 'bg-parchment-500/15 text-parchment-400 border-b-parchment-500/25',
	processing: 'bg-lapis-600/25 text-lapis-300 border-b-lapis-600/45',
	completed: 'bg-emerald-600/20 text-emerald-400 border-b-emerald-600/40',
	failed: 'bg-terracotta-600/25 text-terracotta-400 border-b-terracotta-600/45',
	cancelled: 'bg-parchment-500/10 text-parchment-500 border-b-parchment-500/20',
	discarded: 'bg-terracotta-600/10 text-terracotta-400 border-b-terracotta-600/20'
};

export function statusBandClasses(status) {
	return bandClasses[status] ?? 'bg-parchment-500/10 text-parchment-500 border-b-parchment-500/20';
}

export function statusChipClasses(status) {
	return statusClasses[status] ?? 'bg-parchment-500/10 text-parchment-500';
}

export function originBadgeClasses(origin) {
	return originClasses[origin] ?? 'bg-parchment-500/10 text-parchment-500';
}
