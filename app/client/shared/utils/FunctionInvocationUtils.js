/** @typedef {import('/models/FunctionInvocationResult.js').FunctionInvocationResult} FunctionInvocationResult */
/** @typedef {import('/models/FunctionInvocationState.js').FunctionInvocationState} FunctionInvocationState */

export default class FunctionInvocationUtils {
  /** @returns {number} */
  static get payloadBytes() { return 256 * 1024 }

  /** @param {{invokeFunction:(project:string,app:string,service:string,payload:unknown)=>Promise<FunctionInvocationResult>}} api @param {{project:string, app:string, service:string, text:string}} request @returns {Promise<FunctionInvocationState>} */
  static async send(api, request) {
    const parsed = this.#parse(request.text)
    const bytes = new TextEncoder().encode(request.text).length
    const problem = this.#problem(parsed, bytes, request.service)
    const perform = () => this.#invoke(api, request, parsed.payload)
    const action = this.#action(problem, perform)
    return await action()
  }

  /** @param {string} text @returns {{valid:boolean,payload:unknown}} */
  static #parse(text) {
    try { return { valid: true, payload: JSON.parse(text) } }
    catch { return { valid: false, payload: null } }
  }
  /** @param {{valid:boolean,payload:unknown}} parsed @param {number} bytes @param {string} service @returns {string} */
  static #problem(parsed, bytes, service) {
    switch (true) {
      case !service: return 'Choose a function.'
      case bytes > this.payloadBytes: return 'Payload must be within 256 KiB.'
      case !parsed.valid: return 'Payload must be valid JSON.'
      default: return ''
    }
  }
  /** @param {string} problem @param {() => Promise<FunctionInvocationState>} perform @returns {() => Promise<FunctionInvocationState>} */
  static #action(problem, perform) {
    if (problem) return async () => ({ error: problem, output: '', version: '', requestID: '', outcome: '' })
    return perform
  }
  /** @param {{invokeFunction:(project:string,app:string,service:string,payload:unknown)=>Promise<FunctionInvocationResult>}} api @param {{project:string,app:string,service:string}} request @param {unknown} payload @returns {Promise<FunctionInvocationState>} */
  static async #invoke(api, request, payload) {
    try {
      const response = await api.invokeFunction(request.project, request.app, request.service, payload)
      return this.#result(response)
    } catch (error) {
      return { error: String(error).replace(/^Error: /, ''), output: '', version: '', requestID: '', outcome: '' }
    }
  }
  /** @param {FunctionInvocationResult} response @returns {FunctionInvocationState} */
  static #result(response) {
    return { error: '', output: JSON.stringify(response.payload, null, 2), version: response.version, requestID: response.request_id, outcome: this.#outcome(response.function_error) }
  }
  /** @param {string} error @returns {string} */
  static #outcome(error) { if (error) return `Function error: ${error}`; return 'Completed' }
}
