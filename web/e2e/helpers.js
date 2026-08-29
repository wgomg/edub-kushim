export const TEST_USER = { username: 'admin', role: 'admin' };

export const DOCUMENTS = [
	{
		id: 1,
		title: 'Annual Report 2025',
		file_size: 1048576,
		created_at: '2026-01-15T10:00:00Z',
		tags: [{ name: 'report' }],
		people: [],
		language: 'eng',
		document_type_id: 1
	},
	{
		id: 2,
		title: 'Contract NDA',
		file_size: 2048,
		created_at: '2026-02-01T10:00:00Z',
		tags: [],
		people: [],
		language: 'eng',
		document_type_id: 1
	}
];

const DASHBOARD = {
	total_files: 2,
	total_batches: 1,
	total_size_gb: 0.001,
	waiting: 0,
	pending: 0,
	processing: 0,
	completed: 2,
	failed: 0,
	cancelled: 0,
	discarded: 0,
	running_tasks: [],
	recent_batches: [],
	analytics: null,
	processing_health: null,
	original_type_breakdown: [],
	storage_trend: [],
	avg_file_size_bytes: 0,
	total_pages: 0,
	total_words: 0
};

function json(route, data, status = 200) {
	return route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) });
}

export async function mockApi(page, overrides = {}) {
	const handlers = {
		'/api/v1/auth/login': (route) => json(route, { token: 'test-token', user: TEST_USER }),
		'/api/v1/auth/logout': (route) => json(route, {}),
		'/api/v1/me': (route) => json(route, TEST_USER),
		'/api/v1/dashboard': (route) => json(route, DASHBOARD),
		'/api/v1/documents/search': (route) =>
			json(route, { results: DOCUMENTS, total: DOCUMENTS.length }),
		'/api/v1/documents': (route) => json(route, DOCUMENTS),
		'/api/v1/documents/1': (route) => json(route, DOCUMENTS[0]),
		'/api/v1/tasks': (route) => json(route, []),
		'/api/v1/tags': (route) => json(route, { results: [], total: 0 }),
		'/api/v1/saved-searches': (route) => json(route, []),
		'/api/v1/people-types': (route) => json(route, []),
		'/api/v1/document-types': (route) => json(route, [{ id: 1, name: 'Invoice' }]),
		'/api/v1/filter-languages': (route) => json(route, ['eng', 'spa']),
		'/api/v1/supported-mime-types': (route) =>
			json(route, [{ extension: '.pdf' }, { extension: '.docx' }]),
		'/api/v1/consume/upload': (route) =>
			json(route, { accepted: 1, batch_id: 'batch-1', rejected: [] }, 202),
		'/wizard/bootstrap': (route) => json(route, { auth_enabled: true, missing_tools: [] }),
		'/health': (route) =>
			json(route, { status: 'ok', version: 'test', time: '2026-01-01T00:00:00Z' }),
		...overrides
	};

	await page.route(/\/api\/|\/wizard\/|\/health/, (route) => {
		const path = new URL(route.request().url()).pathname;
		const handler = handlers[path];
		if (handler) return handler(route);
		return route.fulfill({ status: 404, contentType: 'application/json', body: '{}' });
	});
}

export async function seedAuth(page) {
	await page.addInitScript((user) => {
		localStorage.setItem('token', 'test-token');
		localStorage.setItem('user', JSON.stringify(user));
	}, TEST_USER);
}
