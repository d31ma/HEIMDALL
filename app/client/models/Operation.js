/**
 * @typedef {object} Operation
 * @property {string} [id]
 * @property {string} [phase]
 * @property {string} [started_at]
 * @property {string} [finished_at]
 * @property {string} [revision]
 * @property {string} [message]
 * @property {string} [principal_id]
 * @property {string} [reason_code]
 * @property {boolean} [dry_run]
 * @property {boolean} [rollback]
 * @property {{kind?: string, service?: string, reason?: string}[]} [operations]
 * @property {{kind?: string, service?: string}[]} [applied]
 * @property {Record<string, string>} [failures]
 */
export {}
