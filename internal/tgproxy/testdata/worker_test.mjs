// Проверка кода воркера без Cloudflare: поддельные сокет и WebSocket.
//
//	node internal/tgproxy/testdata/worker_test.mjs internal/tgproxy/cf-worker.js
//
// Что проверяется: данные сайта доходят до WebSocket целиком и по порядку, крупными блоками;
// отправленный блок потом не портится (буфер не берётся повторно); сообщения клиента пишутся
// в сокет по порядку, каким бы видом они ни пришли; адрес сайта ищется в DNS один раз.
import { readFileSync, writeFileSync, mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

const source = readFileSync(process.argv[2], "utf8");
const marker = 'import { connect } from "cloudflare:sockets";';
if (!source.includes(marker)) throw new Error("в коде воркера нет строки импорта сокетов");
const dir = mkdtempSync(join(tmpdir(), "fi-worker-"));
const file = join(dir, "worker.mjs");
writeFileSync(file, source.replace(marker, "const { connect } = globalThis.__fiSockets;"));

const fail = (msg) => {
  console.error("ОШИБКА:", msg);
  process.exit(1);
};

// Сайт отдаёт 6 МБ: сперва мелочь (как рукопожатие), потом сплошной поток. Чтение в свой буфер
// получает столько, сколько накопилось в сети, — как у сокета Cloudflare; обычное чтение даёт
// куски по 4 КБ.
const TOTAL = 6 * 1024 * 1024;
const SMALL = 20000; // первые байты приходят порциями по 700
const payload = new Uint8Array(TOTAL);
for (let i = 0; i < TOTAL; i++) payload[i] = (i * 31 + (i >> 8)) & 0xff;
const plainPieces = Math.ceil(TOTAL / 4096);

let written = [];
let socketClosed = 0;
let byobReads = 0;
globalThis.__fiSockets = {
  connect() {
    let off = 0;
    const readable = new ReadableStream({
      type: "bytes",
      async pull(controller) {
        await new Promise((r) => setTimeout(r, 0)); // данные приходят из сети, а не сразу
        const request = controller.byobRequest;
        if (off >= TOTAL) {
          controller.close();
          request?.respond(0);
          return;
        }
        const ready = off < SMALL ? 700 : 1 << 20; // сколько накопилось к этому чтению
        if (request) {
          byobReads++;
          const n = Math.min(request.view.byteLength, ready, TOTAL - off);
          new Uint8Array(request.view.buffer, request.view.byteOffset, n).set(payload.subarray(off, off + n));
          off += n;
          request.respond(n);
        } else {
          const n = Math.min(4096, ready, TOTAL - off);
          controller.enqueue(payload.slice(off, off + n));
          off += n;
        }
      },
    });
    const writable = new WritableStream({
      write(chunk) {
        written.push(Buffer.from(chunk));
      },
    });
    return { readable, writable, closed: new Promise(() => {}), opened: Promise.resolve(), close: async () => void socketClosed++ };
  },
};

let dnsQueries = 0;
globalThis.fetch = async () => {
  dnsQueries++;
  return { json: async () => ({ Answer: [{ type: 1, data: "149.154.167.99" }] }) };
};

class FakeSocket {
  sent = []; // что отправлено: копия на момент отправки и сам переданный блок
  listeners = {};
  closed = null;
  accept() {}
  send(data) {
    const view = data instanceof ArrayBuffer ? new Uint8Array(data) : new Uint8Array(data.buffer, data.byteOffset, data.byteLength);
    this.sent.push({ copy: Buffer.from(view), live: view });
  }
  addEventListener(type, fn) {
    this.listeners[type] = fn;
  }
  close(code, reason) {
    this.closed ??= { code, reason };
  }
}
let server;
globalThis.WebSocketPair = class {
  constructor() {
    server = new FakeSocket();
    this[0] = new FakeSocket();
    this[1] = server;
  }
};
globalThis.Response = class {
  constructor(body, init) {
    Object.assign(this, { body, ...init });
  }
};

const worker = (await import(pathToFileURL(file).href)).default;
const request = (query) => ({
  url: "https://fi.example.workers.dev/?" + query,
  headers: { get: (name) => (name === "Upgrade" ? "websocket" : null) },
});

const resp = await worker.fetch(request("host=telegram.org&port=443"));
if (resp.status !== 101) fail(`ответ ${resp.status}, ждали 101`);

// Сообщения клиента: обычные двоичные, а между ними — вида Blob, разбор которого асинхронный.
const msg = (n) => Uint8Array.from({ length: 5 }, (_, i) => n * 10 + i);
const blob = (bytes) => ({ arrayBuffer: () => new Promise((r) => setTimeout(() => r(bytes.buffer), 5)) });
server.listeners.message({ data: msg(1).buffer });
server.listeners.message({ data: blob(msg(2)) });
server.listeners.message({ data: msg(3).buffer }); // не должно обогнать второе
server.listeners.message({ data: msg(4) });

for (let waited = 0; !server.closed; waited += 10) {
  if (waited > 20000) fail("сайт закрыл соединение, а воркер WebSocket не закрыл");
  await new Promise((r) => setTimeout(r, 10));
}
if (server.closed.code !== 1000) fail(`код закрытия ${server.closed.code} (${server.closed.reason})`);

const got = Buffer.concat(server.sent.map((s) => s.copy));
if (got.length !== TOTAL || Buffer.compare(got, Buffer.from(payload)) !== 0) fail(`до WebSocket дошло ${got.length} байт из ${TOTAL} или они искажены`);
for (const [n, s] of server.sent.entries()) {
  if (Buffer.compare(s.copy, Buffer.from(s.live)) !== 0) fail(`блок ${n} изменился после отправки: его буфер взят повторно`);
}
if (!byobReads) fail("воркер читает сайт обычным способом, кусками по 4 КБ, а не в свой буфер");
if (server.sent.length > plainPieces / 8) fail(`отправок ${server.sent.length}, а кусками по 4 КБ было бы ${plainPieces} — блоки не укрупняются`);

const up = Buffer.concat(written);
const want = Buffer.concat([msg(1), msg(2), msg(3), msg(4)].map((m) => Buffer.from(m)));
if (Buffer.compare(up, want) !== 0) fail(`сообщения клиента записаны не по порядку: ${up.toString("hex")}`);

const sends = server.sent.length;

// Второе соединение к тому же сайту адрес в DNS уже не ищет.
await worker.fetch(request("host=telegram.org&port=443"));
if (dnsQueries !== 1) fail(`запросов DNS ${dnsQueries}, ждали 1: адрес не запомнен`);

console.log(`ок: ${TOTAL} байт сайта за ${sends} отправок (кусками по 4 КБ было бы ${plainPieces}), сообщения клиента по порядку, DNS — один запрос`);
process.exit(0);
