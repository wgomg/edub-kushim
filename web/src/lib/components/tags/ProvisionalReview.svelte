<script>
	import { onDestroy, onMount } from 'svelte';
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { api, encodeCriteria } from '#lib/api.js';
	import Icon from '#lib/components/Icon.svelte';
	import Modal from '#lib/components/Modal.svelte';
	import SlideOver from '#lib/components/SlideOver.svelte';
	import FoldTagModal from '#lib/components/tags/FoldTagModal.svelte';
	import ProvisionalFilters from '#lib/components/tags/ProvisionalFilters.svelte';
	import TagReviewHelp from '#lib/components/tags/TagReviewHelp.svelte';
	import { confirmStore } from '#lib/stores/confirmStore.svelte.js';
	import { toastStore } from '#lib/stores/toastStore.svelte.js';
	import { documentsForTagHref, documentsLabel, formatNumber } from '#lib/utils/html.js';
	import { demotionLabel } from '#lib/utils/tagReconcile.js';

	const PAGE_SIZE = 50;

	const criteria = [
		{ key: 'subject_matter', label: 'subject matter' },
		{ key: 'dimension', label: 'dimension' },
		{ key: 'not_person', label: 'not person' },
		{ key: 'atomic', label: 'atomic' },
		{ key: 'not_form_genre', label: 'not publishing format' },
		{ key: 'defer', label: 'defer' }
	];

	// review states the compact bar offers, grouped exactly as the menu renders them; the values mirror
	// the server's canonical list
	const reviewStates = [
		{ value: 'no_verdict', label: 'no verdict', group: 'Verdict' },
		{ value: 'admitted', label: 'awaiting promotion floor', group: 'Verdict' },
		{ value: 'deferred', label: 'deferred to review', group: 'Verdict' },
		{ value: 'rejected', label: 'judge rejected', group: 'Verdict' },
		{ value: 'accepted', label: 'accepted deferral', group: 'Verdict' },
		{ value: 'identity_pending', label: 'identity pending', group: 'Blocking' },
		{ value: 'identity_hold', label: 'identity hold', group: 'Blocking' },
		{ value: 'split_hold', label: 'split hold', group: 'Blocking' },
		{ value: 'degraded', label: 'degraded composition', group: 'Blocking' },
		{ value: 'demoted', label: 'demoted', group: 'Blocking' },
		{ value: 'needs_decision', label: 'needs decision', group: 'Action' },
		{ value: 'unblocked', label: 'awaiting floor, unblocked', group: 'Action' }
	];

	// older links carry the single verdict filter; map it onto the states the server newly exposes so
	// they keep landing filtered; the old admit class folded accepted deferrals in, so it maps them to
	// both admitted and accepted
	const LEGACY_VERDICT_STATES = {
		none: ['no_verdict'],
		admit: ['admitted', 'accepted'],
		defer: ['deferred'],
		reject: ['rejected']
	};

	// defer records the judge's abstention, not a content criterion: the verdict badge already says
	// when it fired, so the rubric counts only the five substantive checks
	const rubricCriteria = criteria.filter((c) => c.key !== 'defer');

	const cardClass =
		'grid grid-cols-1 gap-2 rounded-lg border border-clay-800 bg-clay-900 p-3.5 md:grid-cols-[minmax(0,1fr)_16rem] md:gap-x-3 md:pl-4';
	const contentClass = 'order-1 flex min-w-0 flex-col gap-1.5 md:col-start-1 md:row-start-1';
	const sideClass =
		'contents md:col-start-2 md:row-start-1 md:flex md:flex-col md:gap-1 md:self-start md:border-l md:border-clay-800 md:pl-3';
	const decisionsClass =
		'order-2 grid grid-cols-2 gap-1.5 border-t border-clay-800 pt-2 md:order-none md:border-t-0 md:pt-0';
	const sideSeparatorClass = 'my-1.5 hidden h-px bg-clay-800 md:block';
	const utilitiesClass = 'order-4 flex flex-wrap gap-x-2.5 gap-y-0.5 md:order-none';
	const resolveClass =
		'order-3 flex flex-wrap items-center gap-1.5 border-t border-clay-800 pt-2 md:col-span-2 md:order-none';
	const resolveLabelClass =
		'shrink-0 text-[0.59375rem] font-semibold tracking-[0.09em] text-parchment-600 uppercase';

	const criteriaPill = 'rounded-full px-2 py-0.5 font-mono text-[0.6875rem] whitespace-nowrap';
	const pillBase =
		'rounded-full px-2 py-0.5 font-mono text-[0.6875rem] font-medium whitespace-nowrap';
	const pillPlain = `${pillBase} bg-clay-800 text-parchment-400`;
	const pillOk = `${pillBase} bg-emerald-600/20 text-emerald-500`;
	const pillWarn = `${pillBase} bg-amber-600/20 text-amber-400`;
	const pillButton =
		'cursor-pointer hover:brightness-125 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none';
	const summaryPill = `${criteriaPill} bg-clay-800 text-parchment-500`;

	const btnApprove =
		'inline-flex w-full items-center justify-center gap-1.5 rounded-md bg-gold-500 px-2 py-1 text-xs font-medium text-clay-950 hover:bg-gold-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-40';
	const btnReject =
		'inline-flex w-full items-center justify-center gap-1.5 rounded-md px-2 py-1 text-xs text-parchment-400 hover:bg-terracotta-900/40 hover:text-terracotta-400 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-40';
	const btnAction =
		'inline-flex max-w-full items-center gap-1.5 rounded-md border border-clay-700 px-2.5 py-1 text-xs font-medium text-parchment-300 hover:border-gold-500 hover:text-gold-400 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-40';
	const btnUtil =
		'inline-flex items-center gap-1 rounded px-0 py-0.5 text-[0.6875rem] text-parchment-500 hover:text-parchment-200 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-40';
	const btnUtilDanger = `${btnUtil} hover:text-terracotta-400`;
	const btnHelp =
		'inline-flex items-center gap-1.5 rounded-lg border border-clay-700 px-2.5 py-1 text-xs font-medium text-parchment-300 hover:border-gold-500 hover:text-gold-400 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none';

	const iconButton =
		'rounded-md p-1.5 text-parchment-400 hover:bg-clay-800 hover:text-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-40';
	const btnGhost =
		'rounded-lg px-3 py-1.5 text-xs text-parchment-400 hover:bg-clay-800 hover:text-terracotta-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-50';
	const countLink =
		'flex items-center gap-1.5 text-xs text-parchment-400 tabular-nums underline-offset-[3px] hover:text-lapis-300 hover:underline hover:decoration-lapis-300/40 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none';
	const pagerButton =
		'rounded-md border border-clay-700 px-2.5 py-1 text-parchment-400 hover:border-gold-500 hover:text-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-40';
	const inputClass =
		'w-full rounded-md border border-clay-700 bg-clay-900 px-3 py-1.5 text-sm text-parchment-200 placeholder-parchment-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none';
	const btnPrimary =
		'rounded-lg bg-gold-500 px-3 py-1.5 text-xs font-medium text-clay-950 hover:bg-gold-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-50';

	let { onchanged = null } = $props();

	let tags = $state([]);
	let total = $state(0);
	let loading = $state(true);
	let error = $state('');
	let acting = $state(null);
	let identityActing = $state(false);
	let offset = $state(0);
	let pendingTotal = $state(0);
	let filterStates = $state({});
	let stateFilter = $state([]);
	let stateValues = $state(null);
	let facets = $state(null);
	let facetsOpen = $state(false);
	let query = $state('');
	let loadSeq = 0;

	let helpOpen = $state(false);
	let helpSection = $state('overview');
	let renaming = $state(null);
	let renameName = $state('');
	let renameError = $state('');
	let renamingBusy = $state(false);
	let splitting = $state(null);
	let splitParts = $state([]);
	let splitError = $state('');
	let splittingBusy = $state(false);
	let folding = $state(null);

	let hasFilters = $derived(
		query !== '' || criteria.some((c) => filterStates[c.key]) || stateFilter.length > 0
	);

	// the server owns the canonical state values; the local list only supplies the labels and the menu
	// grouping
	let activeReviewStates = $derived(
		stateValues ? reviewStates.filter((s) => stateValues.includes(s.value)) : reviewStates
	);

	onMount(() => {
		const sp = page.url.searchParams;

		query = sp.get('prov_q') ?? '';

		const rawPage = sp.get('prov_page') ?? '';
		if (/^\d+$/.test(rawPage)) offset = parseInt(rawPage, 10) * PAGE_SIZE;

		const rawCriteria = sp.get('prov_criteria') ?? '';
		for (const entry of rawCriteria ? rawCriteria.split(',') : []) {
			const [key, state] = entry.split(':');
			if (criteria.some((c) => c.key === key) && ['', 'pass', 'fail', 'absent'].includes(state)) {
				filterStates[key] = state;
			}
		}

		const rawStates = sp.get('prov_state') ?? '';
		for (const value of rawStates ? rawStates.split(',') : []) {
			if (reviewStates.some((s) => s.value === value) && !stateFilter.includes(value)) {
				stateFilter = [...stateFilter, value];
			}
		}
		if (stateFilter.length === 0) {
			const legacy = LEGACY_VERDICT_STATES[sp.get('prov_verdict') ?? ''] ?? [];
			for (const value of legacy) {
				if (reviewStates.some((s) => s.value === value) && !stateFilter.includes(value)) {
					stateFilter = [...stateFilter, value];
				}
			}
		}

		load();
	});

	function activeCriteria() {
		return criteria
			.filter((c) => filterStates[c.key])
			.map((c) => ({ key: c.key, state: filterStates[c.key] }));
	}

	function syncUrl() {
		// transient helper to sync the URL with local state; SvelteURLSearchParams is not imported in this file
		// eslint-disable-next-line svelte/prefer-svelte-reactivity
		const sp = new URLSearchParams(page.url.searchParams);
		if (offset > 0) sp.set('prov_page', String(offset / PAGE_SIZE));
		else sp.delete('prov_page');

		if (query) sp.set('prov_q', query);
		else sp.delete('prov_q');

		const active = encodeCriteria(activeCriteria());

		if (active) sp.set('prov_criteria', active);
		else sp.delete('prov_criteria');
		if (stateFilter.length > 0) sp.set('prov_state', stateFilter.join(','));
		else sp.delete('prov_state');
		sp.delete('prov_verdict');

		goto(resolve(`${page.url.pathname}?${sp.toString()}${page.url.hash}`), {
			shallow: true,
			replace: true
		});
	}

	async function load(clamp = true, silent = false) {
		const seq = ++loadSeq;
		if (!silent) loading = true;
		error = '';
		const result = await api.tags.provisionalList(PAGE_SIZE, offset, {
			criteria: activeCriteria(),
			states: stateFilter,
			q: query,
			facets: facetsOpen
		});
		if (seq !== loadSeq) return;
		if (!result.ok) {
			if (silent) return;
			tags = [];
			total = 0;
			pendingTotal = 0;
			error = 'Failed to load new tags.';
			loading = false;
			return;
		}
		tags = result.data?.results ?? [];
		total = result.data?.total ?? 0;
		pendingTotal = result.data?.pending_identity_total ?? 0;
		const values = result.data?.state_values;
		if (Array.isArray(values) && values.length > 0) stateValues = values;
		if (facetsOpen) facets = result.data?.state_facets ?? null;
		// an emptied page and a deep-link offset the API clamped away both
		// surface as offset >= total
		if (clamp && offset > 0 && total <= offset) {
			offset = Math.max(0, (Math.ceil(total / PAGE_SIZE) - 1) * PAGE_SIZE);
			syncUrl();
			return load(false);
		}
		loading = false;
	}

	function onFilterChange() {
		offset = 0;
		load();
		syncUrl();
	}

	function onQueryChange(value) {
		query = value;
		onQueryInput();
	}

	// the menu shows what each state would add, so opening it asks the API for the facet counts while
	// the request runs silently so the queue does not flash a loader; closing it stops carrying facets
	// on later list, paging and keystroke requests
	function onFacetsRequested() {
		facetsOpen = true;
		load(true, true);
	}

	function onFacetsClosed() {
		facetsOpen = false;
	}

	function applyFilters(next) {
		query = next.q;
		filterStates = { ...next.criteria };
		stateFilter = [...next.states];
		onFilterChange();
	}

	// one load runs the list query, its count and the per-row identity lookups, so keystrokes wait for a
	// pause instead of firing a full query set per character
	let queryTimer;
	function onQueryInput() {
		clearTimeout(queryTimer);
		queryTimer = setTimeout(() => onFilterChange(), 300);
	}
	onDestroy(() => clearTimeout(queryTimer));

	function clearFilters() {
		applyFilters({ q: '', criteria: {}, states: [] });
	}

	function prevPage() {
		if (offset === 0) return;
		offset = Math.max(0, offset - PAGE_SIZE);
		load();
		syncUrl();
	}

	function nextPage() {
		if (offset + tags.length >= total) return;
		offset += PAGE_SIZE;
		load();
		syncUrl();
	}

	function openHelp(section) {
		helpSection = section;
		helpOpen = true;
	}

	async function approve(tag) {
		acting = tag.id;
		const result = await api.tags.promote(tag.id);
		acting = null;
		if (result.ok) {
			toastStore.success(`Approved "${tag.name}"`);
			if (onchanged) onchanged();
		} else if (result.status !== 409) {
			toastStore.error('Failed to approve tag');
		}
		await load();
	}

	async function reject(tag) {
		const ok = await confirmStore.confirm({
			title: 'Reject tag',
			message: `Reject tag "${tag.name}"? It is deleted and blocked from enrichment.`,
			danger: true
		});
		if (!ok) return;
		acting = tag.id;
		const result = await api.tags.delete(tag.id);
		acting = null;
		if (result.ok) {
			toastStore.success(`Rejected "${tag.name}"`);
			if (onchanged) onchanged();
		} else {
			toastStore.error('Failed to reject tag');
		}
		await load();
	}

	async function retire(tag) {
		const ok = await confirmStore.confirm({
			title: 'Retire tag',
			message: `Retire "${tag.name}"? The tag is removed now and its ${documentsLabel(tag.document_count)} lose the assignment. The name is not blocked, so enrichment may propose it again.`,
			danger: true
		});
		if (!ok) return;
		acting = tag.id;
		const result = await api.tags.retire(tag.id);
		acting = null;
		if (result.ok) {
			toastStore.success(`Retired "${tag.name}"`);
			if (onchanged) onchanged();
		} else {
			toastStore.error('Failed to retire tag');
		}
		await load();
	}

	async function accept(tag) {
		const ok = await confirmStore.confirm({
			title: 'Accept as provisional',
			message: `Accept "${tag.name}" as provisional? The deferral is cleared; the value waits for the promotion floor and promotes once enough live documents carry it.`
		});
		if (!ok) return;
		acting = tag.id;
		const result = await api.tags.accept(tag.id);
		acting = null;
		if (result.ok) {
			toastStore.success(`Accepted "${tag.name}" as provisional`);
			if (onchanged) onchanged();
		} else if (result.status === 409) {
			toastStore.error(result.data?.error ?? 'The tag cannot be accepted');
		} else {
			toastStore.error('Failed to accept the tag');
		}
		await load();
	}

	async function foldInto(tag) {
		const anchor = tag.identity_hold;
		const ok = await confirmStore.confirm({
			title: 'Fold into the existing value',
			message: `Fold "${tag.name}" into "${anchor}"? Its ${documentsLabel(tag.document_count)} move onto "${anchor}", and "${tag.name}" stops existing as a tag.`,
			danger: true
		});
		if (!ok) return;
		acting = tag.id;
		const result = await api.tags.fold(tag.name, anchor);
		acting = null;
		if (result.ok) {
			toastStore.success(`Folded "${tag.name}" into "${anchor}"`);
			if (onchanged) onchanged();
		} else if (result.status === 409) {
			toastStore.error(result.data?.error ?? 'Fold did not apply — the target may already be gone');
		} else {
			toastStore.error('Failed to fold tag');
		}
		await load();
	}

	async function makeCanonical(tag) {
		const anchor = tag.identity_hold;
		const ok = await confirmStore.confirm({
			title: 'Make this tag canonical',
			message: `Fold "${anchor}" into "${tag.name}"? "${anchor}" loses its row and standing, its documents move onto "${tag.name}", and "${tag.name}" stays provisional until approved.`,
			danger: true
		});
		if (!ok) return;
		acting = tag.id;
		const result = await api.tags.fold(anchor, tag.name);
		acting = null;
		if (result.ok) {
			toastStore.success(`Folded "${anchor}" into "${tag.name}"`);
			if (onchanged) onchanged();
		} else if (result.status === 409) {
			toastStore.error(result.data?.error ?? 'Fold did not apply — the target may already be gone');
		} else {
			toastStore.error('Failed to fold tag');
		}
		await load();
	}

	async function keepSeparate(tag) {
		const anchor = tag.identity_hold;
		const ok = await confirmStore.confirm({
			title: 'Keep the values separate',
			message: `Keep "${tag.name}" separate from "${anchor}"? The identity hold clears; the value keeps standing on its own and follows the ordinary review and promotion rules.`
		});
		if (!ok) return;
		acting = tag.id;
		const result = await api.tags.separate(tag.id);
		acting = null;
		if (result.ok) {
			toastStore.success(`Kept "${tag.name}" separate from "${anchor}"`);
			if (onchanged) onchanged();
		} else if (result.status === 409) {
			toastStore.error(result.data?.error ?? 'The hold is already gone');
		} else {
			toastStore.error('Failed to keep the values separate');
		}
		await load();
	}

	async function keepWhole(tag) {
		const ok = await confirmStore.confirm({
			title: 'Keep the compound whole',
			message: `Keep "${tag.name}" whole? The hold is cleared and the value stays as it is.`
		});
		if (!ok) return;
		await submitComposition(tag, 'keep', []);
	}

	async function applyParts(tag) {
		const parts = tag.composition_parts ?? [];
		const ok = await confirmStore.confirm({
			title: 'Apply the parts alongside',
			message: `Apply ${parts.length} part(s) alongside "${tag.name}"? No documents move; the parts are attached to the same documents.`
		});
		if (!ok) return;
		await submitComposition(tag, 'keep', parts);
	}

	async function submitComposition(tag, disposition, parts) {
		acting = tag.id;
		const result = await api.tags.compositionDecision(tag.id, disposition, parts);
		acting = null;
		if (result.ok) {
			if (disposition === 'split') toastStore.success(`Split "${tag.name}"`);
			else if (parts.length > 0) toastStore.success(`Applied the parts alongside "${tag.name}"`);
			else toastStore.success(`Kept "${tag.name}" whole`);
			if (onchanged) onchanged();
		} else if (result.status === 409) {
			toastStore.error('Tag is no longer provisional');
		} else {
			toastStore.error('Failed to apply the decision');
		}
		await load();
	}

	function openSplit(tag) {
		splitting = tag;
		splitParts = (tag.composition_parts ?? []).map((name) => ({ name }));
		if (splitParts.length === 0) splitParts = [{ name: '' }];
		splitError = '';
	}

	function addSplitPart() {
		splitParts = [...splitParts, { name: '' }];
	}

	function removeSplitPart(index) {
		splitParts = splitParts.filter((_, i) => i !== index);
		if (splitParts.length === 0) splitParts = [{ name: '' }];
	}

	async function submitSplit() {
		if (!splitting) return;
		const parts = splitParts.map((part) => part.name.trim()).filter(Boolean);
		if (parts.length === 0) {
			splitError = 'At least one component is required';
			return;
		}
		splittingBusy = true;
		const tag = splitting;
		const result = await api.tags.compositionDecision(tag.id, 'split', parts);
		splittingBusy = false;
		if (result.ok) {
			toastStore.success(`Split "${tag.name}"`);
			splitting = null;
			if (onchanged) onchanged();
			await load();
		} else if (result.status === 409) {
			splitError = 'Tag is no longer provisional';
		} else {
			splitError = 'Failed to split the tag';
		}
	}

	function openRename(tag) {
		renaming = tag;
		renameName = tag.name;
		renameError = '';
	}

	async function submitRename() {
		if (!renaming) return;
		const name = renameName.trim();
		if (!name) {
			renameError = 'Tag name is required';
			return;
		}
		renamingBusy = true;
		const tag = renaming;
		const result = await api.tags.update(tag.id, name);
		renamingBusy = false;
		if (result.ok) {
			toastStore.success(`Renamed "${tag.name}"`);
			renaming = null;
			if (onchanged) onchanged();
			await load();
		} else if (result.status === 409) {
			renameError = 'Tag already exists';
		} else {
			renameError = 'Failed to rename the tag';
		}
	}

	async function judgeNow(tag) {
		await enqueueRejudge(tag, 'quality');
	}

	async function rejudgeComposition(tag) {
		await enqueueRejudge(tag, 'composition');
	}

	async function enqueueRejudge(tag, scope) {
		acting = tag.id;
		const result = await api.tags.rejudge(tag.id, scope);
		acting = null;
		if (result.ok) {
			toastStore.success(rejudgeMessage(scope));
		} else if (result.status === 409) {
			toastStore.error(result.data?.error ?? 'Nothing to re-judge');
		} else {
			toastStore.error('Failed to queue the re-judge');
		}
		await load();
	}

	function rejudgeMessage(scope) {
		return scope === 'composition' ? 'Re-judge queued' : 'Quality judge queued';
	}

	async function reevaluateIdentity() {
		identityActing = true;
		const result = await api.tags.reevaluateIdentity();
		identityActing = false;
		if (result.ok) {
			toastStore.success('Identity adjudication queued');
		} else {
			toastStore.error('Failed to queue identity adjudication');
		}
		await load();
	}

	function criterionMark(value) {
		if (value === true) return '✓';
		if (value === false) return '✗';
		return '—';
	}

	function criterionClass(value) {
		if (value === true) return 'bg-emerald-600/20 text-emerald-500';
		if (value === false) return 'bg-clay-800 text-terracotta-500';
		return 'bg-clay-800 text-parchment-600';
	}

	function passingCount(tag) {
		return rubricCriteria.filter((c) => tag.verdict.criteria?.[c.key] === true).length;
	}

	function failingCriteria(tag) {
		return rubricCriteria.filter((c) => tag.verdict.criteria?.[c.key] !== true);
	}

	function rubricSummary(tag) {
		const passed = passingCount(tag);
		return passed === rubricCriteria.length
			? `rubric ${passed}/${rubricCriteria.length} ✓`
			: `rubric ${passed}/${rubricCriteria.length}`;
	}

	function rubricTitle(tag) {
		const detail = rubricCriteria
			.map((c) => `${c.label} ${criterionMark(tag.verdict.criteria?.[c.key])}`)
			.join(', ');
		const defer = tag.verdict.criteria?.defer;
		const deferDetail =
			defer === true ? 'defer: abstained' : defer === false ? 'defer: did not abstain' : 'defer: —';
		return [verdictMeta(tag), detail, deferDetail].filter(Boolean).join(' · ');
	}

	function verdictPillClass(tag) {
		if (!tag.verdict) return pillPlain;
		if (isAccepted(tag)) return pillOk;
		return tag.verdict.pass ? pillOk : pillWarn;
	}

	function isAccepted(tag) {
		return !!tag.accepted_at;
	}

	function isBareDeferral(tag) {
		return (
			!!tag.verdict &&
			verdictOutcome(tag) === 'defer' &&
			!isAccepted(tag) &&
			!tag.identity_hold &&
			!tag.composition_action &&
			!tag.demoted_at
		);
	}

	function verdictOutcome(tag) {
		if (tag.verdict.outcome) return tag.verdict.outcome;
		if (tag.verdict.pass) return 'admit';
		return tag.verdict.criteria?.defer === true ? 'defer' : 'reject';
	}

	function verdictLabel(tag) {
		if (!tag.verdict) return 'no verdict';
		if (isAccepted(tag)) return 'accepted — awaiting promotion floor';
		if (tag.verdict.pass) return 'awaiting promotion floor';
		return verdictOutcome(tag) === 'defer' ? 'deferred to review' : 'judge rejected';
	}

	function verdictTitle(tag) {
		if (!tag.verdict) return 'Never judged — no verdict was recorded when this tag was born';
		if (isAccepted(tag))
			return 'An operator accepted this deferral; the value now waits for the promotion floor';
		if (tag.verdict.pass) return 'The quality judge admitted this tag';
		if (verdictOutcome(tag) === 'defer') return 'The quality judge deferred this tag to review';
		return 'The quality judge rejected this tag';
	}

	function verdictMeta(tag) {
		return [tag.verdict.model, new Date(tag.verdict.verdict_at).toLocaleString()]
			.filter(Boolean)
			.join(' · ');
	}
