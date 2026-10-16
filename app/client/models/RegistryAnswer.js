/** @typedef {import('/models/Operation.js').Operation} Operation */
/** @typedef {import('/models/RegistryBinding.js').RegistryBinding} RegistryBinding */

/**
 * @typedef {object} RegistryAnswer
 * @property {boolean} [bound]
 * @property {RegistryBinding} [binding]
 * @property {Operation[]} [syncs] Recent registry_sync operations.
 */
export {}
