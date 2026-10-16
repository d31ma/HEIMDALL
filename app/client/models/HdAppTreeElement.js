/** @typedef {import('/models/TreeData.js').TreeData} TreeData */

/**
 * The element as the application page sees it.
 *
 * @typedef {HTMLElement & {
 *   graph: (input: TreeData) => void,
 *   filter: (term?: string) => void,
 * }} HdAppTreeElement
 */
export {}
