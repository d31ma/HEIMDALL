/** @typedef {import('/models/ChartLine.js').ChartLine} ChartLine */
/** @typedef {import('/models/ChartMarker.js').ChartMarker} ChartMarker */
/** @typedef {import('/models/ChartPoint.js').ChartPoint} ChartPoint */

/**
 * @typedef {object} ChartData
 * @property {string} [label] Announced in the SVG's aria-label.
 * @property {''|'bytes'|'percent'} [unit]
 * @property {ChartPoint[]} [points] A single series.
 * @property {ChartLine[]} [lines] Two or more series on one axis; wins over points.
 * @property {ChartMarker[]} [markers]
 * @property {number} [max] Floor for the y axis, so a flat line is not full height.
 * @property {boolean} [caption] False hides the figcaption when a panel has its own title.
 */
export {}
