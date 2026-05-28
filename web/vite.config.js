import tailwindcss from '@tailwindcss/vite';
import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vite';

export default defineConfig({
	plugins: [tailwindcss(), sveltekit()],
	server: {
		proxy: {
			'/api': `http://localhost:${process.env.API_PORT || '3000'}`,
			'/health': `http://localhost:${process.env.API_PORT || '3000'}`
		}
	}
});
