// Cloudflare Worker для FI: принимает WebSocket и перекладывает байты в дата-центр Telegram
// (?dc=N), на сайт Telegram для мини-приложений (?host=telegram.org&port=443) или — с ключом
// FI — на любой сайт: закрытые для России самими сервисами (SoundCloud и другие), видеосерверы
// YouTube, закрытые провайдером, и адреса программ, запущенных из FI через Cloudflare.
// Нужен там, где адреса Telegram закрыты по IP, а Cloudflare открыт.
//
// Как поставить:
//   1. dash.cloudflare.com → Compute → Workers & Pages → Create application →
//      Start with Hello World! → имя (например fi-tg) → Deploy.
//   2. Edit code: стереть шаблон в worker.js, вставить код целиком → Deploy. Код с ключом
//      копирует сам FI: Настройки → Cloudflare → «Ручная настройка» → «Скопировать код».
//   3. Адрес воркера (fi-tg.имя.workers.dev) вписать в FI без https://:
//      Настройки → Telegram → «Свой Cloudflare Worker» → Сохранить.
//
// Воркер ничего не расшифровывает: внутри соединения идёт MTProto или TLS, ключи знают только
// Telegram (сайт) и ваш клиент. Самопроверка: https://<адрес воркера>/?selftest=1

import { connect } from "cloudflare:sockets";

const VERSION = 8;

// KEY — секрет FI: с ним воркер пускает к любым сайтам, без него — только к Telegram, чтобы
// воркером не воспользовались как открытым прокси. FI вписывает его сам при копировании кода.
const KEY = "";

// Адреса дата-центров Telegram.
const DC = {
  1: "149.154.175.50",
  2: "149.154.167.51",
  3: "149.154.175.100",
  4: "149.154.167.91",
  5: "149.154.171.5",
  203: "91.105.192.100",
};

// Тестовые дата-центры Telegram (FI просит их параметром test=1).
const DC_TEST = {
  1: "149.154.175.10",
  2: "149.154.167.40",
  3: "149.154.175.117",
};

// Порты, на которых дата-центры Telegram принимают MTProto.
const DC_PORTS = [443, 80, 5222];

// Сайты Telegram, нужные мини-приложениям. К другим адресам воркер не пускает,
// чтобы его нельзя было использовать как открытый прокси.
const WEB = ["telegram.org", "t.me", "telegram.me", "telesco.pe", "cdn-telegram.org", "telegra.ph", "graph.org", "tdesktop.com", "fragment.com"];

function isTelegramWeb(host) {
  host = host.toLowerCase().replace(/\.$/, "");
  return WEB.some((d) => host === d || host.endsWith("." + d));
}

// Адреса сайтов, которые воркер уже искал: браузер открывает к одному видеосерверу много
// соединений подряд, и каждое заново спрашивало DNS — лишние десятки миллисекунд.
const DNS_TTL = 5 * 60 * 1000;
const dnsCache = new Map();

// IPv4-адрес сайта через DNS Cloudflare: подключение из воркера по имени к сайтам Telegram
// не проходит, по адресу — проходит.
async function resolveA(host) {
  const hit = dnsCache.get(host);
  if (hit && hit.until > Date.now()) return hit.ips;
  try {
    const r = await fetch(`https://cloudflare-dns.com/dns-query?name=${encodeURIComponent(host)}&type=A`, {
      headers: { accept: "application/dns-json" },
    });
    const j = await r.json();
    const ips = (j.Answer || []).filter((a) => a.type === 1).map((a) => a.data);
    if (ips.length) {
      if (dnsCache.size > 300) dnsCache.clear();
      dnsCache.set(host, { ips, until: Date.now() + DNS_TTL });
    }
    return ips;
  } catch {
    return [];
  }
}

function text(body, status) {
  return new Response(body, { status, headers: { "content-type": "text/plain; charset=utf-8" } });
}

const errText = (e) => String(e?.message || e).slice(0, 110);

// bytes — содержимое сообщения WebSocket как байты. Двоичное сообщение приходит не обязательно
// ArrayBuffer'ом: прежние версии воркера писали в сокет не те байты и соединение рвалось.
async function bytes(data) {
  if (data instanceof ArrayBuffer) return new Uint8Array(data);
  if (ArrayBuffer.isView(data)) return new Uint8Array(data.buffer, data.byteOffset, data.byteLength);
  if (data && typeof data.arrayBuffer === "function") return new Uint8Array(await data.arrayBuffer());
  if (typeof data === "string") return new TextEncoder().encode(data);
  throw new Error("сообщение неизвестного вида: " + Object.prototype.toString.call(data));
}

