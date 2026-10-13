// A minimal OpenAI-compatible server for the Code Agent tests: requires KEY.
import http from 'node:http'
export const KEY = 'sk-e2e-0123456789'
export function startFakeModel(port = 19998) {
  const srv = http.createServer((req, res) => {
    const send = (code, obj) => { res.writeHead(code, { 'Content-Type': 'application/json' }); res.end(JSON.stringify(obj)) }
    if (req.headers.authorization !== `Bearer ${KEY}`) return send(401, { error: { message: 'bad key' } })
    if (req.method === 'GET' && req.url.startsWith('/models')) return send(200, { data: [{ id: 'qwen2.5-coder:32b' }] })
    if (req.method === 'POST' && req.url.startsWith('/chat/completions')) { req.resume(); return req.on('end', () => send(200, { choices: [{ message: { content: 'pong' } }] })) }
    send(404, {})
  })
  return new Promise((resolve) => srv.listen(port, '127.0.0.1', () => resolve(srv)))
}
