<script>
	import { formatSize, formatNumber } from '$lib/utils/html.js';

	let {
		originalTypeBreakdown = [],
		storageTrend = [],
		avgFileSizeBytes = 0,
		totalPages = 0,
		totalWords = 0
	} = $props();

	const chartColors = ['#c9953a', '#3b5e8a', '#b84a3a', '#9a8c78', '#b07d2e', '#2e4a6e', '#5a3a2a'];

	const topTypes = $derived(() => {
		const sorted = [...originalTypeBreakdown].sort((a, b) => b.total_bytes - a.total_bytes);
		if (sorted.length <= 7) return sorted;
		const top = sorted.slice(0, 7);
		const other = sorted.slice(7).reduce(
			(acc, item) => {
				acc.count += item.count;
				acc.total_bytes += item.total_bytes;
				return acc;
			},
			{ original_type: 'other', count: 0, total_bytes: 0 }
		);
		return [...top, other];
	});

	const maxTypeBytes = $derived(() =>
		topTypes().length > 0 ? Math.max(...topTypes().map((t) => t.total_bytes)) : 1
	);

	const trendMax = $derived(() =>
		storageTrend.length > 0 ? Math.max(...storageTrend.map((t) => t.cumulative_bytes)) : 1
	);

	const dailyMax = $derived(() =>
		storageTrend.length > 0 ? Math.max(...storageTrend.map((t) => t.daily_bytes)) : 1
	);

	let chartEl = $state(null);
	let containerWidth = $state(600);

	$effect(() => {
		const el = chartEl;
		if (!el) return;
		const ro = new ResizeObserver(([entry]) => {
			containerWidth = Math.round(entry.contentRect.width);
		});
		ro.observe(el);
		return () => ro.disconnect();
	});

	let dailyChartEl = $state(null);
	let dailyContainerWidth = $state(600);

	$effect(() => {
		const el = dailyChartEl;
		if (!el) return;
		const ro = new ResizeObserver(([entry]) => {
			dailyContainerWidth = Math.round(entry.contentRect.width);
		});
		ro.observe(el);
		return () => ro.disconnect();
	});

	const svgWidth = $derived(Math.max(containerWidth, 300));
	const dailySvgWidth = $derived(Math.max(dailyContainerWidth, 300));
	const svgHeight = 200;
	const padding = { top: 10, right: 10, bottom: 30, left: 60 };
	const chartW = $derived(svgWidth - padding.left - padding.right);
	const dailyChartW = $derived(dailySvgWidth - padding.left - padding.right);
	const chartH = $derived(svgHeight - padding.top - padding.bottom);

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

	const dailyYTicks = $derived(() => {
		const max = dailyMax();
		if (max === 0) return [0];
		const niceMax = Math.ceil(max / (1024 * 1024)) * 1024 * 1024;
		const step = niceMax / 4;
		const ticks = [];
		for (let i = 0; i <= 4; i++) {
			ticks.push(Math.round(i * step));
		}
		return ticks;
	});

	function dailyBars(points) {
		if (points.length === 0) return [];

		if (points.length === 1) {
			const halfWidth = 12;
			const x = padding.left;
			const y = padding.top + chartH - (points[0].daily_bytes / dailyMax()) * chartH;
			return [
				{
					x: x - halfWidth,
					y,
					width: halfWidth * 2,
					height: padding.top + chartH - y,
					title: `${formatSize(points[0].daily_bytes)}\u00A0·\u00A0${points[0].daily_count}\u00A0docs`
				}
			];
		}

		const slotWidth = dailyChartW / points.length;
		const barWidth = slotWidth * 0.8;
		return points.map((p, i) => {
			const x = padding.left + i * slotWidth + (slotWidth - barWidth) / 2;
			const y = padding.top + chartH - (p.daily_bytes / dailyMax()) * chartH;
			return {
				x,
				y,
				width: barWidth,
				height: padding.top + chartH - y,
				title: `${formatSize(p.daily_bytes)}\u00A0·\u00A0${p.daily_count}\u00A0docs`
			};
		});
	}

	const fmtDate = new Intl.DateTimeFormat(undefined, { month: 'short', day: 'numeric' });

	const xLabelPositions = (width) => {
		if (storageTrend.length === 0) return [];
		const skip = storageTrend.length > 30 ? Math.ceil(storageTrend.length / 10) : 1;
		return storageTrend.map((p, i) => ({
			date: fmtDate.format(new Date(p.date)),
			show: i % skip === 0 || i === storageTrend.length - 1,
			x: padding.left + (i / Math.max(storageTrend.length - 1, 1)) * width
		}));
	};

	const xLabels = $derived(() => xLabelPositions(chartW));
	const dailyXLabels = $derived(() => xLabelPositions(dailyChartW));

	function shortMime(mime) {
		if (mime === 'other') return 'other';
		const parts = mime.split('/');
		return parts[parts.length - 1];
	}
