import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { render } from '@testing-library/svelte';
import Icon from './Icon.svelte';

function svelteFiles(dir) {
	return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
		const path = join(dir, entry.name);
		if (entry.isDirectory()) return svelteFiles(path);
		return entry.name.endsWith('.svelte') ? [path] : [];
	});
}

const srcRoot = join(process.cwd(), 'src');
const usedNames = new Set();

for (const file of svelteFiles(srcRoot)) {
	const source = readFileSync(file, 'utf8');

	for (const match of source.matchAll(/<Icon\s+name="([a-z0-9-]+)"/g)) usedNames.add(match[1]);

	for (const match of source.matchAll(/<Icon\s+name=\{([^}]+)\}/g)) {
		for (const quoted of match[1].matchAll(/'([a-z0-9-]+)'/g)) usedNames.add(quoted[1]);
	}

	if (file.endsWith('TagReviewHelp.svelte')) {
		for (const match of source.matchAll(/\bicon:\s*'([a-z0-9-]+)'/g)) usedNames.add(match[1]);
	}
}

describe('Icon catalogue', () => {
	it('finds the icon names the app renders', () => {
		expect(usedNames.size).toBeGreaterThan(20);
	});

	it.each([...usedNames].sort())('renders markup for "%s"', (name) => {
		const { container } = render(Icon, { name });
		expect(container.querySelector('svg')?.children.length ?? 0).toBeGreaterThan(0);
	});
});
