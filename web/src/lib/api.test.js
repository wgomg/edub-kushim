import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { goto } from '$app/navigation';
import { api } from './api.js';
import { login, logout, isAuthenticated } from './stores/authStore.js';

vi.mock('$app/navigation', () => ({ goto: vi.fn() }));
vi.mock('$app/paths', () => ({ resolve: (p) => p }));

function jsonResponse({
	status = 200,
	ok = true,
	statusText = 'OK',
	body = null,
	contentType = 'application/json',
	contentLength = null
} = {}) {
	return {
		status,
		ok,
		statusText,
		headers: {
			get: (name) => {
				if (name.toLowerCase() === 'content-type') return contentType;
				if (name.toLowerCase() === 'content-length') return contentLength;
				return null;
			}
		},
		json: async () => body
	};
}

describe('api', () => {
	let fetchMock;

	beforeEach(() => {
		fetchMock = vi.fn();
		vi.stubGlobal('fetch', fetchMock);
		vi.spyOn(console, 'error').mockImplementation(() => {});
		logout();
		api._supportedMimeTypesCache = null;
		goto.mockClear();
	});

	afterEach(() => {
		vi.unstubAllGlobals();
		vi.restoreAllMocks();
	});

	it('injects the Bearer header when a token is set', async () => {
		login('tok-1', { username: 'admin', role: 'admin' });
		fetchMock.mockResolvedValue(jsonResponse({ body: [] }));
		await api.documents.list();
		const [url, opts] = fetchMock.mock.calls[0];
		expect(url).toBe('/api/v1/documents?limit=50&offset=0&sort_by=created_at&sort_order=desc');
		expect(opts.headers.Authorization).toBe('Bearer tok-1');
	});

	it('omits the Authorization header without a token', async () => {
		fetchMock.mockResolvedValue(jsonResponse({ body: [] }));
		await api.documents.list();
		expect(fetchMock.mock.calls[0][1].headers?.Authorization).toBeUndefined();
	});

	it('logs out and redirects on 401, returning null', async () => {
		login('tok-1', { username: 'admin', role: 'admin' });
		fetchMock.mockResolvedValue(jsonResponse({ status: 401, ok: false }));
		expect(await api.documents.get(1)).toBeNull();
		expect(isAuthenticated()).toBe(false);
		expect(goto).toHaveBeenCalledWith('/login');
	});

	it('returns null on non-ok responses', async () => {
		fetchMock.mockResolvedValue(jsonResponse({ status: 500, ok: false, statusText: 'Boom' }));
		expect(await api.documents.get(1)).toBeNull();
	});

	it('returns null for 204 and zero-length responses', async () => {
		fetchMock.mockResolvedValue(jsonResponse({ status: 204, ok: true }));
		expect(await api.documents.get(1)).toBeNull();

		fetchMock.mockResolvedValue(jsonResponse({ body: null, contentLength: '0' }));
		expect(await api.documents.get(1)).toBeNull();
	});

	it('returns null when fetch rejects', async () => {
		fetchMock.mockRejectedValue(new Error('network down'));
		expect(await api.documents.get(1)).toBeNull();
	});

	it('requestRaw exposes ok, status and parsed data', async () => {
		fetchMock.mockResolvedValue(jsonResponse({ body: { accepted: 2 } }));
		expect(await api.consume.upload([new File(['x'], 'a.pdf')])).toEqual({
			ok: true,
			status: 200,
			data: { accepted: 2 }
		});
	});

	it('requestRaw keeps data null for non-JSON payloads and network errors', async () => {
		fetchMock.mockResolvedValue(jsonResponse({ body: 'plain', contentType: 'text/plain' }));
		expect(await api.consume.upload([new File(['x'], 'a.pdf')])).toEqual({
			ok: true,
			status: 200,
			data: null
		});

		fetchMock.mockRejectedValue(new Error('network down'));
		expect(await api.consume.upload([new File(['x'], 'a.pdf')])).toEqual({
			ok: false,
			status: 0,
			data: null
		});
	});

	it('supportedMimeTypes caches the result across calls', async () => {
		fetchMock.mockResolvedValue(jsonResponse({ body: [{ extension: '.pdf' }] }));
		await api.supportedMimeTypes();
		await api.supportedMimeTypes();
		expect(fetchMock).toHaveBeenCalledTimes(1);
		expect(await api.supportedMimeTypes()).toEqual([{ extension: '.pdf' }]);
	});
});
