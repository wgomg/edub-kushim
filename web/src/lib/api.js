import { getToken, logout as authLogout } from '$lib/stores/authStore.js';
import { goto } from '$app/navigation';
import { resolve } from '$app/paths';

let _redirecting = false;

async function handleUnauthorized() {
	if (_redirecting) return;
	_redirecting = true;
	fetch('/api/v1/auth/logout', { method: 'POST' });
	authLogout();
	try {
		await goto(resolve('/login'));
	} finally {
		_redirecting = false;
	}
}

function withAuth(opts = {}) {
	const token = getToken();
	if (!token) return opts;
	const headers = { ...opts.headers };
	headers['Authorization'] = `Bearer ${token}`;
	return { ...opts, headers };
}

async function request(path, opts = {}) {
	try {
		const res = await fetch(path, withAuth(opts));
		if (res.status === 401) {
			handleUnauthorized();
			return null;
		}
		if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
		if (res.status === 204 || res.headers.get('content-length') === '0') return null;
		return await res.json();
	} catch (err) {
		console.error(`API ${path}:`, err);
		return null;
	}
}

async function requestRaw(path, opts = {}) {
	try {
		const res = await fetch(path, withAuth(opts));
		let data = null;
		const contentType = res.headers.get('content-type') || '';
		if (contentType.includes('application/json') && res.status !== 204) {
			try {
				data = await res.json();
			} catch {
				data = null;
			}
		}
		return { ok: res.ok, status: res.status, data };
	} catch (err) {
		console.error(`API ${path}:`, err);
		return { ok: false, status: 0, data: null };
	}
}

async function requestStrict(path, opts = {}) {
	const res = await fetch(path, withAuth(opts));
	if (res.status === 401) {
		handleUnauthorized();
		throw new Error('Unauthorized');
	}
	if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
	if (res.status === 204 || res.headers.get('content-length') === '0') return null;
	return await res.json();
}

const asList = (data) =>
	Array.isArray(data) ? data : Array.isArray(data?.results) ? data.results : [];

