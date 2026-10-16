/** @typedef {import('/models/ServiceDiff.js').ServiceDiff} ServiceDiff */

/**
 * @typedef {object} StatusAnswer
 * @property {string} [sync_status]
 * @property {string} [health]
 * @property {string} [desired_revision]
 * @property {string} [live_revision]
 * @property {string} [target]
 * @property {ServiceDiff[]} [services]
 */
export {}
