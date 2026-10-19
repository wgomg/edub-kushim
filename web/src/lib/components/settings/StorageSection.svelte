<script>
	import Card from './Card.svelte';
	import GroupHead from './GroupHead.svelte';
	import UnitField from './UnitField.svelte';

	let { cfg, showHead = true } = $props();
</script>

<section id="storage" class="group scroll-mt-6 pb-12">
	{#if showHead}
		<GroupHead
			eyebrow="Stage 4 · Pipeline"
			title="Storage"
			description="Where everything ends up — directories, database, trash."
		/>
	{/if}

	<div class="space-y-4">
		<Card
			title="Directories"
			name="directories consumption storage migration copy move"
			description="The inbox documents arrive in and the archive they move to."
		>
			<div class="grid gap-4 sm:grid-cols-2">
				<UnitField
					id="consumption-dir"
					label="Consumption directory (inbox)"
					mono
					bind:value={cfg.storage.consumption_dir}
				/>
				<UnitField
					id="storage-dir"
					label="Storage directory"
					mono
					bind:value={cfg.storage.storage_dir}
				/>
				<div class="field sm:col-span-2">
					<label for="migration-mode" class="text-[0.8125rem] font-medium text-parchment-200"
						>Migration mode</label
					>
					<select
						id="migration-mode"
						name="migration-mode"
						bind:value={cfg.storage.migration_mode}
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-2.5 py-[0.45rem] text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					>
						<option value="copy">Copy, then delete — safer, needs extra disk space</option>
						<option value="move">Move directly — faster, risk of data loss if interrupted</option>
					</select>
					{#if cfg.storage.migration_mode === 'move'}
						<p class="text-xs text-terracotta-500">
							Files are renamed into place. If the process dies mid-migration, files can be left
							behind in the old location. Copy mode is recommended.
						</p>
					{:else}
						<p class="text-xs text-parchment-500">
							Used when the storage or consumption directory changes.
						</p>
					{/if}
				</div>
			</div>
		</Card>

		<Card
			title="Trash"
			name="trash soft delete retention purge"
			description="Deleted documents rest here before permanent removal."
		>
			<div class="grid gap-4 sm:grid-cols-2">
				<UnitField
					id="trash-retention-days"
					label="Retention period"
					unit="days"
					type="number"
					inputmode="numeric"
					min="1"
					hint="Soft-deleted documents are permanently purged after this many days."
					bind:value={cfg.storage.trash.retention_days}
				/>
			</div>
		</Card>

		<Card
			title="Database"
			name="database postgres host port sslmode runtime container psql"
			description="PostgreSQL connection and restore behavior."
		>
			<div class="grid gap-4 sm:grid-cols-2">
				<UnitField id="db-host" label="Host" mono bind:value={cfg.database.host} />
				<UnitField
					id="db-port"
					label="Port"
					type="number"
					inputmode="numeric"
					min="1"
					max="65535"
					mono
					bind:value={cfg.database.port}
				/>
				<UnitField id="db-user" label="User" mono bind:value={cfg.database.user} />
				<UnitField id="db-name" label="Database name" mono bind:value={cfg.database.database} />
				<div class="field">
					<label for="db-sslmode" class="text-[0.8125rem] font-medium text-parchment-200"
						>SSL mode</label
					>
					<select
						id="db-sslmode"
						name="db-sslmode"
						bind:value={cfg.database.sslmode}
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-2.5 py-[0.45rem] text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					>
						<option value="disable">disable</option>
						<option value="allow">allow</option>
						<option value="prefer">prefer</option>
						<option value="require">require</option>
						<option value="verify-ca">verify-ca</option>
						<option value="verify-full">verify-full</option>
					</select>
				</div>
				<div class="field">
					<label for="db-runtime" class="text-[0.8125rem] font-medium text-parchment-200"
						>Runtime</label
					>
					<select
						id="db-runtime"
						name="db-runtime"
						bind:value={cfg.database.runtime}
						class="w-full rounded-lg border border-clay-800 bg-clay-950 px-2.5 py-[0.45rem] text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					>
						<option value="host">host</option>
						<option value="docker">docker</option>
						<option value="podman">podman</option>
						<option value="remote">remote</option>
					</select>
					<p class="text-xs text-parchment-500">
						Where restores run psql: on this machine (host), inside a container (docker/podman), or
						not at all (remote — restore must be done manually on the remote host).
					</p>
				</div>
				{#if cfg.database.runtime === 'docker' || cfg.database.runtime === 'podman'}
					<UnitField
						id="db-container"
						label="Container name"
						mono
						placeholder="edub-pg"
						hint="Required for docker/podman: the container running PostgreSQL, where psql is executed for restores."
						wide
						advancedOnly
						bind:value={cfg.database.container}
					/>
				{/if}
			</div>
		</Card>
	</div>
</section>
