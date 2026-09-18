// Cloudflare Worker для FI: принимает WebSocket и перекладывает байты в дата-центр Telegram.
// Нужен там, где адреса Telegram закрыты по IP, а Cloudflare открыт.
//
// Как поставить:
//   1. dash.cloudflare.com → Workers & Pages → Create → Start with Hello World → Deploy.
//   2. Edit code: вставить этот файл целиком, Deploy.
//   3. Домен воркера (вида имя-1234.логин.workers.dev) вписать в FI:
//      Настройки → Telegram → «Путь через Cloudflare».
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

export default {
  async fetch(request) {
    const url = new URL(request.url);
    if (request.headers.get("Upgrade") !== "websocket") {
      return new Response("FI worker: работает", { status: 200 });
    }

    const table = url.searchParams.get("test") === "1" ? DC_TEST : DC;
    const address = table[Number(url.searchParams.get("dc"))];
    if (!address) {
      return new Response("неизвестный дата-центр", { status: 400 });
    }

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
