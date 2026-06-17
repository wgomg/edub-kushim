const BASE = '/wizard';

async function request(method, path, body) {
	const opts = {
		method,
		headers: { 'Content-Type': 'application/json' }
	};
	if (body !== undefined) {
		opts.body = JSON.stringify(body);
	}
	const res = await fetch(`${BASE}${path}`, opts);
	if (!res.ok) {
		const text = await res.text().catch(() => 'request failed');
		throw new Error(text);
	}
	return res.status === 204 ? null : res.json();
}

export const configApi = {
	get: () => request('GET', '/config'),
	update: (body) => request('PUT', '/config', body),
	status: () => request('GET', '/config/status')
};
