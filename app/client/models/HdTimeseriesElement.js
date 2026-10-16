/** @typedef {import('/models/ChartData.js').ChartData} ChartData */

/**
 * The element as a consumer sees it: an HTMLElement that also takes data.
 * A page casts `document.getElementById(...)` to this, because the DOM's own
 * typing stops at HTMLElement.
 *
 * @typedef {HTMLElement & {series: (input: ChartData) => void}} HdTimeseriesElement
 */
export {}