</script>

<div class="mb-4 flex flex-wrap items-center gap-x-3 gap-y-2">
	<h2 class="text-base font-semibold text-parchment-200">Provisional queue</h2>
	{#if !loading && !error}
		<span class="text-[0.8125rem] text-parchment-500">
			{formatNumber(total)} awaiting review{#if hasFilters}
				· filtered{/if}
		</span>
	{/if}
	<div class="ml-auto flex items-center gap-2">
		{#if pendingTotal > 0}
			<button
				onclick={reevaluateIdentity}
				disabled={identityActing}
				class={btnGhost}
				title="Adjudicate the pending identity evidence now instead of waiting for the daily identity pass"
			>
				{identityActing ? 'Queueing…' : 'Reevaluate identity'}
			</button>
		{/if}
		<button
			type="button"
			class={btnHelp}
			title="Open the tag review guide"
			onclick={() => openHelp('overview')}
		>
			<Icon name="help" size={14} />
			Help
		</button>
	</div>
</div>

<ProvisionalFilters
	q={query}
	{criteria}
	criteriaStates={filterStates}
	reviewStates={activeReviewStates}
	states={stateFilter}
	{facets}
	onquerychange={onQueryChange}
	onchange={applyFilters}
	onfacets={onFacetsRequested}
	onfacetsclose={onFacetsClosed}
/>

<p class="mb-4 max-w-3xl text-[0.8125rem] text-parchment-500">
	New tags stay provisional until the admission decision admits them and enough live documents carry
	them to clear the promotion floor. Approve promotes a tag into the shared vocabulary; reject
	deletes it and blocks it from enrichment.
	<button
		type="button"
		class="underline underline-offset-[3px] hover:text-parchment-300"
		onclick={() => openHelp('overview')}>How this queue works</button
	>.
</p>

{#if loading}
	<p class="text-[0.8125rem] text-parchment-500">Loading new tags…</p>
{:else if error}
	<p class="text-[0.8125rem] text-terracotta-500" role="alert">{error}</p>
{:else if tags.length === 0}
	<div
		class="rounded-lg border border-dashed border-clay-800 px-4 py-12 text-center text-parchment-500"
	>
		{#if hasFilters}
			<p>
				No tags match the current filters.
				<button
					onclick={clearFilters}
					class="underline underline-offset-[3px] hover:text-parchment-300"
				>
					Clear filters
				</button>
			</p>
		{:else}
			<p>
				<span
					class="mr-1.5 inline-block h-[0.45rem] w-[0.45rem] rounded-full bg-emerald-400"
					aria-hidden="true"
				></span>
				<b class="font-semibold text-parchment-300">Queue is clear.</b>
				No provisional tags. New tags appear here once enrichment assigns them to a document.
			</p>
		{/if}
	</div>
{:else}
	<ul class="flex flex-col gap-2">
		{#each tags as tag (tag.id)}
			<li class={cardClass}>
				<div class={contentClass}>
					<div class="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
						<span class="text-[0.9375rem] font-semibold text-parchment-200">{tag.name}</span>
						<a href={resolve(documentsForTagHref(tag.name))} class={countLink}>
							<Icon name="external-link" size={12} class="text-lapis-400" />
							{documentsLabel(tag.document_count)}
						</a>
						<span class="text-xs text-parchment-500">
							· created {new Date(tag.created_at).toLocaleDateString()}
						</span>
					</div>

					<div class="flex flex-wrap items-center gap-1.5">
						<button
							type="button"
							class="{verdictPillClass(tag)} {pillButton}"
							title={verdictTitle(tag)}
							onclick={() => openHelp('verdict')}>{verdictLabel(tag)}</button
						>
						{#if tag.pending_identity > 0}
							<button
								type="button"
								class="{pillWarn} {pillButton}"
								title="This tag is waiting for a same-intent decision; the daily identity pass resolves it, or trigger the reevaluation"
								onclick={() => openHelp('identity-pending')}>identity pending</button
							>
						{/if}
						{#if tag.identity_hold}
							<button
								type="button"
								class="{pillWarn} {pillButton}"
								title="The identity judge refused a fold of this tag onto that store value; promotion waits for review"
								onclick={() => openHelp('identity-hold')}
								>identity hold → {tag.identity_hold}</button
							>
						{/if}
						{#if tag.composition_hold}
							<button
								type="button"
								class="{pillWarn} {pillButton}"
								title="The split decision could not settle this compound; promotion waits for review"
								onclick={() => openHelp('split-hold')}>split hold → {tag.composition_hold}</button
							>
						{/if}
						{#if tag.composition_reason === 'judge_unavailable' && !tag.composition_hold}
							<button
								type="button"
								class="{pillPlain} {pillButton}"
								title="No split judge was available; the compound was kept whole and is re-judged when emitted again"
								onclick={() => openHelp('composition-degraded')}
								>composition → judge_unavailable</button
							>
						{/if}
						{#if tag.demoted_at}
							<button
								type="button"
								class="{pillWarn} {pillButton}"
								title="This tag's standing was revoked; approve to restore it"
								onclick={() => openHelp('demoted')}
								>demoted → {demotionLabel(tag.demoted_reason)}</button
							>
						{/if}
					</div>

					{#if tag.verdict?.criteria}
						<div class="flex flex-wrap items-center gap-1.5" title={rubricTitle(tag)}>
							{#each failingCriteria(tag) as c (c.key)}
								<span class="{criteriaPill} {criterionClass(tag.verdict.criteria?.[c.key])}">
									{c.label}
									{criterionMark(tag.verdict.criteria?.[c.key])}
								</span>
							{/each}
							<span class={summaryPill}>{rubricSummary(tag)}</span>
						</div>
					{/if}
				</div>

				<div class={sideClass}>
					<div class={decisionsClass}>
						<button
							type="button"
							class={btnApprove}
							title="Approve — promote into the shared vocabulary"
							disabled={acting === tag.id}
							onclick={() => approve(tag)}
						>
							<Icon name="check" size={14} />
							Approve
						</button>
						<button
							type="button"
							class={btnReject}
							title="Reject — delete and block the name"
							disabled={acting === tag.id}
							onclick={() => reject(tag)}
						>
							<Icon name="x" size={14} />
							Reject
						</button>
					</div>
					<div class={sideSeparatorClass}></div>
					<div class={utilitiesClass}>
						{#if !tag.verdict}
							<button
								type="button"
								class={btnUtil}
								title="Judge this tag now"
								disabled={acting === tag.id}
								onclick={() => judgeNow(tag)}
							>
								<Icon name="judge-now" size={13} />
								Judge now
							</button>
						{/if}
						<button
							type="button"
							class={btnUtil}
							title="Fold this tag into another tag; its documents move there and this name becomes an alias"
							disabled={acting === tag.id}
							onclick={() => (folding = tag)}
						>
							<Icon name="fold-into" size={13} />
							Fold into…
						</button>
						<button
							type="button"
							class={btnUtil}
							title="Rename this tag"
							disabled={acting === tag.id}
							onclick={() => openRename(tag)}
						>
							<Icon name="pencil" size={13} />
							Rename
						</button>
						<button
							type="button"
							class={btnUtilDanger}
							title="Retire — remove the tag without blocking the name"
							disabled={acting === tag.id}
							onclick={() => retire(tag)}
						>
							<Icon name="retire" size={13} />
							Retire
						</button>
					</div>
				</div>

				{#if tag.identity_hold || tag.composition_action === 'undecided' || tag.composition_action === 'keep' || tag.composition_action === 'degraded' || isBareDeferral(tag)}
					<div class={resolveClass}>
						<span class={resolveLabelClass}>Resolve</span>
						{#if isBareDeferral(tag)}
							<button
								type="button"
								class={btnAction}
								title="Clear the deferral: the value waits for the promotion floor like any admitted tag"
								disabled={acting === tag.id}
								onclick={() => accept(tag)}
							>
								<Icon name="circle-check" size={13} />
								Accept as provisional
							</button>
						{/if}
						{#if tag.identity_hold}
							<button
								type="button"
								class={btnAction}
								title="Fold this tag into “{tag.identity_hold}”: its documents move there and this name becomes an alias"
								disabled={acting === tag.id}
								onclick={() => foldInto(tag)}
							>
								<Icon name="fold-into" size={13} />
								Fold into “{tag.identity_hold}”
							</button>
							<button
								type="button"
								class={btnAction}
								title="Fold “{tag.identity_hold}” into this tag: this name becomes canonical"
								disabled={acting === tag.id}
								onclick={() => makeCanonical(tag)}
							>
								<Icon name="make-canonical" size={13} />
								Make canonical
							</button>
							<button
								type="button"
								class={btnAction}
								title="Keep this tag separate from “{tag.identity_hold}”: the hold clears and the value stands on its own"
								disabled={acting === tag.id}
								onclick={() => keepSeparate(tag)}
							>
								<Icon name="keep-separate" size={13} />
								Keep separate
							</button>
						{/if}
						{#if tag.composition_action === 'undecided'}
							<button
								type="button"
								class={btnAction}
								title="Split the compound into its components…"
								disabled={acting === tag.id}
								onclick={() => openSplit(tag)}
							>
								<Icon name="split" size={13} />
								Split now…
							</button>
							<button
								type="button"
								class={btnAction}
								title="Record that the compound is a value in its own right"
								disabled={acting === tag.id}
								onclick={() => keepWhole(tag)}
							>
								<Icon name="keep-whole" size={13} />
								Keep whole
							</button>
						{/if}
						{#if tag.composition_action === 'keep'}
							{#if (tag.composition_parts ?? []).length > 0}
								<button
									type="button"
									class={btnAction}
									title="Apply the parts alongside the compound"
									disabled={acting === tag.id}
									onclick={() => applyParts(tag)}
								>
									<Icon name="apply-parts" size={13} />
									Apply parts alongside
								</button>
							{/if}
							<button
								type="button"
								class={btnAction}
								title="Record that the compound is a value in its own right"
								disabled={acting === tag.id}
								onclick={() => keepWhole(tag)}
							>
								<Icon name="keep-whole" size={13} />
								Keep whole
							</button>
						{/if}
						{#if tag.composition_action === 'degraded'}
							<button
								type="button"
								class={btnAction}
								title="Queue a fresh composition judgment"
								disabled={acting === tag.id}
								onclick={() => rejudgeComposition(tag)}
							>
								<Icon name="rotate-cw" size={13} />
								Re-judge
							</button>
						{/if}
					</div>
				{/if}
			</li>
		{/each}
	</ul>
	{#if total > 0}
		<div class="flex flex-wrap items-center justify-between gap-3 pt-1 text-sm">
			<p class="text-parchment-500">
				Showing {offset + 1}–{offset + tags.length} of {total}
			</p>
			<div class="flex items-center gap-3">
				<button disabled={offset === 0} onclick={prevPage} class={pagerButton}>Prev</button>
				<button disabled={offset + tags.length >= total} onclick={nextPage} class={pagerButton}>
					Next
				</button>
			</div>
		</div>
	{/if}
{/if}

<SlideOver open={helpOpen} title="Tag review guide" onClose={() => (helpOpen = false)}>
	<TagReviewHelp section={helpSection} />
</SlideOver>

<Modal
	open={renaming !== null}
	title="Rename tag"
	onClose={() => {
		renaming = null;
		renamingBusy = false;
	}}
>
	<form
		onsubmit={(e) => {
			e.preventDefault();
			submitRename();
		}}
	>
		<div class="space-y-4">
			<div>
				<label for="rename-tag" class="mb-1 block text-xs font-medium text-parchment-400"
					>Name</label
				>
				<input
					id="rename-tag"
					name="rename-tag"
					type="text"
					bind:value={renameName}
					autocomplete="off"
					class={inputClass}
				/>
				<p class="mt-1.5 text-[0.75rem] text-parchment-600">
					The verdict and hold journals are keyed by name, so the tag shows no verdict until it is
					judged again under the new name.
				</p>
			</div>
			{#if renameError}
				<p class="text-sm text-terracotta-500" aria-live="polite">{renameError}</p>
			{/if}
			<div class="flex justify-end gap-2">
				<button
					type="button"
					onclick={() => (renaming = null)}
					class="rounded-md px-3 py-1.5 text-xs font-medium text-parchment-400 hover:bg-clay-800 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
				>
					Cancel
				</button>
				<button type="submit" disabled={renamingBusy} class={btnPrimary}>
					{renamingBusy ? 'Saving…' : 'Save'}
				</button>
			</div>
		</div>
	</form>
</Modal>

<FoldTagModal
	open={folding !== null}
	tag={folding}
	onClose={() => (folding = null)}
	onfolded={() => {
		if (onchanged) onchanged();
		load();
	}}
/>

<Modal
	open={splitting !== null}
	title="Split compound"
	onClose={() => {
		splitting = null;
		splittingBusy = false;
	}}
>
	<form
		onsubmit={(e) => {
			e.preventDefault();
			submitSplit();
		}}
	>
		<div class="space-y-4">
			<p class="text-[0.8125rem] text-parchment-400">
				Replace <b class="text-parchment-300">{splitting?.name}</b> with its components. The
				components are created, the compound's {documentsLabel(splitting?.document_count ?? 0)} are attached
				to them, and the compound is deleted.
			</p>
			<div class="space-y-2">
				{#each splitParts as part, index (index)}
					<div class="flex items-center gap-2">
						<input
							type="text"
							bind:value={part.name}
							placeholder="Component name…"
							autocomplete="off"
							class={inputClass}
							aria-label="Component {index + 1}"
						/>
						<button
							type="button"
							class={iconButton}
							title="Remove this component"
							aria-label="Remove component {index + 1}"
							onclick={() => removeSplitPart(index)}
						>
							<Icon name="minus" size={14} />
						</button>
					</div>
				{/each}
			</div>
			<button type="button" class="{btnGhost} px-0" onclick={addSplitPart}>
				+ Add component
			</button>
			{#if splitError}
				<p class="text-sm text-terracotta-500" aria-live="polite">{splitError}</p>
			{/if}
			<div class="flex justify-end gap-2">
				<button
					type="button"
					onclick={() => (splitting = null)}
					class="rounded-md px-3 py-1.5 text-xs font-medium text-parchment-400 hover:bg-clay-800 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
				>
					Cancel
				</button>
				<button type="submit" disabled={splittingBusy} class={btnPrimary}>
					{splittingBusy ? 'Splitting…' : 'Split'}
				</button>
			</div>
		</div>
	</form>
</Modal>
