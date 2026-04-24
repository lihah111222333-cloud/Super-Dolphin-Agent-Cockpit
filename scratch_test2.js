import http from 'http';

const agentId = 'agent_1777010175710_87442b0dcd2e1182';
const payload = JSON.stringify({
  jsonrpc: "2.0",
  id: 1,
  method: "thread/config/get",
  params: { threadId: agentId }
});

const req = http.request({
  hostname: 'localhost',
  port: 8080,
  path: '/api/rpc',
  method: 'POST',
  headers: {
    'Content-Type': 'application/json',
    'Content-Length': payload.length
  }
}, (res) => {
  let data = '';
  res.on('data', chunk => data += chunk);
  res.on('end', () => console.log('8080:', data));
});
req.on('error', e => console.error('8080:', e.message));
req.write(payload);
req.end();
