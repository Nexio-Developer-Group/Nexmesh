// A scripted OpenAI-compatible model for the Code Agent journeys. Requests that
// carry tools consume the next step; the configuration test (no tools) answers
// "pong". Each step is a tool call or final text.
import http from 'node:http'
export const KEY = 'sk-e2e-agent-0123456789'

export function startScriptedModel(steps, port = 19997) {
  const queue = [...steps]
  const seen = []
  const srv = http.createServer((req, res) => {
    const send = (code, obj) => { res.writeHead(code, { 'Content-Type': 'application/json' }); res.end(JSON.stringify(obj)) }
    if (req.headers.authorization !== `Bearer ${KEY}`) return send(401, { error: { message: 'bad key' } })
    if (req.method === 'GET') return send(200, { data: [{ id: 'scripted-1' }] })
    let body = ''
    req.on('data', (c) => (body += c))
    req.on('end', () => {
      const parsed = JSON.parse(body || '{}')
      if (!parsed.tools || parsed.tools.length === 0) return send(200, { choices: [{ message: { content: 'pong' } }] })
      seen.push(parsed)
      const step = queue.shift() ?? { text: 'Done.' }
      if (step.tool) {
        return send(200, { choices: [{ finish_reason: 'tool_calls', message: { content: step.say ?? '', tool_calls: [{ id: `call_${seen.length}`, type: 'function', function: { name: step.tool, arguments: JSON.stringify(step.args) } }] } }] })
      }
      return send(200, { choices: [{ finish_reason: 'stop', message: { content: step.text } }] })
    })
  })
  return new Promise((resolve) => srv.listen(port, '127.0.0.1', () => resolve({ srv, seen, close: () => srv.close() })))
}
