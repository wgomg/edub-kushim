export const EDIT_ICON =
	'<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><path d="M17 3a2.85 2.83 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5Z"/></svg>';

export const DELETE_ICON =
	'<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><path d="M3 6h18"/><path d="M19 6v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6"/><path d="M8 6V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2"/></svg>';

export const DOWNLOAD_ICON =
	'<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" x2="12" y1="15" y2="3"/></svg>';

export const RETRY_ICON =
	'<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/><path d="M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16"/><path d="M3 21v-5h5"/></svg>';

export const RESUME_ICON =
	'<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><polygon points="5 3 19 12 5 21 5 3"/></svg>';

export const CANCEL_ICON =
	'<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><path d="m15 9-6 6"/><path d="m9 9 6 6"/></svg>';

export const BTN_BASE = 'rounded-md p-1.5 hover:bg-clay-800';

/**
 * Build a complete <button> HTML string with inline SVG, tooltip, classes, and data-* attributes.
 * @param {string} svg - SVG markup string
 * @param {string} tooltip - `title` attribute value
 * @param {string} extraClasses - additional Tailwind classes (e.g. "text-parchment-400 hover:text-gold-500")
 * @param {Record<string, string>} [dataAttrs] - key-value pairs mapped to data-* attributes; values should be pre-escaped
 * @returns {string} complete <button> HTML string
 */
export function actionButton(svg, tooltip, extraClasses, dataAttrs) {
	const attrs = dataAttrs
		? Object.entries(dataAttrs)
				.map(([k, v]) => `${k}="${v}"`)
				.join(' ')
		: '';
	const cls = `${BTN_BASE} ${extraClasses || ''}`.trim();
	return `<button ${attrs} class="${cls}" title="${tooltip}" aria-label="${tooltip}">${svg}</button>`;
}
