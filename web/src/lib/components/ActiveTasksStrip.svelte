<script>
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { formatRelative } from '$lib/utils/html.js';
	import { statusBandClasses } from '$lib/utils/statusChip.js';

	let { tasks = [], count = 0 } = $props();

	function openTask(taskId) {
		goto(resolve(`/tasks/${taskId}`));
	}
</script>

<section>
	<div class="mb-3 flex items-center justify-between">
		<h2 class="text-lg font-semibold text-parchment-200">
			Active Tasks{#if count > 0}
				({count}){/if}
		</h2>
		{#if count > tasks.length}
			<a
				href={resolve('/tasks?status=active')}
				class="text-sm text-gold-500 transition-colors hover:text-gold-400 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
			>
				View all {count} →
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
				class="flex touch-manipulation snap-x snap-mandatory gap-4 overflow-x-auto overscroll-x-contain pb-2"
				role="region"
				aria-label="Active tasks"
				aria-live="polite"
			>
				{#each tasks as task (task.task_id)}
					<div
						role="link"
						tabindex="0"
						class="w-80 shrink-0 cursor-pointer snap-start overflow-hidden rounded-[0.625rem] border bg-clay-900 transition-colors hover:bg-clay-800 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none {task.status ===
						'processing'
							? 'border-lapis-400/50 shadow-[0_0_0_1px_rgba(98,145,201,0.12),0_0_14px_rgba(59,94,138,0.22)] hover:border-lapis-300/65 hover:shadow-[0_0_0_1px_rgba(125,168,220,0.16),0_0_16px_rgba(59,94,138,0.3)]'
							: 'border-clay-800 hover:border-clay-700'}"
						onclick={(e) => {
							if (e.target.closest('a')) return;
							openTask(task.task_id);
						}}
						onkeydown={(e) => {
							if (e.target.closest('a, button, input')) return;
							if (e.key === 'Enter' || e.key === ' ') {
								e.preventDefault();
								openTask(task.task_id);
							}
						}}
					>
						<div
							class="flex items-center gap-2 border-b px-3.5 py-2 text-[0.6875rem] font-semibold tracking-wider uppercase {statusBandClasses(
								task.status
							)}"
						>
							<span
								class="size-1.5 shrink-0 rounded-full bg-current {task.status === 'processing'
									? 'animate-pulse motion-reduce:animate-none'
									: ''}"
							></span>
							<span>{task.status}</span>
							<span class="ml-auto font-normal tracking-normal normal-case tabular-nums opacity-75">
								{formatRelative(task.status === 'processing' ? task.started_at : task.created_at)}
							</span>
						</div>
						<div class="px-3.5 pt-3 pb-3.5">
							{#if task.payload_doc_id}
								<a
									href={resolve(`/documents/${task.payload_doc_id}`)}
									class="flex items-center gap-1.5 text-sm font-medium text-parchment-100 hover:text-lapis-300 hover:underline hover:decoration-lapis-300/40 hover:underline-offset-[3px]"
									title={task.payload_doc_id}
								>
									<svg
										class="size-3 shrink-0 text-lapis-400"
										viewBox="0 0 16 16"
										fill="none"
										stroke="currentColor"
										stroke-width="1.3"
										stroke-linecap="round"
										stroke-linejoin="round"
										aria-hidden="true"
									>
										<path d="M6.5 3.5h-3a1 1 0 0 0-1 1v8a1 1 0 0 0 1 1h8a1 1 0 0 0 1-1v-3" />
										<path d="M9.5 2.5h4v4" />
										<path d="M13.5 2.5 7.5 8.5" />
									</svg>
									<span class="min-w-0 truncate">{task.label}</span>
								</a>
							{:else}
								<span class="block truncate text-sm font-medium text-parchment-100"
									>{task.label}</span
								>
							{/if}
							{#if task.progress}
								<p class="mt-1.5 flex items-baseline gap-3 text-xs text-parchment-400">
									<span class="min-w-0 truncate">
										<span class="font-medium text-parchment-200">{task.progress.step}</span>{task
											.progress.detail
											? ` — ${task.progress.detail}`
											: ''}
									</span>
									<span class="ml-auto shrink-0 text-parchment-500 tabular-nums">
										{formatRelative(task.progress.updated_at)} ago
									</span>
								</p>
							{/if}
						</div>
					</div>
				{/each}
			</div>
			<div
				class="pointer-events-none absolute inset-y-0 right-0 w-12 bg-linear-to-l from-clay-900 to-transparent"
			></div>
		</div>
	{/if}
</section>
