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
// Воркер ничего не расшифровывает: внутри соединения идёт MTProto, ключ знают только
// Telegram и ваш клиент.

import { connect } from "cloudflare:sockets";

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

export default {
  async fetch(request) {
    const url = new URL(request.url);
    if (request.headers.get("Upgrade") !== "websocket") {
      return new Response("FI worker: работает", { status: 200 });
    }

    let target;
    const host = url.searchParams.get("host");
    if (host) {
      if (!isTelegramWeb(host) || url.searchParams.get("port") !== "443") {
        return new Response("можно только сайты Telegram на порту 443", { status: 403 });
      }
      target = { hostname: host, port: 443 };
    } else {
      const table = url.searchParams.get("test") === "1" ? DC_TEST : DC;
      const address = table[Number(url.searchParams.get("dc"))];
      if (!address) {
        return new Response("неизвестный дата-центр", { status: 400 });
      }
      target = { hostname: address, port: 443 };
    }

    const [client, server] = Object.values(new WebSocketPair());
    server.accept();

    const socket = connect(target);
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
