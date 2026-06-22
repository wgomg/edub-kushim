let nextId = 1;
let _toasts = $state([]);

const TIMEOUTS = {
	error: 6000,
	success: 4000,
	warning: 4000,
	info: 4000
};

function push({ variant = 'info', message }) {
	const id = nextId++;
	const toast = { id, variant, message };
	if (_toasts.length >= 3) {
		_toasts.shift();
	}
	_toasts.push(toast);
	setTimeout(() => dismiss(id), TIMEOUTS[variant] ?? 4000);
}

function dismiss(id) {
	_toasts = _toasts.filter((t) => t.id !== id);
}

function error(message) {
	push({ variant: 'error', message });
}
function success(message) {
	push({ variant: 'success', message });
}
function warning(message) {
	push({ variant: 'warning', message });
}
function info(message) {
	push({ variant: 'info', message });
}

export const toastStore = {
	get toasts() {
		return _toasts;
	},
	push,
	dismiss,
	error,
	success,
	warning,
	info
};
