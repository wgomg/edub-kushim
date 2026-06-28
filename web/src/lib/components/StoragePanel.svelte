<script>
	import { formatSize, formatNumber } from '$lib/utils/html.js';

	let {
		mimeTypeBreakdown = [],
		storageTrend = [],
		avgFileSizeBytes = 0,
		totalPages = 0,
		totalWords = 0
	} = $props();

	const chartColors = ['#c9953a', '#3b5e8a', '#b84a3a', '#9a8c78', '#b07d2e', '#2e4a6e', '#5a3a2a'];

	const topTypes = $derived(() => {
		const sorted = [...mimeTypeBreakdown].sort((a, b) => b.total_bytes - a.total_bytes);
		if (sorted.length <= 7) return sorted;
		const top = sorted.slice(0, 7);
		const other = sorted.slice(7).reduce(
			(acc, item) => {
				acc.count += item.count;
				acc.total_bytes += item.total_bytes;
				return acc;
			},
			{ mime_type: 'other', count: 0, total_bytes: 0 }
		);
		return [...top, other];
	});

	const maxTypeBytes = $derived(() =>
		topTypes().length > 0 ? Math.max(...topTypes().map((t) => t.total_bytes)) : 1
	);

	const trendMax = $derived(() =>
		storageTrend.length > 0 ? Math.max(...storageTrend.map((t) => t.cumulative_bytes)) : 1
	);

	const svgWidth = 600;
	const svgHeight = 200;
	const padding = { top: 10, right: 10, bottom: 30, left: 60 };
	const chartW = svgWidth - padding.left - padding.right;
	const chartH = svgHeight - padding.top - padding.bottom;

	function trendPath(points) {
		if (points.length === 0) return '';
		const xScale = (i) => padding.left + (i / Math.max(points.length - 1, 1)) * chartW;
		const yScale = (v) => padding.top + chartH - (v / trendMax()) * chartH;
		const bottom = padding.top + chartH;

		if (points.length === 1) {
			const barHalfWidth = 12;
			const x = xScale(0);
			const y = yScale(points[0].cumulative_bytes);
			return `M ${x - barHalfWidth} ${y} L ${x - barHalfWidth} ${bottom} L ${x + barHalfWidth} ${bottom} L ${x + barHalfWidth} ${y} Z`;
		}

		let d = `M ${xScale(0)} ${yScale(points[0].cumulative_bytes)}`;
		for (let i = 1; i < points.length; i++) {
			d += ` L ${xScale(i)} ${yScale(points[i].cumulative_bytes)}`;
		}
		d += ` L ${xScale(points.length - 1)} ${bottom}`;
		d += ` L ${xScale(0)} ${bottom} Z`;
		return d;
	}

	function trendLine(points) {
		if (points.length === 0) return '';
		const xScale = (i) => padding.left + (i / Math.max(points.length - 1, 1)) * chartW;
		const yScale = (v) => padding.top + chartH - (v / trendMax()) * chartH;

		if (points.length === 1) {
			const x = xScale(0);
			const y = yScale(points[0].cumulative_bytes);
			return `M ${x - 4} ${y} a 4 4 0 1 1 8 0 a 4 4 0 1 1 -8 0`;
		}

		let d = `M ${xScale(0)} ${yScale(points[0].cumulative_bytes)}`;
		for (let i = 1; i < points.length; i++) {
			d += ` L ${xScale(i)} ${yScale(points[i].cumulative_bytes)}`;
		}
		return d;
	}

	const yTicks = $derived(() => {
		const max = trendMax();
		if (max === 0) return [0];
		const niceMax = Math.ceil(max / (1024 * 1024)) * 1024 * 1024;
		const step = niceMax / 4;
		const ticks = [];
		for (let i = 0; i <= 4; i++) {
			ticks.push(Math.round(i * step));
		}
		return ticks;
	});

	const xLabels = $derived(() => {
		if (storageTrend.length === 0) return [];
		const skip = storageTrend.length > 30 ? Math.ceil(storageTrend.length / 10) : 1;
		return storageTrend.map((p, i) => ({
			date: p.date,
			show: i % skip === 0 || i === storageTrend.length - 1,
			x: padding.left + (i / Math.max(storageTrend.length - 1, 1)) * chartW
		}));
	});

	function shortMime(mime) {
		if (mime === 'other') return 'other';
		const parts = mime.split('/');
		return parts[parts.length - 1];
	}
</script>

