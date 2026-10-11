// Cloudflare Worker для FI: принимает WebSocket и перекладывает байты в дата-центр Telegram
// (?dc=N) или на сайт Telegram для мини-приложений (?host=telegram.org&port=443).
// Нужен там, где адреса Telegram закрыты по IP, а Cloudflare открыт.
//
// Как поставить:
//   1. dash.cloudflare.com → Compute → Workers & Pages → Create application →
//      Start with Hello World! → имя (например fi-tg) → Deploy.
//   2. Edit code: стереть шаблон в worker.js, вставить этот файл целиком → Deploy.
//   3. Адрес воркера (fi-tg.имя.workers.dev) вписать в FI без https://:
//      Настройки → Telegram → «Свой Cloudflare Worker» → Сохранить.
//
// Воркер ничего не расшифровывает: внутри соединения идёт MTProto или TLS, ключи знают только
// Telegram и ваш клиент. Проверка: https://<адрес воркера>/?check=telegram.org

import { connect } from "cloudflare:sockets";

const VERSION = 2;

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

// Сайты Telegram, нужные мини-приложениям. К другим адресам воркер не пускает,
// чтобы его нельзя было использовать как открытый прокси.
const WEB = ["telegram.org", "t.me", "telegram.me", "telesco.pe", "cdn-telegram.org", "telegra.ph", "graph.org", "tdesktop.com", "fragment.com"];

function isTelegramWeb(host) {
  host = host.toLowerCase().replace(/\.$/, "");
  return WEB.some((d) => host === d || host.endsWith("." + d));
}

// IPv4-адреса сайта через DNS Cloudflare: подключение по имени из воркера к сайтам Telegram
// не проходит, по адресу — проходит.
async function resolveA(host) {
  const r = await fetch(`https://cloudflare-dns.com/dns-query?name=${encodeURIComponent(host)}&type=A`, {
    headers: { accept: "application/dns-json" },
  });
  const j = await r.json();
  return (j.Answer || []).filter((a) => a.type === 1).map((a) => a.data);
}

// Куда подключаться: адреса дата-центра или сайта Telegram, по порядку.
async function targets(url) {
  const host = url.searchParams.get("host") || url.searchParams.get("check");
  if (host) {
    if (!isTelegramWeb(host) || (url.searchParams.get("port") || "443") !== "443") {
      return { error: "можно только сайты Telegram на порту 443", status: 403 };
    }
    let ips = [];
    try {
      ips = await resolveA(host);
    } catch {}
    return { list: [...ips, host] };
  }
  const table = url.searchParams.get("test") === "1" ? DC_TEST : DC;
  const address = table[Number(url.searchParams.get("dc"))];
  if (!address) {
    return { error: "неизвестный дата-центр", status: 400 };
  }
  return { list: [address] };
}

// Открывает TCP к первому адресу, который ответил.
async function open(list, log) {
  let last = new Error("нет адресов");
  for (const hostname of list) {
    try {
      const socket = connect({ hostname, port: 443 });
      await socket.opened;
      log?.push(`${hostname}: подключено`);
      return socket;
    } catch (e) {
      last = e;
      log?.push(`${hostname}: ${e?.message || e}`);
    }
  }
  throw last;
}

export default {
  async fetch(request) {
    const url = new URL(request.url);
    const t = await targets(url);
    if (t.error) {
      return new Response(t.error, { status: t.status });
    }

    if (request.headers.get("Upgrade") !== "websocket") {
      if (!url.searchParams.get("check")) {
        return new Response(`FI worker: работает (версия ${VERSION})`, { status: 200 });
      }
      const log = [];
      try {
        const socket = await open(t.list, log);
        socket.close().catch(() => {});
      } catch {}
      return new Response(log.join("\n") + "\n", { status: 200, headers: { "content-type": "text/plain; charset=utf-8" } });
    }

    let socket;
    try {
      socket = await open(t.list);
    } catch (e) {
      return new Response("не удалось подключиться: " + (e?.message || e), { status: 502 });
    }

    const [client, server] = Object.values(new WebSocketPair());
    server.accept();
    const writer = socket.writable.getWriter();

    server.addEventListener("message", (event) => {
      const data = event.data instanceof ArrayBuffer ? new Uint8Array(event.data) : event.data;
      writer.write(data).catch(() => server.close());
    });
    server.addEventListener("close", () => socket.close().catch(() => {}));
    server.addEventListener("error", () => socket.close().catch(() => {}));

    (async () => {
      const reader = socket.readable.getReader();
      try {
        for (;;) {
          const { value, done } = await reader.read();
          if (done) break;
          server.send(value);
        }
      } catch {
        // соединение оборвалось — закрываем WebSocket ниже
      }
      try {
        server.close();
      } catch {}
    })();

    return new Response(null, { status: 101, webSocket: client });
  },
};
