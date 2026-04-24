import net from 'net';

const agentId = 'agent_1777010175710_87442b0dcd2e1182';
const payload = {
  jsonrpc: "2.0",
  id: 1,
  method: "thread/config/get",
  params: { threadId: agentId }
};

const client = net.createConnection({ port: 3000 }, () => {
  client.write(JSON.stringify(payload) + '\n');
});

let data = '';
client.on('data', (chunk) => {
  data += chunk;
  try {
    const res = JSON.parse(data);
    console.log(JSON.stringify(res, null, 2));
    client.end();
  } catch (e) {
    // Wait for more data
  }
});
client.on('end', () => {
  console.log('Disconnected');
});
client.on('error', (err) => {
  console.error(err);
});
