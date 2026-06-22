let _pending = $state(null);

function confirm({ title, message, danger = false }) {
	return new Promise((resolve) => {
		if (_pending) {
			_pending.resolve(false);
		}
		_pending = { title, message, danger, resolve };
	});
}

function resolve(val) {
	if (_pending) {
		_pending.resolve(val);
		_pending = null;
	}
}

export const confirmStore = {
	get pending() {
		return _pending;
	},
	confirm,
	resolve
};
