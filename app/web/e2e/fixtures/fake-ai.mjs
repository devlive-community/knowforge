// 端到端测试用的假 OpenAI 兼容服务：对话按系统提示返回固定内容（支持流式与工具调用），嵌入按关键词计数构造向量。
import http from 'node:http'

const PORT = Number(process.env.E2E_AI_PORT || 6990)
const words = ['的', '是', '缓存', '插件', '书', 'a', 'e', 'the']

function answer(req) {
  const msgs = req.messages || []
  const system = (msgs.find((m) => m.role === 'system') || {}).content || ''
  const users = msgs.filter((m) => m.role === 'user')
  const last = users.length ? users[users.length - 1].content || '' : ''
  const toolMsgs = msgs.filter((m) => m.role === 'tool').length
  if (system.includes('内容安全审核员')) {
    return { role: 'assistant', content: JSON.stringify({ verdict: 'safe', confidence: 0.96, categories: [], reason: '正常内容' }) }
  }
  if (system.includes('译者')) {
    if (system.includes('JSON 字符串数组')) {
      try { return { role: 'assistant', content: JSON.stringify(JSON.parse(last).map((x) => '[EN] ' + x)) } } catch { /* 按正文处理 */ }
    }
    return { role: 'assistant', content: '[EN] ' + last }
  }
  if (req.tools && toolMsgs < 1) {
    return { role: 'assistant', content: '', tool_calls: [{ id: 'c1', type: 'function', function: { name: 'search_book', arguments: JSON.stringify({ query: last.slice(0, 40) }) } }] }
  }
  const refs = [...new Set([...(last + msgs.filter((m) => m.role === 'tool').map((m) => m.content || '').join('')).matchAll(/\[(\d+)\]/g)].map((m) => m[1]))]
  const ref = refs[0] || '1'
  return { role: 'assistant', content: `根据书中内容，缓存可以加速读取 [${ref}]。这是端到端测试的模拟回答。` }
}

const server = http.createServer((req, res) => {
  if (req.method === 'GET' && req.url === '/health') {
    res.end('ok')
    return
  }
  let body = ''
  req.on('data', (c) => { body += c })
  req.on('end', () => {
    const payload = body ? JSON.parse(body) : {}
    if (req.url.endsWith('/embeddings')) {
      const input = Array.isArray(payload.input) ? payload.input : [payload.input || '']
      const data = input.map((t, index) => ({ index, embedding: [...words.map((w) => t.split(w).length - 1), 0.1] }))
      res.setHeader('Content-Type', 'application/json')
      res.end(JSON.stringify({ data, usage: { prompt_tokens: 10, total_tokens: 10 } }))
      return
    }
    const msg = answer(payload)
    if (!payload.stream) {
      res.setHeader('Content-Type', 'application/json')
      res.end(JSON.stringify({ model: 'fake', usage: { prompt_tokens: 100, completion_tokens: 20 }, choices: [{ message: msg }] }))
      return
    }
    res.writeHead(200, { 'Content-Type': 'text/event-stream' })
    const send = (obj) => res.write(`data: ${JSON.stringify(obj)}\n\n`)
    const finish = () => {
      send({ choices: [], usage: { prompt_tokens: 100, completion_tokens: 20 } })
      res.end('data: [DONE]\n\n')
    }
    if (msg.tool_calls) {
      send({ model: 'fake', choices: [{ delta: { tool_calls: msg.tool_calls.map((c, index) => ({ ...c, index })) } }] })
      finish()
      return
    }
    const chunks = msg.content.match(/[\s\S]{1,4}/g) || []
    let i = 0
    const tick = () => {
      if (i >= chunks.length) return finish()
      send({ model: 'fake', choices: [{ delta: { content: chunks[i++] } }] })
      setTimeout(tick, 60)
    }
    tick()
  })
})
server.listen(PORT, '127.0.0.1')
