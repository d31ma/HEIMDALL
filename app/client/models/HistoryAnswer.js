/** @typedef {import('/models/ActivityEntry.js').ActivityEntry} ActivityEntry */
/** @typedef {import('/models/Operation.js').Operation} Operation */
/** @typedef {import('/models/RevisionSummary.js').RevisionSummary} RevisionSummary */

/**
 * @typedef {object} HistoryAnswer
 * @property {ActivityEntry[]} [activity] The paged index — the Activity table's source.
 * @property {Operation[]} [operations] The same page as whole operation documents.
 * @property {RevisionSummary[]} [revisions]
 * @property {string} [next_cursor] Empty on the last page.
 * @property {string} [revisions_cursor]
 * @property {boolean} [truncated]
 */
export {}
