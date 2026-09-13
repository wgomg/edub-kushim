<script>
	import Card from './Card.svelte';
	import GroupHead from './GroupHead.svelte';
	import Switch from './Switch.svelte';
	import UnitField from './UnitField.svelte';

	let { cfg } = $props();
</script>

<section id="system" class="group scroll-mt-6 pb-12">
	<GroupHead
		eyebrow="Operations"
		variant="op"
		title="System"
		description="Logging for both binaries."
	/>

	<div class="space-y-4">
		<Card
			title="Logging"
			name="logging logs level rotation size compress lumberjack"
			description="Level and rotation of log files."
		>
			<div class="grid gap-4 sm:grid-cols-2">
				<div class="field">
					<label for="log-level" class="text-[0.8125rem] font-medium text-parchment-200"
						>Log level</label
					>
					<select
						id="log-level"
						name="log-level"
						bind:value={cfg.app.log_level}
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-2.5 py-[0.45rem] text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					>
						<option value="silent">silent</option>
						<option value="fatal">fatal</option>
						<option value="error">error</option>
						<option value="warn">warn</option>
						<option value="info">info</option>
						<option value="debug">debug</option>
					</select>
				</div>
				<UnitField
					id="log-max-size"
					label="Max size per file"
					unit="MB"
					type="number"
					inputmode="numeric"
					min="0"
					hint="0 = no rotation"
					bind:value={cfg.app.logging.max_size}
				/>
				<UnitField
					id="log-max-backups"
					label="Max backups to keep"
					unit="files"
					type="number"
					inputmode="numeric"
					min="0"
					bind:value={cfg.app.logging.max_backups}
				/>
				<UnitField
					id="log-max-age"
					label="Max age"
					unit="days"
					type="number"
					inputmode="numeric"
					min="0"
					hint="0 = no limit"
					bind:value={cfg.app.logging.max_age}
				/>
				<div class="sm:col-span-2">
					<Switch
						id="log-compress"
						bind:checked={cfg.app.logging.compress}
						label="Compress rotated logs"
					/>
				</div>
			</div>
		</Card>
	</div>
</section>
