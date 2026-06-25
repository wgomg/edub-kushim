async function request(path, opts = {}) {
	try {
		const res = await fetch(path, opts);
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
		const res = await fetch(path, opts);
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

export const api = {
	health: () =>
		request('/health').then((data) => data ?? { status: 'unreachable', version: '-', time: '-' }),

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
			request(`/api/v1/batches/${batchId}/resume`, { method: 'POST' }).then((data) => data ?? {})
	},

	summary: {
		get: () => request('/api/v1/summary').then((data) => data ?? null)
	},

	autocomplete: {
		tags: (q, limit = 20) =>
			request(`/api/v1/tags?q=${encodeURIComponent(q)}&limit=${limit}`).then(
				(data) => (data && data.results) ?? []
			),

		people: (q, limit = 20) =>
			request(`/api/v1/people?q=${encodeURIComponent(q)}&limit=${limit}`).then(
				(data) => data ?? []
			),

		peopleTypes: () => request('/api/v1/people-types').then((data) => data ?? []),

		documentTypes: () => request('/api/v1/document-types').then((data) => data ?? [])
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
			return request(`/api/v1/people?${params.toString()}`).then((data) => data ?? []);
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
		get: () => request('/wizard/config'),
		update: (body) =>
			request('/wizard/config', {
				method: 'PUT',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify(body)
			}),
		status: () => request('/wizard/config/status')
	}
};
