// Проверка кода воркера без Cloudflare: поддельные сокет и WebSocket.
//
//	node internal/tgproxy/testdata/worker_test.mjs internal/tgproxy/cf-worker.js
//
// Что проверяется:
//   - данные сайта доходят до WebSocket целиком и по порядку, а вызовов «прочитал — отправил»
//     заметно меньше, чем кусков по 4 КБ, которыми читает Cloudflare;
//   - воркер не придерживает данные: когда сайт замолкает на границе записи TLS, всё присланное
//     уже отправлено (иначе соединение повисло бы навсегда);
//   - поток не TLS и поток дата-центра Telegram пересылаются по мере прихода;
//   - отправленный блок потом не портится (буфер не берётся повторно, пока блок в очереди);
//   - сообщения клиента пишутся в сокет по порядку, каким бы видом они ни пришли;
//   - адрес сайта ищется в DNS один раз;
//   - без ключа FI открыты только сайты Telegram на порту 443, с ключом — любой сайт и порт.
import { readFileSync, writeFileSync, mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

const source = readFileSync(process.argv[2], "utf8");
const marker = 'import { connect } from "cloudflare:sockets";';
if (!source.includes(marker)) throw new Error("в коде воркера нет строки импорта сокетов");
const keyLine = 'const KEY = "";';
if (!source.includes(keyLine)) throw new Error("в коде воркера нет строки с ключом");
const dir = mkdtempSync(join(tmpdir(), "fi-worker-"));
const load = async (name, key) => {
  const file = join(dir, name);
  writeFileSync(file, source.replace(marker, "const { connect } = globalThis.__fiSockets;").replace(keyLine, `const KEY = "${key}";`));
  return (await import(pathToFileURL(file).href)).default;
};

const fail = (msg) => {
  console.error("ОШИБКА:", msg);
  process.exit(1);
};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// В Cloudflare у читателя есть readAtLeast; в Node то же самое делает read с параметром min.
ReadableStreamBYOBReader.prototype.readAtLeast = function (min, view) {
  return this.read(view, { min });
};

// ─── Поддельные сокет и WebSocket ───────────────────────────────────────

const FRAME = 4096; // столько Cloudflare отдаёт за одно чтение

// site описывает, что отдаёт «сайт»: data — поток, gates — места, где сайт замолкает и ждёт,
// пока всё присланное до этого места дойдёт до WebSocket (как сервер ждёт ответа клиента).
let site = { data: new Uint8Array(0), gates: [] };
let connects = [];
let written = [];
let byobReads = 0;
let server;

globalThis.__fiSockets = {
  connect(address) {
    connects.push(`${address.hostname}:${address.port}`);
    const { data, gates } = site;
    let off = 0;
    let gate = 0;
    let pulls = 0;
    const readable = new ReadableStream({
      type: "bytes",
      async pull(controller) {
        // Данные приходят из сети, а не сразу; настоящая пауза — изредка, иначе тест идёт минуту.
        if (pulls++ % 32 === 0) await sleep(0);
        else await null;
        if (gate < gates.length && off >= gates[gate]) {
          for (let waited = 0; received() < gates[gate]; waited += 5) {
            if (waited > 5000) fail(`воркер придержал данные: сайт прислал ${gates[gate]} байт и ждёт, а до WebSocket дошло ${received()}`);
            await sleep(5);
          }
          gate++;
        }
        const request = controller.byobRequest;
        if (off >= data.length) {
          controller.close();
          request?.respond(0);
          return;
        }
        const limit = gate < gates.length ? gates[gate] : data.length; // до места, где сайт замолкнет
        if (request) {
          byobReads++;
          const n = Math.min(request.view.byteLength, FRAME, limit - off);
          new Uint8Array(request.view.buffer, request.view.byteOffset, n).set(data.subarray(off, off + n));
          off += n;
          request.respond(n);
        } else {
          const n = Math.min(FRAME, limit - off);
          controller.enqueue(data.slice(off, off + n));
          off += n;
        }
      },
    });
    const writable = new WritableStream({
      write(chunk) {
        written.push(Buffer.from(chunk));
      },
    });
    return { readable, writable, closed: new Promise(() => {}), opened: Promise.resolve(), close: async () => {} };
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
const received = () => server.sent.reduce((n, s) => n + s.copy.length, 0);

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

const request = (query, upgrade = true) => ({
  url: "https://fi.example.workers.dev/?" + query,
  headers: { get: (name) => (name === "Upgrade" && upgrade ? "websocket" : null) },
});

// run открывает соединение через воркер и ждёт, пока «сайт» отдаст всё и закроется.
async function run(worker, query, data, gates = []) {
  site = { data, gates };
  written = [];
  const resp = await worker.fetch(request(query));
  if (resp.status !== 101) fail(`${query}: ответ ${resp.status}, ждали 101`);
  const ws = server;
  for (let waited = 0; !ws.closed; waited += 10) {
    if (waited > 20000) fail(`${query}: сайт закрыл соединение, а воркер WebSocket не закрыл`);
    await sleep(10);
  }
  if (ws.closed.code !== 1000) fail(`${query}: код закрытия ${ws.closed.code} (${ws.closed.reason})`);
  const got = Buffer.concat(ws.sent.map((s) => s.copy));
  if (got.length !== data.length || Buffer.compare(got, Buffer.from(data)) !== 0) fail(`${query}: до WebSocket дошло ${got.length} байт из ${data.length} или они искажены`);
  for (const [n, s] of ws.sent.entries()) {
    if (Buffer.compare(s.copy, Buffer.from(s.live)) !== 0) fail(`${query}: блок ${n} изменился после отправки — его буфер взят повторно`);
  }
  return ws.sent.length;
}

const pattern = (n, seed) => Uint8Array.from({ length: n }, (_, i) => (i * 31 + (i >> 8) + seed) & 0xff);

// record — запись TLS: вид, версия 3.3, длина и тело.
const record = (type, size, seed) => {
  const rec = new Uint8Array(5 + size);
  rec.set([type, 3, 3, size >> 8, size & 0xff]);
  rec.set(pattern(size, seed), 5);
  return rec;
};
const join2 = (parts) => {
  const out = new Uint8Array(parts.reduce((n, p) => n + p.length, 0));
  let off = 0;
  for (const p of parts) {
    out.set(p, off);
    off += p.length;
  }
  return out;
};

const worker = await load("worker.mjs", "");

// ─── 1. Поток TLS: рукопожатие, большая загрузка, короткий хвост ─────────

const handshake = [record(22, 1200, 1), record(20, 1, 2), record(23, 3000, 3)];
const bulk = Array.from({ length: 300 }, (_, i) => record(23, 16384, i));
const tail = [record(23, 900, 7)];
const tls = join2([...handshake, ...bulk, ...tail]);
const afterHandshake = join2(handshake).length;
const afterBulk = afterHandshake + join2(bulk).length;
// Сайт замолкает после рукопожатия, после загрузки и после хвоста: к этим местам всё должно дойти.
const sends = await run(worker, "host=telegram.org&port=443", tls, [afterHandshake, afterBulk, tls.length]);
const frames = Math.ceil(tls.length / FRAME);
if (!byobReads) fail("воркер читает сайт обычным способом, кусками по 4 КБ, а не в свой буфер");
if (sends > frames / 1.8) fail(`поток TLS: отправок ${sends}, а кусков по 4 КБ ${frames} — записи не читаются целиком`);
// p=0 — простая пересылка: тот же поток уходит по мере прихода, как в прежнем коде. Так FI
// просит, если чтение записями в настоящем Cloudflare не сработало.
const plainSends = await run(worker, "host=telegram.org&port=443&p=0", tls, [afterHandshake, afterBulk, tls.length]);
if (plainSends < frames * 0.9) fail(`простая пересылка (p=0): отправок ${plainSends}, а кусков по 4 КБ ${frames} — записи всё равно читаются целиком`);

// ─── 2. Сообщения клиента пишутся по порядку ────────────────────────────

site = { data: record(23, 100, 1), gates: [] };
written = [];
await worker.fetch(request("host=telegram.org&port=443"));
const msg = (n) => Uint8Array.from({ length: 5 }, (_, i) => n * 10 + i);
const blob = (bytes) => ({ arrayBuffer: () => new Promise((r) => setTimeout(() => r(bytes.buffer), 5)) });
server.listeners.message({ data: msg(1).buffer });
server.listeners.message({ data: blob(msg(2)) }); // вид Blob: разбор асинхронный
server.listeners.message({ data: msg(3).buffer }); // не должно обогнать второе
server.listeners.message({ data: msg(4) });
await sleep(100);
const up = Buffer.concat(written);
const want = Buffer.concat([msg(1), msg(2), msg(3), msg(4)].map((m) => Buffer.from(m)));
if (Buffer.compare(up, want) !== 0) fail(`сообщения клиента записаны не по порядку: ${up.toString("hex")}`);
if (dnsQueries !== 1) fail(`запросов DNS ${dnsQueries}, ждали 1: адрес сайта не запомнен`);

// ─── 3. Не TLS и дата-центр Telegram: по мере прихода ───────────────────

// Сайт на порту 443 отвечает не по TLS и замолкает на полуслове — хвост всё равно должен дойти.
const plain = pattern(50000, 9);
plain[0] = 0x48; // «H»: не запись TLS
await run(worker, "host=telegram.org&port=443", plain, [1234, 20000, plain.length]);
// Первые байты похожи на заголовок TLS, но длина записи невозможная — тоже не TLS.
const fake = pattern(40000, 4);
fake.set([23, 3, 3, 0xff, 0xff]);
await run(worker, "host=telegram.org&port=443", fake, [300, fake.length]);
// Дата-центр Telegram: случайные на вид байты, даже если начало похоже на TLS.
const mtproto = pattern(30000, 5);
mtproto.set([23, 3, 3, 0x10, 0x00]);
await run(worker, "dc=2", mtproto, [700, mtproto.length]);

// ─── 4. Кого воркер пускает ─────────────────────────────────────────────

const status = async (w, query) => (await w.fetch(request(query))).status;
site = { data: new Uint8Array(0), gates: [] };
if ((await status(worker, "host=example.org&port=443")) !== 403) fail("без ключа открыт сайт не Telegram");
if ((await status(worker, "host=telegram.org&port=8443")) !== 403) fail("без ключа открыт порт не 443");
const refused = await worker.fetch(request("host=telegram.org&port=8443"));
if (!String(refused.body).includes("порт 443")) fail(`отказ в порту должен называть порт 443 — по этим словам FI подсказывает обновить воркер: ${refused.body}`);
if ((await status(worker, "host=example.org&port=443&key=")) !== 403) fail("пустой ключ принят за верный");

const keyed = await load("worker-keyed.mjs", "секрет");
connects = [];
for (const [query, code] of [
  ["host=example.org&port=443&key=секрет", 101],
  ["host=example.org&port=8443&key=секрет", 101],
  ["host=93.184.216.34&port=5222&key=секрет", 101],
  ["host=example.org&port=25&key=секрет", 403],
  ["host=example.org&port=0&key=секрет", 403],
  ["host=example.org&port=443&key=чужой", 403],
  ["host=bad host&port=443&key=секрет", 403],
]) {
  const got = await status(keyed, encodeURI(query));
  if (got !== code) fail(`${query}: ответ ${got}, ждали ${code}`);
}
if (!connects.includes("93.184.216.34:5222")) fail(`с ключом воркер должен открывать и адрес с любым портом: ${connects}`);
await sleep(50);

console.log(`ок: поток TLS ${tls.length} байт за ${sends} отправок (кусков по 4 КБ — ${frames}), данные не придерживаются, не TLS и Telegram — по мере прихода, порядок сообщений, DNS и доступ в порядке`);
process.exit(0);
