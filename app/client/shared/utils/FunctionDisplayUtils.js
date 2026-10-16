/** @typedef {import('/models/AppDetail.js').AppDetail} AppDetail */
/** @typedef {import('/models/FunctionDisplayRow.js').FunctionDisplayRow} FunctionDisplayRow */
/** @typedef {import('/models/ServiceDiff.js').ServiceDiff} ServiceDiff */
/** @typedef {import('/models/StatusAnswer.js').StatusAnswer} StatusAnswer */

export default class FunctionDisplayUtils {
  /** @param {AppDetail} detail @returns {FunctionDisplayRow[]} */
  static rows(detail) {
    const services = this.#topology(detail.topology)
    const status = this.#status(detail.status)
    const observations = this.#observations(status.services)
    return this.#rows(services, observations)
  }

  /** @param {FunctionDisplayRow[]} rows @returns {boolean} */
  static empty(rows) { return rows.length === 0 }
  /** @param {FunctionDisplayRow[]} rows @returns {boolean} */
  static invokable(rows) { return this.#invokable(rows) }
  /** @param {FunctionDisplayRow[]} rows @returns {boolean} */
  static #invokable(rows) { return rows.some((row) => row.canInvoke) }

  /** @param {{name:string,function?:{runtime_contract?:string,invocation?:string}}[]} services @param {ServiceDiff[]} observations @returns {FunctionDisplayRow[]} */
  static #rows(services, observations) {
    /** @type {FunctionDisplayRow[]} */
    const rows = []
    for (const service of services) rows.push(...this.#row(service, observations))
    return rows
  }

  /** @param {{name:string,function?:{runtime_contract?:string,invocation?:string}}} service @param {ServiceDiff[]} observations @returns {FunctionDisplayRow[]} */
  static #row(service, observations) {
    const definition = this.#definition(service.function)
    const observed = this.#observed(observations, service.name)
    const row = {
      name: service.name,
      runtime: this.#runtime(definition.runtime_contract),
      version: this.#text(observed.function_version, 'Not observed'),
      health: this.#text(observed.health, 'Unknown'),
      canInvoke: this.#direct(definition),
      message: this.#text(observed.message, ''),
    }
    return this.#include(service, row)
  }

  /** @param {{function?:{runtime_contract?:string,invocation?:string}}} service @param {FunctionDisplayRow} row @returns {FunctionDisplayRow[]} */
  static #include(service, row) { if (!service.function) return []; return [row] }
  /** @param {{name:string,function?:{runtime_contract?:string,invocation?:string}}[]} value @returns {{name:string,function?:{runtime_contract?:string,invocation?:string}}[]} */
  static #topology(value) { if (!Array.isArray(value)) return []; return value }
  /** @param {StatusAnswer} value @returns {StatusAnswer} */
  static #status(value) { if (!value) return {}; return value }
  /** @param {ServiceDiff[]} value @returns {ServiceDiff[]} */
  static #observations(value) { if (!Array.isArray(value)) return []; return value }
  /** @param {{runtime_contract?:string,invocation?:string}} value @returns {{runtime_contract?:string,invocation?:string}} */
  static #definition(value) { if (!value) return {}; return value }
  /** @param {string} value @param {string} fallback @returns {string} */
  static #text(value, fallback) { if (!value) return fallback; return value }
  /** @param {ServiceDiff[]} observations @param {string} name @returns {{function_version?:string,health?:string,message?:string}} */
  static #observed(observations, name) {
    const found = observations.find((observation) => observation.service === name)
    return this.#observation(found)
  }
  /** @param {ServiceDiff} value @returns {{function_version?:string,health?:string,message?:string}} */
  static #observation(value) { if (!value) return {}; return value }
  /** @param {string} contract @returns {string} */
  static #runtime(contract) {
    switch (contract) {
      case 'lambda-runtime-api': return 'AWS Lambda'
      case 'azure-functions-v4': return 'Azure Functions'
      default: return contract
    }
  }
  /** @param {{runtime_contract?:string,invocation?:string}} definition @returns {boolean} */
  static #direct(definition) { return definition.runtime_contract === 'lambda-runtime-api' && definition.invocation === 'json' }
}