// Пересылка данных сайта. Бесплатный воркер ограничен процессорным временем, и Cloudflare
// обрывает соединение, когда оно кончается, — без объяснений, посреди передачи. Время уходит на
// каждый вызов «прочитал — отправил», а читает Cloudflare кусочками по 4 КБ: на быстрой загрузке
// (видео, файлы) это тысячи вызовов в секунду. Чем меньше вызовов на мегабайт, тем дольше живёт
// соединение.
//
// Копить данные впрок нельзя: сайт может замолчать в ожидании ответа, и недосланный хвост
// повис бы навсегда. Но поток почти всегда — TLS, а запись TLS (до 16 КБ) получателю всё равно
// не нужна по частям, и отправитель шлёт её целиком. Поэтому воркер читает запись до конца одним
// вызовом: её длина стоит в открытом заголовке. Ждёт он только того, что сайт уже начал слать.
const BLOCK = 256 * 1024;
const TLS_HEADER = 5;
const TLS_RECORD_MAX = 16384 + 2048; // больше в записи TLS не бывает

// readSome читает с сайта не меньше min байт — или сколько есть, если поток кончился.
function readSome(reader, min, view) {
  if (min > 1 && typeof reader.readAtLeast === "function") return reader.readAtLeast(min, view);
  return reader.read(view);
}

// pump пересылает данные сайта в WebSocket, пока сайт не закроет соединение. tls — поток стоит
// разбирать как записи TLS; если это окажется не TLS, воркер читает по мере прихода.
async function pump(socket, server, tls) {
  let reader;
  try {
    reader = socket.readable.getReader({ mode: "byob" });
  } catch {
    // Поток не умеет читать в свой буфер — пересылаем по-старому, кусочками.
    await socket.readable.pipeTo(
      new WritableStream({
        write(chunk) {
          server.send(chunk);
        },
      }),
    );
    return;
  }
  let buffer = new ArrayBuffer(BLOCK);
  let need = 0; // сколько байт осталось до конца текущей записи TLS
  let head = []; // байты заголовка записи, прочитанного не целиком
  for (;;) {
    const min = tls ? Math.max(1, Math.min(need, BLOCK)) : 1;
    const { value, done } = await readSome(reader, min, new Uint8Array(buffer));
    const n = value ? value.byteLength : 0;
    if (n > 0) {
      for (let i = 0; tls && i < n; ) {
        if (need > 0) {
          const k = Math.min(need, n - i);
          need -= k;
          i += k;
          continue;
        }
        head.push(value[i++]);
        if (head.length < TLS_HEADER) continue;
        need = (head[3] << 8) | head[4];
        if (head[0] < 20 || head[0] > 23 || head[1] !== 3 || need > TLS_RECORD_MAX) {
          tls = false; // не TLS: дальше — по мере прихода
          need = 0;
          break;
        }
        head = [];
      }
      if (n < BLOCK / 4) {
        // Немного данных: отправляем копию, а буфер берём снова.
        server.send(value.slice());
        buffer = value.buffer;
      } else {
        // Много данных: отправляем сам блок, для следующего чтения нужен новый буфер.
        server.send(value);
        buffer = new ArrayBuffer(BLOCK);
      }
    } else if (value) {
      buffer = value.buffer;
    }
    if (done) return;
  }
}

const describe = (d) =>
  `${Object.prototype.toString.call(d)} ${d?.constructor?.name ?? ""} ${d?.byteLength ?? d?.size ?? d?.length ?? "?"}`;

// Самопроверка: что из воркера доходит до Telegram и где рвётся.
async function selftest() {
  const lines = [`FI worker версии ${VERSION}`];
  const step = async (name, fn) => {
    const start = Date.now();
    try {
      lines.push(`${name}: ${await fn()} (${Date.now() - start} мс)`);
    } catch (e) {
      lines.push(`${name}: ОШИБКА ${errText(e)} (${Date.now() - start} мс)`);
    }
  };
  const readSome = async (socket, ms) => {
    const reader = socket.readable.getReader();
    const timeout = new Promise((r) => setTimeout(() => r("timeout"), ms));
    const res = await Promise.race([reader.read(), timeout]);
    if (res === "timeout") return "соединение живо, данных нет";
    if (res.done) return "соединение закрыто сервером";
    return new TextDecoder().decode(res.value.slice(0, 40)).split("\r\n")[0];
  };
  const ip = (await resolveA("telegram.org"))[0] || "149.154.167.99";
  await step(`HTTP telegram.org (${ip}:80)`, async () => {
    const s = connect({ hostname: ip, port: 80 });
    const w = s.writable.getWriter();
    await w.write(new TextEncoder().encode("GET / HTTP/1.1\r\nHost: telegram.org\r\nConnection: close\r\n\r\n"));
    const r = await readSome(s, 4000);
    s.close().catch(() => {});
    return r;
  });
  await step("TLS telegram.org:443 силами воркера", async () => {
    const s = connect({ hostname: "telegram.org", port: 443 }, { secureTransport: "on" });
    const w = s.writable.getWriter();
    await w.write(new TextEncoder().encode("GET / HTTP/1.1\r\nHost: telegram.org\r\nConnection: close\r\n\r\n"));
    const r = await readSome(s, 4000);
    s.close().catch(() => {});
    return r;
  });
  for (const port of DC_PORTS) {
    await step(`DC2 ${DC[2]}:${port}`, async () => {
      const s = connect({ hostname: DC[2], port });
      await s.opened;
      const w = s.writable.getWriter();
      await w.write(crypto.getRandomValues(new Uint8Array(64))); // заголовок MTProto: сервер ждёт продолжения
      const r = await readSome(s, 2500);
      s.close().catch(() => {});
      return r;
    });
  }
  return lines.join("\n") + "\n";
}