<div class="space-y-6">
	{#if mimeTypeBreakdown.length === 0 && storageTrend.length === 0}
		<div class="rounded-lg border border-clay-800 bg-clay-900 p-6 text-center text-parchment-500">
			No documents yet
		</div>
	{:else}
		<div class="grid grid-cols-1 gap-4 sm:grid-cols-3">
			<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
				<p class="text-sm text-parchment-500">Avg File Size</p>
				<p class="mt-1 text-lg font-semibold text-parchment-200">{formatSize(avgFileSizeBytes)}</p>
			</div>
			<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
				<p class="text-sm text-parchment-500">Total Pages</p>
				<p class="mt-1 text-lg font-semibold text-parchment-200">{formatNumber(totalPages)}</p>
			</div>
			<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
				<p class="text-sm text-parchment-500">Total Words</p>
				<p class="mt-1 text-lg font-semibold text-parchment-200">{formatNumber(totalWords)}</p>
			</div>
		</div>

		{#if topTypes().length > 0}
			<section>
				<h3 class="mb-3 text-base font-semibold text-parchment-200">MIME Type Breakdown</h3>
				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<div class="grid grid-cols-1 gap-6 lg:grid-cols-2">
						<div class="space-y-2">
							{#each topTypes() as item, i}
								<div class="flex items-center gap-3">
									<div
										class="h-4 w-4 shrink-0 rounded"
										style="background-color: {chartColors[i % chartColors.length]}"
									></div>
									<div class="flex-1">
										<div class="flex justify-between text-sm">
											<span class="truncate text-parchment-200">{shortMime(item.mime_type)}</span>
											<span class="shrink-0 text-parchment-500">{formatSize(item.total_bytes)}</span
											>
										</div>
										<div class="mt-1 h-2 w-full rounded-full bg-clay-800">
											<div
												class="h-2 rounded-full transition-all"
												style="width: {(item.total_bytes / maxTypeBytes()) *
													100}%; background-color: {chartColors[i % chartColors.length]}"
											></div>
										</div>
									</div>
								</div>
							{/each}
						</div>
						<div class="overflow-x-auto">
							<table class="w-full text-sm">
								<thead>
									<tr class="border-b border-clay-800 text-left text-parchment-500">
										<th class="pr-4 pb-2 font-medium">Type</th>
										<th class="pr-4 pb-2 font-medium">Count</th>
										<th class="pb-2 font-medium">Size</th>
									</tr>
								</thead>
								<tbody>
									{#each topTypes() as item, i}
										<tr class="border-b border-clay-800/50">
											<td class="py-2 pr-4 text-parchment-200">
												<span
													class="inline-block h-2.5 w-2.5 rounded-full"
													style="background-color: {chartColors[i % chartColors.length]}"
												></span>
												<span class="ml-2">{item.mime_type}</span>
											</td>
											<td class="py-2 pr-4 text-parchment-400">{formatNumber(item.count)}</td>
											<td class="py-2 text-parchment-400">{formatSize(item.total_bytes)}</td>
										</tr>
									{/each}
								</tbody>
							</table>
						</div>
					</div>
				</div>
			</section>
		{/if}

		{#if storageTrend.length > 0}
			<section>
				<h3 class="mb-3 text-base font-semibold text-parchment-200">Cumulative Storage Trend</h3>
				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<svg
						width="100%"
						viewBox="0 0 {svgWidth} {svgHeight}"
						class="overflow-visible"
						style="max-width: {svgWidth}px"
					>
						<defs>
							<linearGradient id="areaGrad" x1="0" x2="0" y1="0" y2="1">
								<stop offset="0%" stop-color="#c9953a" stop-opacity="0.3" />
								<stop offset="100%" stop-color="#c9953a" stop-opacity="0.02" />
							</linearGradient>
						</defs>

						{#each yTicks() as tick}
							<line
								x1={padding.left}
								y1={padding.top + chartH - (tick / trendMax()) * chartH}
								x2={padding.left + chartW}
								y2={padding.top + chartH - (tick / trendMax()) * chartH}
								stroke="#3a322a"
								stroke-width="1"
							/>
							<text
								x={padding.left - 6}
								y={padding.top + chartH - (tick / trendMax()) * chartH + 4}
								text-anchor="end"
								class="fill-parchment-500"
								font-size="11"
							>
								{formatSize(tick)}
							</text>
						{/each}

						<path d={trendPath(storageTrend)} fill="url(#areaGrad)" />

						<path d={trendLine(storageTrend)} fill="none" stroke="#c9953a" stroke-width="2" />

						{#each xLabels() as label}
							{#if label.show}
								<text
									x={label.x}
									y={svgHeight - 6}
									text-anchor="middle"
									class="fill-parchment-500"
									font-size="10"
								>
									{label.date}
								</text>
							{/if}
						{/each}
					</svg>
				</div>
			</section>
		{/if}
	{/if}
</div>
