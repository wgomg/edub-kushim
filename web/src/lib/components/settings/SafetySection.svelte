<script>
	import { hintsForEngine } from '$lib/tools.js';
	import Card from './Card.svelte';
	import GroupHead from './GroupHead.svelte';
	import Note from './Note.svelte';
	import Switch from './Switch.svelte';
	import UnitField from './UnitField.svelte';

	let { cfg, toolStatus = [] } = $props();

	let rsyncTool = $derived(toolStatus.find((t) => t.engine === 'rsync' && !t.available));

	function rsyncHints() {
		const frontend = hintsForEngine('rsync');
		const source = Object.keys(frontend).length > 0 ? frontend : (rsyncTool?.install_hints ?? {});
		return Object.entries(source);
	}

	function addSchedule() {
		if (!cfg.backup.schedules) cfg.backup.schedules = [];
		cfg.backup.schedules = [
			...cfg.backup.schedules,
			{ mode: 'full', interval: 1, time: '02:00', keep: 7, path: '' }
		];
	}

	function removeSchedule(index) {
		cfg.backup.schedules = cfg.backup.schedules.filter((_, i) => i !== index);
	}
</script>

<section id="safety" class="group scroll-mt-6 pb-12">
	<GroupHead
		eyebrow="Operations"
		variant="op"
		title="Safety"
		description="Copies of the archive — scheduled dumps and the mirror."
	/>

	<div class="space-y-4">
		<Card
			title="Backups"
			name="backup schedules dump full database documents"
			description="Scheduled dumps of the database and documents."
		>
			<div class="grid gap-4 sm:grid-cols-2">
				<div class="sm:col-span-2">
					<Switch
						id="backup-enabled"
						bind:checked={cfg.backup.enabled}
						label="Run scheduled backups"
					/>
				</div>
				<UnitField
					id="backup-path"
					label="Fallback output directory"
					mono
					wide
					bind:value={cfg.backup.path}
				/>
				<div class="field sm:col-span-2">
					<span class="text-[0.8125rem] font-medium text-parchment-200">Schedules</span>
					<div class="space-y-2.5">
						{#each cfg.backup.schedules as s, i (i)}
							<div class="rounded-[0.625rem] border border-clay-800 bg-clay-950 p-3.5">
								<div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-6">
									<div class="field">
										<label
											for="backup-schedule-{i}-mode"
											class="text-xs font-medium text-parchment-200">Mode</label
										>
										<select
											id="backup-schedule-{i}-mode"
											name="backup-schedule-{i}-mode"
											bind:value={s.mode}
											class="w-full rounded-lg border border-clay-800 bg-clay-950 px-2.5 py-[0.45rem] text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
										>
											<option value="full">Full (database + documents)</option>
											<option value="database">Database only</option>
											<option value="documents">Documents only</option>
										</select>
									</div>
									<div class="field">
										<label
											for="backup-schedule-{i}-interval"
											class="text-xs font-medium text-parchment-200">Interval (days)</label
										>
										<input
											id="backup-schedule-{i}-interval"
											name="backup-schedule-{i}-interval"
											type="number"
											inputmode="numeric"
											min="0.1"
											step="0.1"
											autocomplete="off"
											bind:value={s.interval}
											class="w-full rounded-lg border border-clay-800 bg-clay-950 px-2.5 py-[0.45rem] text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
										/>
									</div>
									<div class="field">
										<label
											for="backup-schedule-{i}-time"
											class="text-xs font-medium text-parchment-200">Preferred time (HH:MM)</label
										>
										<input
											id="backup-schedule-{i}-time"
											name="backup-schedule-{i}-time"
											type="text"
											bind:value={s.time}
											pattern="([01][0-9]|2[0-3]):[0-5][0-9]"
											placeholder="HH:MM…"
											autocomplete="off"
											class="w-full rounded-lg border border-clay-800 bg-clay-950 px-2.5 py-[0.45rem] font-mono text-[0.8125rem] text-parchment-200 placeholder-parchment-500 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
										/>
									</div>
									<div class="field">
										<label
											for="backup-schedule-{i}-keep"
											class="text-xs font-medium text-parchment-200">Keep (0 = unlimited)</label
										>
										<input
											id="backup-schedule-{i}-keep"
											name="backup-schedule-{i}-keep"
											type="number"
											inputmode="numeric"
											min="0"
											autocomplete="off"
											bind:value={s.keep}
											class="w-full rounded-lg border border-clay-800 bg-clay-950 px-2.5 py-[0.45rem] text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
										/>
									</div>
									<div class="field sm:col-span-2 lg:col-span-2">
										<label
											for="backup-schedule-{i}-path"
											class="text-xs font-medium text-parchment-200"
											>Output directory (optional)</label
										>
										<input
											id="backup-schedule-{i}-path"
											name="backup-schedule-{i}-path"
											type="text"
											bind:value={s.path}
											placeholder="Fallback directory…"
											autocomplete="off"
											class="w-full rounded-lg border border-clay-800 bg-clay-950 px-2.5 py-[0.45rem] font-mono text-[0.8125rem] text-parchment-200 placeholder-parchment-500 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
										/>
									</div>
								</div>
								<button
									type="button"
									onclick={() => removeSchedule(i)}
									class="mt-3 rounded-lg border border-clay-800 px-2.5 py-1 text-[0.8125rem] font-medium text-parchment-400 hover:bg-clay-800 hover:text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
								>
									Remove
								</button>
							</div>
						{/each}
					</div>
					<button
						type="button"
						onclick={addSchedule}
						class="mt-2 self-start text-[0.8125rem] font-medium text-gold-500 hover:text-gold-600 hover:underline focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					>
						+ Add schedule
					</button>
				</div>
			</div>
			<div class="mt-4">
				<Note variant="info">
					Changing the interval or preferred time takes effect at the closest upcoming occurrence of
					the new timing. If that time has already passed since the last run, the task runs once
					immediately and the new cadence counts from that run.
				</Note>
			</div>
		</Card>

		<Card
			title="Mirror"
			name="mirror rsync nas remote copy"
			description="Keeps a second copy of storage in sync with rsync."
		>
			{#if rsyncTool}
				<div class="mb-4">
					<Note variant="danger" title="“rsync” not installed (required for the mirror)">
						Mirror runs will fail until rsync is installed.
						{#each rsyncHints() as [system, cmd] (system)}
							<pre class="mt-1 overflow-x-auto text-xs text-parchment-300">{system}: {cmd}</pre>
						{/each}
					</Note>
				</div>
			{/if}
			<div class="grid gap-4 sm:grid-cols-2">
				<div class="sm:col-span-2">
					<Switch id="mirror-enabled" bind:checked={cfg.mirror.enabled} label="Mirror storage" />
				</div>
				<UnitField
					id="mirror-path"
					label="Destination"
					mono
					wide
					placeholder="/mnt/nas/documents or user@host:/path"
					spellcheck="false"
					hint="Local path or rsync remote target ([user@]host:path; ssh key setup is your responsibility). Files deleted from storage are removed here too (--delete)."
					bind:value={cfg.mirror.path}
				/>
				<UnitField
					id="mirror-interval"
					label="Interval"
					unit="days"
					type="number"
					inputmode="numeric"
					min="0.1"
					step="0.1"
					bind:value={cfg.mirror.interval}
				/>
				<UnitField
					id="mirror-time"
					label="Preferred time"
					mono
					placeholder="02:00"
					pattern="([01][0-9]|2[0-3]):[0-5][0-9]"
					bind:value={cfg.mirror.time}
				/>
			</div>
			<div class="mt-4">
				<Note variant="info">
					Changing the interval or preferred time takes effect at the closest upcoming occurrence of
					the new timing. If that time has already passed since the last run, the task runs once
					immediately and the new cadence counts from that run.
				</Note>
			</div>
		</Card>
	</div>
</section>