export const api = {
	filterLanguages: () => request('/api/v1/filter-languages').then((data) => data ?? []),
	_supportedMimeTypesCache: null,
	supportedMimeTypes: () => {
		if (api._supportedMimeTypesCache) return Promise.resolve(api._supportedMimeTypesCache);
		return request('/api/v1/supported-mime-types').then((data) => {
			const result = data ?? [];
			api._supportedMimeTypesCache = result;
			return result;
		});
	},
	dashboard: () => request('/api/v1/dashboard'),

	documents: {
		list: (limit = 50, offset = 0, sortBy = 'created_at', sortOrder = 'desc') =>
			request(
				`/api/v1/documents?limit=${limit}&offset=${offset}&sort_by=${sortBy}&sort_order=${sortOrder}`
			).then((data) => data ?? []),

		get: (id) => request(`/api/v1/documents/${id}`),

		search: (q, limit = 50, offset = 0) =>
			request(
				`/api/v1/documents/search?q=${encodeURIComponent(q)}&limit=${limit}&offset=${offset}`
			).then((data) => data ?? []),

		searchStructured: (body) =>
			request('/api/v1/documents/search', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify(body)
			}).then((data) => data ?? { results: [], total: 0 }),

		update: (id, body) =>
			request(`/api/v1/documents/${id}`, {
				method: 'PUT',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify(body)
			}),

		delete: (id) => request(`/api/v1/documents/${id}`, { method: 'DELETE' }),

		reenrich: (id) => request(`/api/v1/documents/${id}/reenrich`, { method: 'POST' }),

		tags: {
			add: (id, tagId) =>
				request(`/api/v1/documents/${id}/tags`, {
					method: 'POST',
					headers: { 'Content-Type': 'application/json' },
					body: JSON.stringify({ tag_id: tagId })
				}),
			remove: (id, tagId) =>
				request(`/api/v1/documents/${id}/tags`, {
					method: 'DELETE',
					headers: { 'Content-Type': 'application/json' },
					body: JSON.stringify({ tag_id: tagId })
				})
		},

		batchDelete: (ids) =>
			requestRaw('/api/v1/documents/batch-delete', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ document_ids: ids })
			}),

		batchAssignTags: (ids, tagIds, mode = 'add') =>
			requestRaw('/api/v1/documents/batch-tags', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ document_ids: ids, tag_ids: tagIds, mode })
			}),

		batchSetDocumentType: (ids, documentTypeId) =>
			requestRaw('/api/v1/documents/batch-type', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ document_ids: ids, document_type_id: documentTypeId })
			}),

		downloadBatch: (ids) => {
			const form = document.createElement('form');
			form.method = 'POST';
			form.action = '/api/v1/documents/download';
			form.style.display = 'none';

			const input = document.createElement('input');
			input.type = 'hidden';
			input.name = 'document_ids';
			input.value = JSON.stringify(ids);
			form.appendChild(input);

			document.body.appendChild(form);
			form.submit();
			document.body.removeChild(form);
		},

		people: {
			add: (id, peopleId, peopleTypeId) =>
				request(`/api/v1/documents/${id}/people`, {
					method: 'POST',
					headers: { 'Content-Type': 'application/json' },
					body: JSON.stringify({ people_id: peopleId, people_type_id: peopleTypeId })
				}),
			remove: (id, peopleId, peopleTypeId) =>
				request(`/api/v1/documents/${id}/people`, {
					method: 'DELETE',
					headers: { 'Content-Type': 'application/json' },
					body: JSON.stringify({ people_id: peopleId, people_type_id: peopleTypeId })
				})
		}
	},

	tasks: {
		list: (batch, status, limit = 20, offset = 0) => {
			const params = new URLSearchParams();
			if (batch) params.set('batch', batch);
			if (status) params.set('status', status);
			params.set('limit', String(limit));
			params.set('offset', String(offset));
			const qs = params.toString();
			return request(`/api/v1/tasks${qs ? '?' + qs : ''}`).then((data) => data ?? []);
		},

		get: (taskId) => request(`/api/v1/tasks/${taskId}`),
		retry: (taskId) => request(`/api/v1/tasks/${taskId}/retry`, { method: 'POST' })
	},

	batches: {
		list: (limit = 20, offset = 0) =>
			request(`/api/v1/batches?limit=${limit}&offset=${offset}`).then(
				(data) => data?.batches ?? []
			),

		get: (batchId) => request(`/api/v1/batches/${batchId}`),
		retry: (batchId) => request(`/api/v1/batches/${batchId}/retry`, { method: 'POST' }),
		resume: (batchId) =>
			request(`/api/v1/batches/${batchId}/resume`, { method: 'POST' }).then((data) => data ?? {}),
		cancel: (batchId) => request(`/api/v1/batches/${batchId}/cancel`, { method: 'POST' })
	},

	autocomplete: {
		tags: (q, limit = 20) =>
			requestStrict(`/api/v1/tags?q=${encodeURIComponent(q)}&limit=${limit}`).then(asList),

		people: (q, limit = 20) =>
			requestStrict(`/api/v1/people?q=${encodeURIComponent(q)}&limit=${limit}`).then(asList),

		peopleTypes: () => requestStrict('/api/v1/people-types').then(asList),

		documentTypes: () => requestStrict('/api/v1/document-types').then(asList)
	},

	tags: {
		list: (q, limit = 50, offset = 0) => {
			const params = new URLSearchParams();
			if (q) params.set('q', q);
			params.set('limit', String(limit));
			params.set('offset', String(offset));
			return request(`/api/v1/tags?${params.toString()}`).then(
				(data) => data ?? { results: [], total: 0 }
			);
		},
		create: (name) =>
			requestRaw('/api/v1/tags', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ name })
			}),
		update: (id, name) =>
			requestRaw(`/api/v1/tags/${id}`, {
				method: 'PUT',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ name })
			}),
		delete: (id) => requestRaw(`/api/v1/tags/${id}`, { method: 'DELETE' })
	},

	people: {
		list: (q, limit = 50, offset = 0) => {
			const params = new URLSearchParams();
			if (q) params.set('q', q);
			params.set('limit', String(limit));
			params.set('offset', String(offset));
			return request(`/api/v1/people?${params.toString()}`);
		},
		create: (body) =>
			requestRaw('/api/v1/people', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify(body)
			}),
		update: (id, body) =>
			requestRaw(`/api/v1/people/${id}`, {
				method: 'PUT',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify(body)
			}),
		delete: (id) => requestRaw(`/api/v1/people/${id}`, { method: 'DELETE' })
	},

	peopleTypes: {
		list: (q, limit = 50, offset = 0) => {
			const params = new URLSearchParams();
			if (q) params.set('q', q);
			params.set('limit', String(limit));
			params.set('offset', String(offset));
			return request(`/api/v1/people-types?${params.toString()}`).then((data) => data ?? []);
		},
		create: (body) =>
			requestRaw('/api/v1/people-types', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify(body)
			}),
		update: (id, body) =>
			requestRaw(`/api/v1/people-types/${id}`, {
				method: 'PUT',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify(body)
			}),
		delete: (id) => requestRaw(`/api/v1/people-types/${id}`, { method: 'DELETE' })
	},

	documentTypes: {
		list: (q, limit = 50, offset = 0) => {
			const params = new URLSearchParams();
			if (q) params.set('q', q);
			params.set('limit', String(limit));
			params.set('offset', String(offset));
			return request(`/api/v1/document-types?${params.toString()}`).then((data) => data ?? []);
		},
		create: (body) =>
			requestRaw('/api/v1/document-types', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify(body)
			}),
		update: (id, body) =>
			requestRaw(`/api/v1/document-types/${id}`, {
				method: 'PUT',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify(body)
			}),
		delete: (id) => requestRaw(`/api/v1/document-types/${id}`, { method: 'DELETE' })
	},

	users: {
		list: (limit = 50, offset = 0) =>
			request(`/api/v1/users?limit=${limit}&offset=${offset}`).then(
				(data) => data ?? { users: [], total: 0 }
			),
		get: (id) => request(`/api/v1/users/${id}`),
		create: (body) =>
			requestRaw('/api/v1/users', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify(body)
			}),
		update: (id, body) =>
			requestRaw(`/api/v1/users/${id}`, {
				method: 'PUT',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify(body)
			}),
		delete: (id) => requestRaw(`/api/v1/users/${id}`, { method: 'DELETE' })
	},

	savedSearches: {
		list: () => request('/api/v1/saved-searches').then((data) => data ?? []),

		create: (name, filter) =>
			request('/api/v1/saved-searches', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ name, filter })
			}),

		delete: (id) => request(`/api/v1/saved-searches/${id}`, { method: 'DELETE' })
	},

	consume: {
		upload: async (files) => {
			const fd = new FormData();
			for (const f of files) fd.append('files', f);
			return requestRaw('/api/v1/consume/upload', { method: 'POST', body: fd });
		}
	},

	config: {
		bootstrap: () => request('/wizard/bootstrap'),
		get: () => request('/wizard/config'),
		update: (body) =>
			request('/wizard/config', {
				method: 'PUT',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify(body)
			}),
		status: () => request('/wizard/config/status'),
		llmModels: () =>
			request('/api/v1/llm/models').then((data) => data ?? { adapters: {}, providers: {} })
	},

	auth: {
		login: (username, password) =>
			requestRaw('/api/v1/auth/login', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ username, password })
			}),
		logout: () => fetch('/api/v1/auth/logout', { method: 'POST' })
	},

	me: {
		profile: () => request('/api/v1/me'),
		generateKey: () => requestRaw('/api/v1/me/api-key', { method: 'POST' }),
		revokeKey: () => requestRaw('/api/v1/me/api-key', { method: 'DELETE' }),
		rotateKey: () => requestRaw('/api/v1/me/api-key', { method: 'PUT' }),
		keyStatus: () => request('/api/v1/me/api-key')
	},

	orphaned: {
		list: () => request('/api/v1/orphaned').then((data) => data ?? []),

		scan: () => requestRaw('/api/v1/orphaned/scan', { method: 'POST' }),

		delete: (id) => requestRaw(`/api/v1/orphaned/${id}`, { method: 'DELETE' }),

		restore: (id) => requestRaw(`/api/v1/orphaned/${id}/restore`, { method: 'POST' }),

		moveToInbox: (id) => requestRaw(`/api/v1/orphaned/${id}/move-to-inbox`, { method: 'POST' }),

		deleteAll: () => requestRaw('/api/v1/orphaned/delete-all', { method: 'POST' }),

		moveAllToInbox: () => requestRaw('/api/v1/orphaned/move-to-inbox-all', { method: 'POST' })
	},

	trash: {
		list: (limit = 50, offset = 0) =>
			request(`/api/v1/trash?limit=${limit}&offset=${offset}`).then((data) => ({
				results: data?.documents ?? [],
				total: data?.total ?? 0
			})),

		restore: (documentId) => requestRaw(`/api/v1/trash/${documentId}/restore`, { method: 'POST' }),

		permanentDelete: (documentId) =>
			requestRaw(`/api/v1/trash/${documentId}`, { method: 'DELETE' }),

		batchRestore: (ids) =>
			requestRaw('/api/v1/trash/batch-restore', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ document_ids: ids })
			}),

		batchPermanentDelete: (ids) =>
			requestRaw('/api/v1/trash/batch-delete', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ document_ids: ids })
			}),

		purge: () => requestRaw('/api/v1/trash/purge', { method: 'POST' })
	},

	errored: {
		list: () => request('/api/v1/errored').then((data) => data ?? []),

		download: (subdir, file) => {
			const url = `/api/v1/errored/download?subdir=${encodeURIComponent(subdir)}&file=${encodeURIComponent(file)}`;
			window.open(url, '_blank');
		},

		delete: (subdir, file) =>
			requestRaw(
				`/api/v1/errored?subdir=${encodeURIComponent(subdir)}&file=${encodeURIComponent(file)}`,
				{ method: 'DELETE' }
			),

		deleteAll: () => requestRaw('/api/v1/errored/delete-all', { method: 'POST' })
	},

	logs: {
		get: (name, lines = 500, signal) => request(`/api/v1/logs/${name}?lines=${lines}`, { signal })
	}
};
