// 端到端测试用的假 OpenAI 兼容服务：对话按系统提示返回固定内容（支持流式与工具调用），嵌入按关键词计数构造向量，
// 语音合成返回一段 0.3 秒的提示音（MP3）。
import http from 'node:http'

const PORT = Number(process.env.E2E_AI_PORT || 6990)
// 0.3 秒 440Hz 提示音（ffmpeg 生成的 MP3）
const TONE = Buffer.from('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', 'base64')
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
    if (req.url.endsWith('/audio/speech')) {
      res.setHeader('Content-Type', 'audio/mpeg')
      res.end(TONE)
      return
    }
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
