import { cpSync, rmSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

// the pdf.js asset directories are regenerated from node_modules on every install and build so that the
// decoders copied into static/pdfjs always match the installed pdfjs-dist version instead of drifting
// from committed copies
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const source = path.join(root, 'node_modules', 'pdfjs-dist');
const target = path.join(root, 'static', 'pdfjs');

rmSync(target, { recursive: true, force: true });
for (const dir of ['wasm', 'cmaps', 'standard_fonts', 'iccs']) {
	cpSync(path.join(source, dir), path.join(target, dir), { recursive: true });
}
