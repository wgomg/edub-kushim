import tailwindcss from '@tailwindcss/vite';
import { sveltekit } from '@sveltejs/kit/vite';
import { svelteTesting } from '@testing-library/svelte/vite';
import { defineConfig } from 'vitest/config';

const stub = new URL('src/lib/stubs/empty.mjs', import.meta.url).pathname;

export default defineConfig({
	plugins: [tailwindcss(), sveltekit()],
	resolve: {
		alias: {
			child_process: stub,
			url: stub
		}
	},
	server: {
		proxy: {
			'/api': `http://localhost:${process.env.API_PORT || '3000'}`,
			'/health': `http://localhost:${process.env.API_PORT || '3000'}`
		}
	},
	test: {
		projects: [
			{
				extends: true,
				test: {
					name: 'unit',
					environment: 'node',
					include: ['src/lib/**/*.test.js'],
					exclude: [
						'src/lib/components/**',
						'src/lib/stores/authStore.test.js',
						'src/lib/api.test.js'
					]
				}
			},
			{
				extends: true,
				plugins: [svelteTesting()],
				test: {
					name: 'components',
					environment: 'jsdom',
					include: [
						'src/lib/components/**/*.test.js',
						'src/lib/stores/authStore.test.js',
						'src/lib/api.test.js'
					]
				}
			}
		]
	}
});
