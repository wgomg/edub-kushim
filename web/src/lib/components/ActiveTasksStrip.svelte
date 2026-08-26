<script>
	import { resolve } from '$app/paths';
	import { formatRelative } from '$lib/utils/html.js';
	import { statusChipClasses } from '$lib/utils/statusChip.js';

	let { tasks = [], total = 0 } = $props();
</script>

<section>
	<div class="mb-3 flex items-center justify-between">
		<h2 class="text-lg font-semibold text-parchment-200">
			Active Tasks{#if total > 0}
				({total}){/if}
		</h2>
		{#if total > tasks.length}
			<a
				href={resolve('/tasks?status=active')}
				class="text-sm text-gold-500 transition-colors hover:text-gold-400 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
			>
				View all {total} →
			</a>
		{/if}
	</div>

	{#if tasks.length === 0}
		<div class="rounded-lg border border-clay-800 bg-clay-900 p-6 text-center text-parchment-500">
			No active tasks
		</div>
	{:else}
		<div class="relative">
			<div
				class="flex snap-x snap-mandatory gap-4 overflow-x-auto pb-2"
				role="region"
				aria-label="Active tasks"
				aria-live="polite"
			>
				{#each tasks as task (task.task_id)}
					<a
						href={resolve(`/tasks/${task.task_id}`)}
						class="w-64 shrink-0 snap-start rounded-lg border border-clay-800 bg-clay-900 p-4 transition-colors hover:bg-clay-800 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					>
						<p class="truncate text-sm font-medium text-parchment-200" title={task.label}>
							{task.label}
						</p>
						<div class="mt-2 flex items-center justify-between gap-2">
							<span
								class="inline-block rounded-full px-2.5 py-0.5 text-xs font-medium {statusChipClasses(
									task.status
								)}"
							>
								{task.status}
							</span>
							<span class="shrink-0 text-xs text-parchment-500">
								{task.status === 'processing'
									? `running ${formatRelative(task.started_at)}`
									: `queued ${formatRelative(task.created_at)}`}
							</span>
						</div>
					</a>
				{/each}
			</div>
			<div
				class="pointer-events-none absolute inset-y-0 right-0 w-12 bg-linear-to-l from-clay-900 to-transparent"
			></div>
		</div>
	{/if}
</section>
