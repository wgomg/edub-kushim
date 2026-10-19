<script>
	import Card from './Card.svelte';
	import GroupHead from './GroupHead.svelte';
	import Switch from './Switch.svelte';
	import UnitField from './UnitField.svelte';

	let { cfg, mimeTypeOptions = [], showHead = true } = $props();

	function addWindow() {
		cfg.consumer.polling.windows = [
			...(cfg.consumer.polling.windows ?? []),
			{ start: '', end: '' }
		];
	}

	function removeWindow(index) {
		cfg.consumer.polling.windows = cfg.consumer.polling.windows.filter((_, i) => i !== index);
	}
</script>

<section id="intake" class="group scroll-mt-6 pb-12">
	{#if showHead}
		<GroupHead
			eyebrow="Stage 1 · Pipeline"
			title="Intake"
			description="How documents get in — uploads, the watched inbox, and what the inbox accepts."
		/>
	{/if}

	<div class="space-y-4">
		<Card
			title="Uploads & server"
			name="uploads server limits host port"
			description="Where the API listens and the limits it enforces."
		>
			<div class="grid gap-4 sm:grid-cols-2">
				<UnitField id="server-host" label="Listen host" mono bind:value={cfg.server.host} />
				<UnitField
					id="server-port"
					label="Listen port"
					type="number"
					inputmode="numeric"
					min="1"
					max="65535"
					mono
					bind:value={cfg.server.port}
				/>
				<UnitField
					id="server-max-upload"
					label="Max upload size"
					unit="MB"
					type="number"
					inputmode="numeric"
					min="1"
					bind:value={cfg.server.max_upload_size}
				/>
				<UnitField
					id="server-max-download-files"
					label="Max download files"
					unit="files"
					type="number"
					inputmode="numeric"
					min="1"
					bind:value={cfg.server.max_download_files}
				/>
				<UnitField
					id="server-max-download-size"
					label="Max download size"
					unit="MB"
					type="number"
					inputmode="numeric"
					min="0"
					bind:value={cfg.server.max_download_size_mb}
				/>
				<UnitField
					id="max-concurrent-batches"
					label="Max concurrent batches"
					unit="batches"
					type="number"
					inputmode="numeric"
					min="1"
					bind:value={cfg.server.max_concurrent_batches}
				/>
				<UnitField
					id="server-max-batch-delete"
					label="Max batch delete"
					unit="docs"
					type="number"
					inputmode="numeric"
					min="1"
					wide
					bind:value={cfg.server.max_batch_delete}
				/>
			</div>
		</Card>

		<Card
			title="Watched inbox"
			name="polling watched inbox folder scan interval"
			description="Pick up files dropped into the consumption directory."
		>
			<div class="grid gap-4 sm:grid-cols-2">
				<div class="sm:col-span-2">
					<Switch
						id="polling-enabled"
						bind:checked={cfg.consumer.polling.enabled}
						label="Watch the inbox"
						description="Scan the consumption directory on a schedule."
					/>
				</div>
				<UnitField
					id="polling-interval"
					label="Scan interval"
					unit="min"
					type="number"
					inputmode="numeric"
					min="1"
					wide
					bind:value={cfg.consumer.polling.interval}
				/>
				<div class="field advanced-only sm:col-span-2">
					<span class="text-[0.8125rem] font-medium text-parchment-200"
						>Active windows (optional)</span
					>
					<div class="space-y-2.5">
						{#each cfg.consumer.polling.windows as w, i (i)}
							<div class="flex items-center gap-2">
								<input
									type="text"
									name="polling-window-start-{i}"
									bind:value={w.start}
									aria-label="Start time"
									pattern="([01][0-9]|2[0-3]):[0-5][0-9]"
									placeholder="HH:MM…"
									minlength="5"
									maxlength="5"
									class="w-24 rounded-lg border border-clay-800 bg-clay-950 px-2.5 py-[0.45rem] font-mono text-[0.8125rem] text-parchment-200 placeholder-parchment-500 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
								/>
								<span class="text-[0.8125rem] text-parchment-500">to</span>
								<input
									type="text"
									name="polling-window-end-{i}"
									bind:value={w.end}
									aria-label="End time"
									pattern="([01][0-9]|2[0-3]):[0-5][0-9]|24:00"
									placeholder="HH:MM…"
									minlength="5"
									maxlength="5"
									class="w-24 rounded-lg border border-clay-800 bg-clay-950 px-2.5 py-[0.45rem] font-mono text-[0.8125rem] text-parchment-200 placeholder-parchment-500 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
								/>
								<button
									type="button"
									onclick={() => removeWindow(i)}
									class="rounded-lg border border-clay-800 px-2.5 py-[0.45rem] text-[0.8125rem] font-medium text-parchment-400 hover:bg-clay-800 hover:text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
								>
									Remove
								</button>
							</div>
						{/each}
					</div>
					<button
						type="button"
						onclick={addWindow}
						class="mt-2 self-start text-[0.8125rem] font-medium text-gold-500 hover:text-gold-600 hover:underline focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					>
						+ Add window
					</button>
				</div>
			</div>
		</Card>

		<Card
			title="Accepted file types"
			name="file types accepted mime pdf docx odt tiff jpeg png"
			description="Only checked types are ingested."
		>
			<div class="flex flex-wrap gap-x-5 gap-y-2.5">
				{#each mimeTypeOptions as opt (opt.mime_type)}
					<label
						class="flex items-center gap-2 text-sm {opt.checked
							? 'text-parchment-300'
							: 'text-parchment-500'}"
					>
						<input
							type="checkbox"
							bind:checked={opt.checked}
							disabled={opt.required}
							class="size-4 rounded border-clay-800 bg-clay-950 accent-gold-500 disabled:opacity-50"
						/>
						<span class="font-mono">{opt.label}</span>
						{#if opt.required}
							<span
								class="font-mono text-[0.625rem] font-semibold tracking-[0.05em] text-parchment-500"
								>required</span
							>
						{/if}
					</label>
				{/each}
			</div>
		</Card>

		<Card
			title="Office converter"
			name="office converter libreoffice docx odt"
			description="Converts DOCX and ODT files to PDF via LibreOffice before text extraction."
		>
			<div class="grid gap-4 sm:grid-cols-2">
				<div class="sm:col-span-2">
					<Switch
						id="converter-enabled"
						bind:checked={cfg.consumer.converter.enabled}
						label="Convert office documents"
					/>
				</div>
				<UnitField
					id="converter-binary"
					label="Binary path"
					mono
					bind:value={cfg.consumer.converter.binary}
				/>
				<UnitField
					id="converter-timeout"
					label="Timeout"
					unit="s"
					type="number"
					inputmode="numeric"
					min="1"
					bind:value={cfg.consumer.converter.timeout}
				/>
			</div>
		</Card>
	</div>
</section>
