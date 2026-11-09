<script>
	import { effectiveUrl } from '#lib/list/listQuery.svelte.js';
	import BatchesTable from './BatchesTable.svelte';
	import TasksTable from './TasksTable.svelte';

	const currentBatch = $derived(effectiveUrl().searchParams.get('batch') ?? '');
	const currentStatus = $derived(effectiveUrl().searchParams.get('status') ?? '');
</script>

{#if currentBatch || currentStatus}
	<TasksTable />
{:else}
	<BatchesTable />
{/if}
