<script>
	import '../app.css';

	let { children } = $props();
</script>

<a
	href="#main-content"
	class="sr-only focus:not-sr-only focus:fixed focus:top-4 focus:left-4 focus:z-50 focus:rounded-lg focus:bg-clay-900 focus:px-4 focus:py-2 focus:text-parchment-200 focus:ring-2 focus:ring-gold-500 focus:outline-none"
>
	Skip to main content
</a>
<main id="main-content" class="flex min-h-screen items-center justify-center bg-clay-950 p-6">
	<div class="w-full max-w-xl rounded-xl border border-clay-800 bg-clay-900 p-8 shadow-xl">
		<div class="mb-8 text-center">
			<h1 class="text-2xl font-bold tracking-tight text-parchment-200">
				<span translate="no">edub-kushim</span> setup
			</h1>
			<p class="mt-2 text-sm text-parchment-500">Configure your document processing server</p>
		</div>
		{@render children()}
	</div>
</main>