export default {
  async fetch(request) {
    const url = new URL(request.url);
    const host = url.searchParams.get("host") || url.searchParams.get("check");
    const upgrade = request.headers.get("Upgrade") === "websocket";
    const keyed = KEY !== "" && url.searchParams.get("key") === KEY;

    if (!upgrade) {
      if (url.searchParams.get("selftest")) return text(await selftest(), 200);
      if (url.searchParams.get("keycheck")) return text(keyed ? "ключ верный" : "ключ неверный", keyed ? 200 : 403);
      if (!url.searchParams.get("check")) return text(`FI worker: работает (версия ${VERSION}${KEY ? ", с ключом" : ""})`, 200);
    }

    // Отладка: в каком виде воркер получает сообщения WebSocket.
    if (upgrade && url.searchParams.get("echo")) {
      const [client, server] = Object.values(new WebSocketPair());
      server.accept();
      server.addEventListener("message", (event) => server.send("получено: " + describe(event.data)));
      return new Response(null, { status: 101, webSocket: client });
    }

    // Куда подключаться.
    let address;
    let port = Number(url.searchParams.get("port") || "443");
    if (host) {
      // Без ключа — только сайты Telegram на порту 443. С ключом FI — любой сайт и порт: так
      // ходят программы, запущенные из FI через Cloudflare. Почту (25) Cloudflare не открывает.
      if (!keyed && port !== 443) return text("без ключа FI можно только порт 443", 403);
      if (!isTelegramWeb(host) && !keyed) {
        return text("без ключа FI можно только сайты Telegram — скопируйте код воркера из FI", 403);
      }
      if (!Number.isInteger(port) || port < 1 || port > 65535 || port === 25) return text("такой порт открыть нельзя", 403);
      const byAddress = /^[0-9.]+$/.test(host);
      if (!/^[a-z0-9.-]+$/i.test(host) || (byAddress && !keyed)) return text("нужно имя сайта, а не адрес", 403);
      address = byAddress ? host : (await resolveA(host))[0] || host;
    } else {
      const table = url.searchParams.get("test") === "1" ? DC_TEST : DC;
      address = table[Number(url.searchParams.get("dc"))];
      if (!address) return text("неизвестный дата-центр", 400);
      if (!DC_PORTS.includes(port)) return text("порт дата-центра — 443, 80 или 5222", 400);
    }

    // Проверка из браузера: удаётся ли открыть TCP.
    if (!upgrade) {
      try {
        const socket = connect({ hostname: address, port });
        await socket.opened;
        socket.close().catch(() => {});
        return text(`${host} → ${address}:${port}: подключено\n`, 200);
      } catch (e) {
        return text(`${host} → ${address}:${port}: ${errText(e)}\n`, 200);
      }
    }

    // Сначала WebSocket, потом TCP — в таком порядке пересылка работает. При обрыве причина
    // уходит в кадре закрытия: её видно в журнале FI.
    const [client, server] = Object.values(new WebSocketPair());
    server.accept();
    const closeWith = (code, reason) => {
      try {
        server.close(code, reason.slice(0, 120));
      } catch {}
    };

    const socket = connect({ hostname: address, port });
    const writer = socket.writable.getWriter();
    socket.closed.catch((e) => closeWith(1011, "tcp: " + errText(e)));

    // Сообщения пишутся в сокет строго по очереди. Обычное двоичное сообщение пишется сразу;
    // очередь нужна только сообщениям другого вида, разбор которых асинхронный.
    let queue = null;
    const failed = (e) => closeWith(1011, "запись: " + errText(e));
    server.addEventListener("message", (event) => {
      const data = event.data;
      if (queue === null && data instanceof ArrayBuffer) {
        writer.write(new Uint8Array(data)).catch(failed);
        return;
      }
      const next = (queue ?? Promise.resolve()).then(async () => writer.write(await bytes(data))).catch(failed);
      queue = next;
      next.then(() => {
        if (queue === next) queue = null;
      });
    });
    server.addEventListener("close", () => socket.close().catch(() => {}));
    server.addEventListener("error", () => socket.close().catch(() => {}));

    // Сайты на порту 443 говорят по TLS; дата-центры Telegram и программы на других портах —
    // по своим протоколам, их по записям не разобрать. p=0 — пересылать по мере прихода и TLS:
    // так просит FI, если проверка показала, что чтение записями в этом воркере не работает.
    const plain = url.searchParams.get("p") === "0";
    pump(socket, server, !plain && Boolean(host) && port === 443).then(
      () => closeWith(1000, "сервер закрыл соединение"),
      (e) => closeWith(1011, "чтение: " + errText(e)),
    );

    return new Response(null, { status: 101, webSocket: client });
  },
};
