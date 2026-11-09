<script>
	import * as authStore from '#lib/stores/authStore.svelte.js';
	import TrashTable from './TrashTable.svelte';
</script>

{#if !authStore.authResolved()}
	<p class="text-parchment-500">Loading…</p>
{:else if authStore.authEnabled() && !authStore.isEditor()}
	<p class="text-parchment-500">You do not have permission to view this page.</p>
{:else}
	<TrashTable />
{/if}
