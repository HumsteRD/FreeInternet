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

const VERSION = 3;

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

// IPv4-адрес сайта через DNS Cloudflare: подключение из воркера по имени к сайтам Telegram
// не проходит, по адресу — проходит.
async function resolveA(host) {
  try {
    const r = await fetch(`https://cloudflare-dns.com/dns-query?name=${encodeURIComponent(host)}&type=A`, {
      headers: { accept: "application/dns-json" },
    });
    const j = await r.json();
    return (j.Answer || []).filter((a) => a.type === 1).map((a) => a.data);
  } catch {
    return [];
  }
}

function text(body, status) {
  return new Response(body, { status, headers: { "content-type": "text/plain; charset=utf-8" } });
}

export default {
  async fetch(request) {
    const url = new URL(request.url);
    const host = url.searchParams.get("host") || url.searchParams.get("check");
    const upgrade = request.headers.get("Upgrade") === "websocket";

    if (!upgrade && !url.searchParams.get("check")) {
      return text(`FI worker: работает (версия ${VERSION})`, 200);
    }

    // Куда подключаться.
    let address;
    if (host) {
      if (!isTelegramWeb(host) || (url.searchParams.get("port") || "443") !== "443") {
        return text("можно только сайты Telegram на порту 443", 403);
      }
      address = (await resolveA(host))[0] || host;
    } else {
      const table = url.searchParams.get("test") === "1" ? DC_TEST : DC;
      address = table[Number(url.searchParams.get("dc"))];
      if (!address) {
        return text("неизвестный дата-центр", 400);
      }
    }

    // Проверка из браузера: удаётся ли открыть TCP.
    if (!upgrade) {
      try {
        const socket = connect({ hostname: address, port: 443 });
        await socket.opened;
        socket.close().catch(() => {});
        return text(`${host} → ${address}: подключено\n`, 200);
      } catch (e) {
        return text(`${host} → ${address}: ${e?.message || e}\n`, 200);
      }
    }

    // Сначала WebSocket, потом TCP — в таком порядке пересылка работает.
    const [client, server] = Object.values(new WebSocketPair());
    server.accept();

    const socket = connect({ hostname: address, port: 443 });
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
