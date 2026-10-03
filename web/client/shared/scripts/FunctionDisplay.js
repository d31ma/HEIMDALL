export default class FunctionDisplay {
  static rows(detail) {
    const services = this.#array(detail.topology)
    const status = this.#object(detail.status)
    const observations = this.#array(status.services)
    return this.#rows(services, observations)
  }

  static empty(rows) { return rows.length === 0 }
  static invokable(rows) { return this.#invokable(rows) }
  static #invokable(rows) { return rows.some((row) => row.canInvoke) }

  static #rows(services, observations) {
    const rows = []
    for (const service of services) rows.push(...this.#row(service, observations))
    return rows
  }

  static #row(service, observations) {
    const definition = this.#object(service.function)
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

  static #include(service, row) { if (!service.function) return []; return [row] }
  static #array(value) { if (!Array.isArray(value)) return []; return value }
  static #object(value) { if (!value) return {}; return value }
  static #text(value, fallback) { if (!value) return fallback; return value }
  static #observed(observations, name) {
    const found = observations.find((observation) => observation.service === name)
    return this.#object(found)
  }
  static #runtime(contract) {
    switch (contract) {
      case 'lambda-runtime-api': return 'AWS Lambda'
      case 'azure-functions-v4': return 'Azure Functions'
      default: return contract
    }
  }
  static #direct(definition) { return definition.runtime_contract === 'lambda-runtime-api' && definition.invocation === 'json' }
}
