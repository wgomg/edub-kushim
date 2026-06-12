import { writable, derived } from 'svelte/store';
import { parseQueryString, serializeFilter, defaultFilter } from './searchFilter.js';

function createFilterStore() {
	const store = writable({ ...defaultFilter });

	return {
		subscribe: store.subscribe,
		set: store.set,
		update: store.update,
		setPartial(partial) {
			store.update((f) => ({ ...f, ...partial }));
		},
		reset() {
			store.set({ ...defaultFilter });
		},
		fromQueryString(str) {
			store.set(parseQueryString(str));
		}
	};
}

export const filterStore = createFilterStore();
export const queryString = derived(filterStore, ($f) => serializeFilter($f));
