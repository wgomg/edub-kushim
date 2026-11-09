/**
 * Owns the debounced free-text draft bound to a list's `q` filter. Inputs bind to `value` directly;
 * user edits commit to the list after a pause. While the field is focused the draft wins; when it
 * loses focus a pending edit commits immediately, so no stale timer can later overwrite an external
 * URL write. The timer is also cancelled on destroy, so a pending edit cannot write into a different
 * route's URL. All current lists use `q` as the free-text key.
 */
export class QueryDraft {
	value = $state('');

	#list;
	#focused = $state(false);
	#edited = $state(false);
	#lastCommitted = null;
	#timer;

	constructor(list) {
		this.#list = list;

		// the URL (or another writer) wins while the field is not being edited; the sync effect keys on
		// the committed value actually moving, so our own commit — whose URL write trails the update
		// call — does not revert the draft mid-flight
		$effect(() => {
			const committed = list.query.q;
			if (committed === this.#lastCommitted) return;
			this.#lastCommitted = committed;
			if (this.#focused) return;
			this.value = committed;
			this.#edited = false;
		});

		// only user edits commit, and only after the pause; clearing first means typing back to the
		// committed value drops the pending write
		$effect(() => {
			const value = this.value;
			if (!this.#focused || !this.#edited) return;

			clearTimeout(this.#timer);
			if (value === this.#list.query.q) return;

			this.#timer = setTimeout(() => {
				this.#edited = false;
				this.#list.update({ q: value });
			}, 250);
		});

		$effect(() => () => clearTimeout(this.#timer));
	}

	/** Marks the current `value` as user-entered so the debounce schedules. */
	markEdited = () => {
		this.#edited = true;
	};

	focus = () => {
		this.#focused = true;
	};

	blur = () => {
		this.#focused = false;

		// blur ends the window where the sync effect ignores the URL, so this method reconciles both
		// sides: an uncommitted edit goes to the list immediately because typed text must never be
		// silently lost, and with no edit pending the committed value is adopted so changes made
		// elsewhere are shown
		clearTimeout(this.#timer);
		if (this.#edited && this.value !== this.#list.query.q) {
			this.#edited = false;
			this.#list.update({ q: this.value });
			return;
		}

		this.#edited = false;
		this.value = this.#list.query.q;
	};
}
