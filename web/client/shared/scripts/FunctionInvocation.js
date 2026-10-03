export default class FunctionInvocation {
  static payloadBytes = 256 * 1024

  static async send(api, request) {
    const parsed = this.#parse(request.text)
    const bytes = new TextEncoder().encode(request.text).length
    const problem = this.#problem(parsed, bytes, request.service)
    const perform = () => this.#invoke(api, request, parsed.payload)
    const action = this.#action(problem, perform)
    return await action()
  }

  static #parse(text) {
    try { return { valid: true, payload: JSON.parse(text) } }
    catch { return { valid: false, payload: null } }
  }
  static #problem(parsed, bytes, service) {
    switch (true) {
      case !service: return 'Choose a function.'
      case bytes > this.payloadBytes: return 'Payload must be within 256 KiB.'
      case !parsed.valid: return 'Payload must be valid JSON.'
      default: return ''
    }
  }
  static #action(problem, perform) {
    if (problem) return async () => ({ error: problem, output: '', version: '', requestID: '', outcome: '' })
    return perform
  }
  static async #invoke(api, request, payload) {
    try {
      const response = await api.invokeFunction(request.project, request.app, request.service, payload)
      return this.#result(response)
    } catch (error) {
      return { error: String(error).replace(/^Error: /, ''), output: '', version: '', requestID: '', outcome: '' }
    }
  }
  static #result(response) {
    return { error: '', output: JSON.stringify(response.payload, null, 2), version: response.version, requestID: response.request_id, outcome: this.#outcome(response.function_error) }
  }
  static #outcome(error) { if (error) return `Function error: ${error}`; return 'Completed' }
}
