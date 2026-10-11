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
// Telegram и ваш клиент. Самопроверка: https://<адрес воркера>/?selftest=1

import { connect } from "cloudflare:sockets";

const VERSION = 4;

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

const errText = (e) => String(e?.message || e).slice(0, 110);

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

    if (!upgrade) {
      if (url.searchParams.get("selftest")) return text(await selftest(), 200);
      if (!url.searchParams.get("check")) return text(`FI worker: работает (версия ${VERSION})`, 200);
    }

    // Куда подключаться.
    let address;
    let port = Number(url.searchParams.get("port") || "443");
    if (host) {
      if (!isTelegramWeb(host) || port !== 443) {
        return text("можно только сайты Telegram на порту 443", 403);
      }
      address = (await resolveA(host))[0] || host;
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

    server.addEventListener("message", (event) => {
      const data = event.data instanceof ArrayBuffer ? new Uint8Array(event.data) : new TextEncoder().encode(event.data);
      writer.write(data).catch((e) => closeWith(1011, "запись: " + errText(e)));
    });
    server.addEventListener("close", () => socket.close().catch(() => {}));
    server.addEventListener("error", () => socket.close().catch(() => {}));

    socket.readable
      .pipeTo(
        new WritableStream({
          write(chunk) {
            server.send(chunk);
          },
          close() {
            closeWith(1000, "сервер закрыл соединение");
          },
        }),
      )
      .catch((e) => closeWith(1011, "чтение: " + errText(e)));

    return new Response(null, { status: 101, webSocket: client });
  },
};
