async function request(path) {
	try {
		const res = await fetch(path);
		if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
		return await res.json();
	} catch (err) {
		console.error(`API ${path}:`, err);
		return null;
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
			).then((data) => data ?? [])
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

		get: (taskId) => request(`/api/v1/tasks/${taskId}`)
	},

	batches: {
		list: (limit = 20, offset = 0) =>
			request(`/api/v1/batches?limit=${limit}&offset=${offset}`).then(
				(data) => data?.batches ?? []
			),

		get: (batchId) => request(`/api/v1/batches/${batchId}`)
	},

	summary: {
		get: () => request('/api/v1/summary').then((data) => data ?? null)
	}
};
