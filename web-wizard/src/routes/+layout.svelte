<script>
	import '../app.css';

	let { children } = $props();
</script>

<div class="flex min-h-screen items-center justify-center bg-clay-950 p-6">
	<div class="w-full max-w-xl rounded-xl border border-clay-800 bg-clay-900 p-8 shadow-xl">
		<div class="mb-8 text-center">
			<h1 class="text-2xl font-bold tracking-tight text-parchment-200">edub-kushim setup</h1>
			<p class="mt-2 text-sm text-parchment-500">Configure your document processing server</p>
		</div>
		{@render children()}
	</div>
</div>
