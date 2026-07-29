import tailwindcss from '@tailwindcss/vite';
import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vite';

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
	}
});