</script>

<div class="space-y-6">
	{#if originalTypeBreakdown.length === 0 && storageTrend.length === 0}
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
				<h3 class="mb-3 text-base font-semibold text-balance text-parchment-200">
					Original Type Breakdown
				</h3>
				<div class="rounded-lg border border-clay-800 bg-clay-900 p-4">
					<div class="grid grid-cols-1 gap-6 lg:grid-cols-2">
						<div class="space-y-2">
							{#each topTypes() as item, i (i)}
								<div class="flex items-center gap-3">
									<div
										class="h-4 w-4 shrink-0 rounded"
										style="background-color: {chartColors[i % chartColors.length]}"
									></div>
									<div class="min-w-0 flex-1">
										<div class="flex justify-between text-sm">
											<span class="truncate text-parchment-200"
												>{shortMime(item.original_type)}</span
											>
											<span class="shrink-0 text-parchment-500">{formatSize(item.total_bytes)}</span
											>
										</div>
										<div class="mt-1 h-2 w-full rounded-full bg-clay-800">
											<div
												class="h-2 rounded-full transition-[width] motion-reduce:transition-none"
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
										<th class="pr-4 pb-2 font-medium" scope="col">Type</th>
										<th class="pr-4 pb-2 font-medium" scope="col">Count</th>
										<th class="pb-2 font-medium" scope="col">Size</th>
									</tr>
								</thead>
								<tbody>
									{#each topTypes() as item, i (i)}
										<tr class="border-b border-clay-800/50">
											<td class="py-2 pr-4 text-parchment-200">
												<span
													class="inline-block h-2.5 w-2.5 rounded-full"
													style="background-color: {chartColors[i % chartColors.length]}"
												></span>
												<span class="ml-2">{item.original_type}</span>
											</td>
											<td class="py-2 pr-4 text-parchment-400 tabular-nums"
												>{formatNumber(item.count)}</td
											>
											<td class="py-2 text-parchment-400 tabular-nums"
												>{formatSize(item.total_bytes)}</td
											>
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
			<div class="grid grid-cols-1 gap-4 lg:grid-cols-2">
				<section>
					<h3 class="mb-3 text-base font-semibold text-balance text-parchment-200">
						Cumulative Storage Trend
					</h3>
					<div bind:this={chartEl} class="rounded-lg border border-clay-800 bg-clay-900 p-4">
						<svg
							width="100%"
							viewBox="0 0 {svgWidth} {svgHeight}"
							class="overflow-visible"
							role="img"
							aria-label="Cumulative storage trend"
						>
							<defs>
								<linearGradient id="areaGrad" x1="0" x2="0" y1="0" y2="1">
									<stop offset="0%" stop-color="#c9953a" stop-opacity="0.3" />
									<stop offset="100%" stop-color="#c9953a" stop-opacity="0.02" />
								</linearGradient>
							</defs>

							{#each yTicks() as tick (tick)}
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

							{#each xLabels() as label (label.x + label.date)}
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

				<section>
					<h3 class="mb-3 text-base font-semibold text-balance text-parchment-200">
						Daily Storage Increase
					</h3>
					<div bind:this={dailyChartEl} class="rounded-lg border border-clay-800 bg-clay-900 p-4">
						<svg
							width="100%"
							viewBox="0 0 {dailySvgWidth} {svgHeight}"
							class="overflow-visible"
							role="img"
							aria-label="Daily storage increase"
						>
							{#each dailyYTicks() as tick (tick)}
								<line
									x1={padding.left}
									y1={padding.top + chartH - (tick / dailyMax()) * chartH}
									x2={padding.left + dailyChartW}
									y2={padding.top + chartH - (tick / dailyMax()) * chartH}
									stroke="#3a322a"
									stroke-width="1"
								/>
								<text
									x={padding.left - 6}
									y={padding.top + chartH - (tick / dailyMax()) * chartH + 4}
									text-anchor="end"
									class="fill-parchment-500"
									font-size="11"
								>
									{formatSize(tick)}
								</text>
							{/each}

							{#each dailyBars(storageTrend) as bar (bar.x)}
								<rect
									x={bar.x}
									y={bar.y}
									width={bar.width}
									height={bar.height}
									fill="#3b5e8a"
									role="img"
									aria-label={bar.title}
								>
									<title>{bar.title}</title>
								</rect>
							{/each}

							{#each dailyXLabels() as label (label.x + label.date)}
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
			</div>
		{/if}
	{/if}
</div>
