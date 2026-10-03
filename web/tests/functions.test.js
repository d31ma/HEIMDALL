import test from 'node:test'
import assert from 'node:assert/strict'
import FunctionDisplay from '../client/shared/scripts/FunctionDisplay.js'
import FunctionInvocation from '../client/shared/scripts/FunctionInvocation.js'

const detail = {
  topology: [
    { name: 'web', replicas: 1 },
    { name: 'handler', function: { runtime_contract: 'lambda-runtime-api', invocation: 'json' } },
    { name: 'http', function: { runtime_contract: 'azure-functions-v4', invocation: 'http' } },
  ],
  status: { services: [{ service: 'handler', health: 'Suspended', function_version: '7' }] },
}

test('functions show actual deployment versions without inventing execution inventory', () => {
  const rows = FunctionDisplay.rows(detail)
  assert.equal(rows.length, 2)
  assert.deepEqual(rows[0], { name: 'handler', runtime: 'AWS Lambda', version: '7', health: 'Suspended', canInvoke: true, message: '' })
  assert.equal(rows[1].canInvoke, false)
  assert.equal(rows[1].version, 'Not observed')
  assert.equal(rows[1].health, 'Unknown')
  assert.equal(FunctionDisplay.rows({}).length, 0)
  assert.equal(FunctionDisplay.invokable(rows), true)
  assert.equal(FunctionDisplay.invokable([rows[1]]), false)
})

test('invalid JSON and UTF-8 byte limits refuse before the invocation call', async () => {
  let calls = 0
  const api = { invokeFunction() { calls++; throw new Error('must not call') } }
  for (const text of ['{', `"${'😀'.repeat(FunctionInvocation.payloadBytes / 3)}"`]) {
    const result = await FunctionInvocation.send(api, { project: 'alpha', app: 'events', service: 'handler', text })
    assert.notEqual(result.error, '')
  }
  const missing = await FunctionInvocation.send(api, { project: 'alpha', app: 'events', service: '', text: '{}' })
  assert.match(missing.error, /Choose/)
  assert.equal(calls, 0)
})

test('an invocation sends one request and preserves version, payload and execution errors', async () => {
  const calls = []
  const api = { async invokeFunction(...input) { calls.push(input); return { request_id: 'request-local', version: '7', payload: { failure: 'handler result' }, function_error: 'Unhandled' } } }
  const result = await FunctionInvocation.send(api, { project: 'alpha', app: 'events', service: 'handler', text: '{"value":"input"}' })
  assert.deepEqual(calls, [['alpha', 'events', 'handler', { value: 'input' }]])
  assert.equal(result.version, '7')
  assert.equal(result.requestID, 'request-local')
  assert.equal(result.outcome, 'Function error: Unhandled')
  assert.match(result.output, /handler result/)
  assert.equal(result.error, '')
})

test('an ambiguous transport error is surfaced without a retry', async () => {
  let calls = 0
  const api = { async invokeFunction() { calls++; throw new Error('wait ended; execution may continue') } }
  const result = await FunctionInvocation.send(api, { project: 'alpha', app: 'events', service: 'handler', text: 'null' })
  assert.equal(calls, 1)
  assert.match(result.error, /execution may continue/)
  assert.equal(result.output, '')
})
