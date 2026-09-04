import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';
import { extname, join } from 'node:path';

const root = new URL('../build/', import.meta.url).pathname;
const port = Number(process.env.PORT || 4173);

const MIME = {
	'.html': 'text/html; charset=utf-8',
	'.js': 'text/javascript',
	'.mjs': 'text/javascript',
	'.css': 'text/css',
	'.json': 'application/json',
	'.svg': 'image/svg+xml',
	'.png': 'image/png',
	'.ico': 'image/x-icon',
	'.txt': 'text/plain; charset=utf-8',
	'.woff2': 'font/woff2'
};

const server = createServer(async (req, res) => {
	const pathname = decodeURIComponent(new URL(req.url, 'http://localhost').pathname);
	const file = join(root, pathname);
	if (!file.startsWith(root)) {
		res.writeHead(403).end();
		return;
	}
	try {
		const body = await readFile(file);
		res.writeHead(200, { 'Content-Type': MIME[extname(file)] || 'application/octet-stream' });
		res.end(body);
	} catch {
		// SPA fallback: unknown routes serve index.html, mirroring adapter-static's fallback
		try {
			const body = await readFile(join(root, 'index.html'));
			res.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' });
			res.end(body);
		} catch {
			res.writeHead(500).end();
		}
	}
});

server.listen(port, '::', () => {
	console.log(`Static preview on http://localhost:${port}`);
});
