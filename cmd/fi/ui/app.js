// Окно FI: показывает состояние службы и передаёт ей команды.
// Запросы идут в /api/<метод>: Go-часть окна пересылает их службе по named pipe.

const ICONS = {
  plus: '<path d="M12 5v14"/><path d="M5 12h14"/>',
  pulse: '<path d="M3 12h4l2.5-6 5 12 2.5-6h4"/>',
  refresh: '<path d="M19.5 10A8 8 0 0 0 5.2 7.2"/><path d="M4.5 4v4h4"/><path d="M4.5 14a8 8 0 0 0 14.3 2.8"/><path d="M19.5 20v-4h-4"/>',
  sliders: '<path d="M4 7h9"/><path d="M17 7h3"/><path d="M4 17h3"/><path d="M11 17h9"/><circle cx="15" cy="7" r="2"/><circle cx="9" cy="17" r="2"/>',
  check: '<path d="M5 12.5l4.5 4.5L19 7.5"/>',
  clock: '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3.5 2"/>',
  play: '<rect x="3" y="5" width="18" height="14" rx="3.5"/><path d="M10 9.3v5.4l4.6-2.7z"/>',
  headset: '<path d="M4 15.5V12a8 8 0 0 1 16 0v3.5"/><rect x="3" y="14" width="4" height="6" rx="1.5"/><rect x="17" y="14" width="4" height="6" rx="1.5"/>',
  bubble: '<path d="M5 4.5h14A1.5 1.5 0 0 1 20.5 6v9a1.5 1.5 0 0 1-1.5 1.5h-9l-5 4v-4A1.5 1.5 0 0 1 3.5 15V6A1.5 1.5 0 0 1 5 4.5z"/>',
  phone: '<path d="M5.5 3.5h3l2 5-2.3 1.4a11 11 0 0 0 5.9 5.9l1.4-2.3 5 2v3A2 2 0 0 1 18.5 20.5 16 16 0 0 1 3.5 5.5a2 2 0 0 1 2-2z"/>',
  globe: '<circle cx="12" cy="12" r="9"/><path d="M3 12h18"/><path d="M12 3c2.4 2.6 3.7 5.6 3.7 9s-1.3 6.4-3.7 9c-2.4-2.6-3.7-5.6-3.7-9s1.3-6.4 3.7-9z"/>',
  monitor: '<rect x="3" y="4.5" width="18" height="12" rx="2"/><path d="M8.5 20h7"/><path d="M12 16.5V20"/>',
  clipboard: '<rect x="5.5" y="5" width="13" height="16" rx="2"/><rect x="9" y="3" width="6" height="4" rx="1.2"/>',
  trash: '<path d="M4.5 7h15"/><path d="M9.5 7V4.5h5V7"/><path d="M6.5 7l.9 12.5h9.2L17.5 7"/>',
  chevronLeft: '<path d="M14.5 6l-6 6 6 6"/>',
  spinner: '<path d="M12 3.5a8.5 8.5 0 1 0 8.5 8.5"/>',
};

const icon = (name, size = 20, sw = 1.75, cls = '') =>
  `<svg class="${cls}" width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="${sw}" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${ICONS[name]}</svg>`;
const spinner = (size = 15) => icon('spinner', size, 2, 'spin');

const mark = (size) =>
  `<svg width="${size}" height="${size}" viewBox="0 0 32 32" aria-hidden="true"><rect width="32" height="32" rx="7" fill="#0E0C11"/><rect x=".5" y=".5" width="31" height="31" rx="6.5" fill="none" stroke="#fff" stroke-opacity=".1"/><path d="M7 7h12v4h-8v3h6v4h-6v7H7z" fill="#F1EEF3"/><rect x="22" y="7" width="4" height="18" fill="#E5484D"/></svg>`;

const esc = (value) =>
  String(value ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]);

// «general (ALT4)» → «ALT4»: в наборе Flowseal все стратегии называются general (…).
const strategyLabel = (name) => String(name ?? '').replace(/^general\s*\((.+)\)$/i, '$1');

const plural = (n, one, few, many) => {
  const m10 = n % 10, m100 = n % 100;
  if (m10 === 1 && m100 !== 11) return one;
  if (m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14)) return few;
  return many;
};

// ─── Связь со службой ─────────────────────────────────────────────────────

class ApiError extends Error {
  constructor(message, code) {
    super(message);
    this.code = code;
  }
}

async function api(method, params = {}) {
  let res;
  try {
    res = await fetch(`/api/${method}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(params),
    });
  } catch {
    throw new ApiError('Окно потеряло связь со своей частью на Go', 'transport');
  }
  const data = await res.json().catch(() => ({}));
  if (!res.ok || data.error) throw new ApiError(data.error || `Ошибка ${res.status}`, data.code);
  return data.result;
}

const post = (path, body) =>
  fetch(path, {
    method: 'POST',
    headers: body ? { 'Content-Type': 'application/json' } : undefined,
    body: body ? JSON.stringify(body) : undefined,
  }).catch(() => null);

const postJSON = async (path, body) => {
  const res = await post(path, body);
  return res && res.ok ? res.json().catch(() => null) : null;
};

// ─── Состояние ────────────────────────────────────────────────────────────

const state = {
  screen: 'main', // main, site, diagnose, settings
  status: null,
  unavailable: false,
  message: '',
  site: { input: '', busy: false, result: null, error: '' },
  diagnose: { service: '', checking: false, report: null, fixing: '' }, // report — что мешает обходу на компьютере
  autostart: null, // запуск окна вместе с Windows; null — ещё не узнали
  copied: false, // ссылка прокси Telegram только что скопирована
  worker: null, // домен Cloudflare Worker, пока его правят в поле; null — показываем сохранённый
  tgClients: null, // клиенты Telegram на компьютере (Telegram, AyuGram…); null — ещё не искали
  notice: '', // что получилось: «Отчёт сохранён…»; сбрасывается при переходе между экранами
};

const TITLES = { youtube: 'YouTube', discord: 'Discord', telegram: 'Telegram', calls: 'Звонки', sites: 'Сайты' };

const SERVICE_META = {
  youtube: { icon: 'play', sub: 'Сайт и видео', hint: 'Видео, превью' },
  discord: { icon: 'headset', sub: 'Сайт, вход и файлы', hint: 'Вход, сообщения' },
  telegram: { icon: 'bubble', sub: 'Сайт и веб-клиент', hint: 'Сообщения, медиа' },
  calls: { icon: 'phone', sub: 'Голос в Discord и звонки', hint: 'Голос, видеосвязь' },
  sites: { icon: 'globe', sub: '', hint: 'Любой адрес' },
};

const STATE_BADGE = {
  ok: ['ok', 'Работает'],
  slow: ['warn', 'Замедлено'],
  partial: ['warn', 'Частично'],
  fail: ['fail', 'Не работает'],
  unknown: ['off', 'Нет связи'],
  empty: ['off', 'Пусто'],
  pending: ['check', 'Проверка'],
};

const VERDICT_BADGE = {
  fixed: ['fixed', 'Исправлено'],
  works: ['ok', 'Открывается'],
  added: ['ok', 'Добавлен'],
  not_fixed: ['fail', 'Не помогло'],
  dns: ['warn', 'Нужен DNS'],
  tunnel: ['warn', 'Нужен туннель'],
  not_found: ['fail', 'Не найден'],
  unreachable: ['fail', 'Не добавлен'],
  cert: ['fail', 'Не добавлен'],
  unknown: ['fail', 'Не добавлен'],
};

// ─── Компоненты ───────────────────────────────────────────────────────────

function badge(kind, label) {
  const lead = kind === 'check' ? spinner(13) : kind === 'fixed' ? icon('check', 13, 2) : '<span class="dot"></span>';
  return `<span class="badge ${kind === 'fixed' ? 'ok' : kind}">${lead}<span>${esc(label)}</span></span>`;
}

function progress(done, total) {
  const pct = total > 0 ? Math.round((done / total) * 100) : 0;
  return `<div class="progress" role="progressbar" aria-valuemin="0" aria-valuemax="${total}" aria-valuenow="${done}"><span style="width: ${pct}%"></span></div>`;
}

const subHeader = (title) =>
  `<div class="subheader"><button class="icon-btn" data-action="back" aria-label="Назад">${icon('chevronLeft', 18)}</button><h1>${esc(title)}</h1></div>`;

const errorLine = () => (state.message ? `<p class="field-error" title="${esc(state.message)}">${esc(state.message)}</p>` : '');
const noticeLine = () => (state.notice ? `<p class="notice-line">${icon('check', 14, 2)}<span>${esc(state.notice)}</span></p>` : '');

function baseLine(base = {}) {
  if (base.error) return base.error;
  let text = base.version ? `Flowseal ${base.version}` : 'Версия неизвестна';
  if (base.update_available && base.latest) text += ` · доступна ${base.latest}`;
  else if (base.checked_at) text += ' · последняя версия';
  return text;
}

function appLine(app = {}) {
  if (app.error) return app.error;
  const text = app.version ? `FI ${app.version}` : 'Версия неизвестна';
  if (!app.configured) return `${text} · автообновление не настроено в этой сборке`;
  if (app.update_available && app.latest) return `${text} · доступна ${app.latest}`;
  return app.checked_at ? `${text} · последняя версия` : text;
}

function appButton(st) {
  if (!st.app?.configured) return '';
  return st.app.update_available
    ? `<button class="btn sm primary" data-action="app-update" ${st.task ? 'disabled' : ''}>Обновить</button>`
    : `<button class="btn sm ghost" data-action="app-check" ${st.task ? 'disabled' : ''}>Проверить</button>`;
}

function checkedText(iso) {
  if (!iso) return 'Ещё не проверялось';
  const at = new Date(iso);
  const minutes = Math.round((Date.now() - at.getTime()) / 60000);
  if (minutes < 1) return 'Проверено только что';
  if (minutes < 60) return `Проверено ${minutes} мин назад`;
  return `Проверено в ${at.toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit' })}`;
}

// ─── Экраны ───────────────────────────────────────────────────────────────

function view() {
  const st = state.status;
  if (state.unavailable) return unavailableView();
  if (!st) return `<div class="loading">${spinner(20)}</div>`;
  if (!st.strategy && state.screen === 'main') return setupView(st);
  switch (state.screen) {
    case 'site':
      return siteView(st);
    case 'diagnose':
      return diagnoseView(st);
    case 'settings':
      return settingsView(st);
    default:
      return mainView(st);
  }
}

function heroView(st) {
  const task = st.task;
  let title;
  let sub;
  let taskRow = '';
  if (task?.kind === 'select') {
    const count = Math.max(0, task.total - 1);
    title = task.quick ? 'Быстрый подбор стратегии' : 'Подбираем стратегию';
    sub = task.done > 0 ? `Стратегия ${task.done} из ${count} · <span class="mono">${esc(strategyLabel(task.current))}</span>` : 'Проверяем сеть без обхода';
    taskRow = `<div class="hero-task">${progress(task.done, task.total)}<button class="btn ghost sm" data-action="cancel">Отменить</button></div>`;
  } else if (task?.kind === 'app') {
    title = 'Обновляем FI';
    sub = esc(task.current || '');
    taskRow = `<div class="hero-task">${progress(task.done, task.total)}</div>`;
  } else if (task?.kind === 'base') {
    title = 'Обновляем набор стратегий';
    sub = esc(task.current || '');
    taskRow = `<div class="hero-task">${progress(task.done, task.total)}</div>`;
  } else if (st.error) {
    title = st.enabled ? 'Обход не работает' : 'Обход выключен';
    sub = esc(st.error);
    if (st.conflict?.length) {
      taskRow = `<div class="hero-task"><button class="btn sm primary" data-action="conflict">Что мешает и как исправить</button></div>`;
    }
  } else if (st.enabled) {
    title = 'Обход работает';
    sub = `Стратегия <span class="mono">${esc(strategyLabel(st.strategy))}</span>`;
  } else {
    title = 'Обход выключен';
    sub = 'Сервисы открываются без обхода';
  }
  return `<section class="panel hero-panel">
    <div class="hero">
      ${mark(44)}
      <div class="hero-text"><span class="hero-title">${title}</span><span class="hero-sub" title="${st.error ? esc(st.error) : ''}">${sub}</span></div>
      <button class="toggle ${st.enabled ? 'on' : ''}" role="switch" aria-checked="${st.enabled}" aria-label="Обход" data-action="toggle" ${task?.kind === 'select' ? 'disabled' : ''}></button>
    </div>
    ${taskRow}
  </section>`;
}

// vpnSelectNotice — предупреждение на экране подбора: через VPN все стратегии выглядят рабочими.
const vpnSelectNotice = (st) =>
  st.vpn
    ? `<section class="panel notice warn">${icon('globe', 16)}<span>Выключите VPN (${esc(st.vpn)}), чтобы подобрать стратегию. Через него все стратегии выглядят рабочими, и подбор выбрал бы наугад — а без VPN такая стратегия может сломать интернет.</span></section>`
    : '';

// selectNote — почему подбор прерван или почему стратегия сменилась сама.
const selectNote = (st) =>
  st.select_note ? `<section class="panel notice">${icon('pulse', 16)}<span>${esc(st.select_note)}</span></section>` : '';

// routeText — каким путём прокси ходит в Telegram: «через Cloudflare», «напрямую».
const routeText = (route) => (route === 'напрямую' ? route : `через ${route}`);

function serviceRow(service, st) {
  const meta = SERVICE_META[service.id] ?? { icon: 'globe', sub: '' };
  let sub = meta.sub;
  if (service.id === 'sites') {
    const n = (st.sites ?? []).filter((s) => !s.parent).length;
    sub = n ? `${n} в списке` : 'Своих сайтов пока нет';
  }
  if (service.detail && service.state !== 'ok') sub = service.detail;
  let [kind, label] = STATE_BADGE[service.state] ?? STATE_BADGE.unknown;
  let trailing = '';

  // Для Telegram важнее всего, пользуется ли клиент Telegram прокси.
  const tg = st.telegram;
  if (service.id === 'telegram' && tg?.enabled) {
    if (tg.error) {
      sub = tg.error;
      [kind, label] = STATE_BADGE.fail;
    } else if (!tg.total) {
      sub = tg.bad_secret ? 'В Telegram сохранён старый прокси FI — подключите заново' : 'Подключите Telegram или AyuGram к прокси';
      trailing = '<button class="btn sm primary" data-action="tg-connect">Подключить</button>';
    } else if (service.state === 'ok' || service.state === 'partial') {
      const via = tg.route ? routeText(tg.route) : '';
      const count = tg.active ? `${tg.active} ${plural(tg.active, 'соединение', 'соединения', 'соединений')}` : '';
      sub = [via && via[0].toUpperCase() + via.slice(1), count].filter(Boolean).join(' · ') || 'Прокси подключён';
      [kind, label] = ['proxy', 'Через прокси'];
    }
  }

  // Причину поломки показываем целиком: в одну строку она не помещается и обрывается на полуслове.
  const subClass = (service.detail && service.state !== 'ok') || service.id === 'telegram' ? 'row-sub wrap' : 'row-sub';
  return `<div class="row">
    <span class="tile">${icon(meta.icon)}</span>
    <span class="row-text"><span class="row-title">${esc(service.title)}</span><span class="${subClass}" title="${esc(sub)}">${esc(sub)}</span></span>
    ${trailing || badge(kind, label)}
  </div>`;
}

function mainView(st) {
  const services = st.services?.length
    ? st.services
    : Object.keys(TITLES).map((id) => ({ id, title: TITLES[id], state: 'pending' }));
  const checking = st.task?.kind === 'check';
  const vpn = st.vpn
    ? `<section class="panel notice">${icon('globe', 16)}<span>Интернет идёт через VPN (${esc(st.vpn)}): статусы показывают сеть VPN, а не провайдера, и автоподбор пока не запускается.</span></section>`
    : '';
  return `${heroView(st)}
    ${errorLine()}
    ${vpn}
    ${selectNote(st)}
    <section class="stack">
      <div class="section-label"><span>Сервисы</span></div>
      <div class="panel">${services.map((s) => serviceRow(s, st)).join('')}</div>
    </section>
    <div class="grid-2">
      <button class="btn" data-action="open" data-screen="diagnose">${icon('pulse', 17)}<span>Не работает?</span></button>
      <button class="btn" data-action="open" data-screen="site">${icon('plus', 17)}<span>Добавить сайт</span></button>
    </div>
    <footer class="footer">
      <span class="meta">${checking ? `${spinner(14)}<span>Проверяем сервисы…</span>` : `${icon('clock', 14)}<span>${esc(checkedText(st.checked_at))}</span>`}</span>
      <span class="footer-actions">
        <button class="icon-btn" data-action="check" title="Проверить сейчас" aria-label="Проверить сейчас" ${st.task ? 'disabled' : ''}>${icon('refresh', 18)}</button>
        <button class="icon-btn" data-action="open" data-screen="settings" title="Настройки" aria-label="Настройки">${icon('sliders', 18)}</button>
      </span>
    </footer>`;
}

function setupView(st) {
  if (!st.strategies) {
    const task = st.task?.kind === 'base' ? st.task : null;
    const error = st.base?.error;
    return `<section class="stack onboarding">
        <span class="eyebrow">Первый запуск</span>
        <h1 class="display">Скачаем набор стратегий</h1>
        <p class="lead">FI берёт стратегии обхода из проекта zapret-discord-youtube. Архив весит около 1,5 МБ, его подлинность проверяется по опубликованному хешу.</p>
      </section>
      ${task ? `<section class="stack"><div class="progress-head"><span>${esc(task.current || 'Готовимся')}</span></div>${progress(task.done, task.total)}</section>` : ''}
      ${error && !task ? `<p class="field-error">${esc(error)}</p>` : ''}
      <div class="setup-actions">
        <button class="btn primary full" data-action="base-update" ${st.task ? 'disabled' : ''}>${task ? `${spinner()}<span>Скачиваем…</span>` : `<span>${error ? 'Попробовать снова' : 'Скачать набор'}</span>`}</button>
        <p class="hint center">Или из своей папки: <span class="mono">fi-service import-base</span></p>
      </div>`;
  }

  const n = st.strategies;
  const intro = (verb) => `<section class="stack onboarding">
      <span class="eyebrow">Первый запуск</span>
      <h1 class="display">${verb} способ обхода для вашей сети</h1>
      <p class="lead">Проверим ${n} ${plural(n, 'стратегию', 'стратегии', 'стратегий')} на настоящих сайтах. Это займёт 3–5 минут, интернет может ненадолго прерываться.</p>
    </section>`;

  const task = st.task?.kind === 'select' ? st.task : null;
  if (!task) {
    // Фоновая проверка сервисов не мешает: служба прервёт её ради подбора.
    const busy = (st.task && st.task.kind !== 'check') || st.vpn;
    return `${intro('Подберём')}
      ${vpnSelectNotice(st)}${selectNote(st)}
      ${errorLine()}
      <div class="setup-actions"><button class="btn primary full" data-action="select" ${busy ? 'disabled' : ''}>Начать подбор</button></div>`;
  }
  return `${intro('Подбираем')}
    <section class="stack">
      <div class="progress-head">
        <span>${task.done === 0 ? 'Проверяем сеть без обхода' : `Стратегия ${task.done} из ${n}`}</span>
        <span class="mono muted">${task.done ? esc(strategyLabel(task.current)) : ''}</span>
      </div>
      ${progress(task.done, task.total)}
    </section>
    <div class="setup-actions">
      <button class="btn full" data-action="hide">Свернуть в трей</button>
      <button class="btn ghost full" data-action="cancel">Отменить</button>
      <p class="hint center">Окно можно закрыть — подбор продолжится</p>
    </div>`;
}

function siteView(st) {
  const s = state.site;
  const sites = st.sites ?? [];
  const result = s.result
    ? (() => {
        const [kind, label] = VERDICT_BADGE[s.result.verdict] ?? VERDICT_BADGE.unknown;
        // Главная страница открывается, а музыка или видео на сайте — нет: такой сайт можно добавить вручную.
        const force =
          s.result.verdict === 'works'
            ? `<p class="result-text">Если на сайте не играет музыка или не грузится видео, добавьте его всё равно — вместе с доменами, с которых он их берёт.</p>
               <button class="btn sm primary" data-action="force-site" ${s.busy || st.task ? 'disabled' : ''}>Всё равно добавить</button>`
            : '';
        return `<section class="panel result">
          <div class="result-head"><span class="mono strong">${esc(s.result.host)}</span>${badge(kind, label)}</div>
          <p class="result-text">${esc(s.result.message)}</p>
          ${force}
        </section>`;
      })()
    : '';
  // Домены, добавленные вместе с сайтом, показываем под ним: удаляются они тоже вместе.
  const main = sites.filter((site) => !site.parent || !sites.some((p) => p.host === site.parent));
  const rows = main
    .map((site) => {
      const related = sites.filter((c) => c.parent === site.host).map((c) => c.host);
      const subs = [
        site.note ? `<span class="row-sub">Было: ${esc(site.note)}</span>` : '',
        related.length ? `<span class="row-sub wrap">Вместе с ним: <span class="mono">${esc(related.join(', '))}</span></span>` : '',
      ].join('');
      return `<div class="row site-row">
        <span class="row-text"><span class="mono">${esc(site.host)}</span>${subs}</span>
        <button class="icon-btn" data-action="remove-site" data-host="${esc(site.host)}" title="Убрать из списка" aria-label="Убрать ${esc(site.host)} из списка">${icon('trash', 16)}</button>
      </div>`;
    })
    .join('');

  return `${subHeader('Добавить сайт')}
    <form class="stack" data-form="site" novalidate>
      <label class="input ${s.error ? 'is-error' : ''}">
        ${icon('globe', 16)}
        <input id="site-input" name="host" autocomplete="off" spellcheck="false" placeholder="Адрес сайта, например x.com" value="${esc(s.input)}" ${s.busy ? 'disabled' : ''}>
        <button type="button" class="btn ghost sm" data-action="paste" ${s.busy ? 'disabled' : ''}>${icon('clipboard', 15)}<span>Вставить</span></button>
      </label>
      ${s.error ? `<p class="field-error">${esc(s.error)}</p>` : ''}
      <button class="btn primary full" type="submit" ${s.busy || st.task ? 'disabled' : ''}>${s.busy ? `${spinner()}<span>Проверяем сайт…</span>` : '<span>Проверить и добавить</span>'}</button>
    </form>
    ${result}
    <section class="stack">
      <div class="section-label"><span>Ваши сайты</span><span>${main.length || ''}</span></div>
      <div class="panel">${rows || '<p class="empty">Пока пусто. Добавьте сайт, который не открывается.</p>'}</div>
      ${errorLine()}
      ${noticeLine()}
      <div class="button-row">
        <button class="btn sm" data-action="sites-export" ${sites.length ? '' : 'disabled'}>Сохранить в файл</button>
        <button class="btn sm ghost" data-action="sites-import">Загрузить из файла</button>
      </div>
    </section>
    <p class="hint">Можно и из трея: скопируйте адрес и выберите «Добавить сайт из буфера».</p>`;
}

const LEVEL_ORDER = { fail: 0, warn: 1, info: 2, ok: 3 };
const LEVEL_BADGE = { fail: ['fail', 'Мешает'], warn: ['warn', 'Может мешать'], info: ['off', 'Совет'] };

const problemsOf = (report) =>
  (report?.findings ?? []).filter((f) => f.level !== 'ok').sort((a, b) => LEVEL_ORDER[a.level] - LEVEL_ORDER[b.level]);

function findingItem(f, fixing) {
  const [kind, label] = LEVEL_BADGE[f.level] ?? LEVEL_BADGE.info;
  const fix = f.fix
    ? `<div class="finding-actions"><button class="btn sm primary" data-action="fix" data-fix="${esc(f.fix)}" ${fixing ? 'disabled' : ''}>${
        fixing === f.fix ? `${spinner(14)}<span>Исправляем…</span>` : `<span>${esc(f.fix_label || 'Исправить')}</span>`
      }</button></div>`
    : '';
  return `<div class="finding">
      <div class="result-head"><span class="row-title-sm">${esc(f.title)}</span>${badge(kind, label)}</div>
      ${f.detail ? `<p class="finding-detail mono">${esc(f.detail)}</p>` : ''}
      ${f.advice ? `<p class="result-text">${esc(f.advice)}</p>` : ''}
      ${fix}
    </div>`;
}

function computerView(d) {
  if (d.checking) {
    return `${subHeader('Компьютер · проверка')}
      <section class="panel notice">${spinner()}<span>Ищем, что мешает обходу…</span></section>`;
  }
  const retry = `<div class="setup-actions"><button class="btn ghost full" data-action="diagnose" data-service="computer">Проверить ещё раз</button></div>`;
  if (!d.report) return `${subHeader('Компьютер · результат')}${errorLine()}${retry}`;

  const problems = problemsOf(d.report);
  const passed = d.report.findings.filter((f) => f.level === 'ok');
  const serious = problems[0] && problems[0].level !== 'info';
  const [kind, label] = serious ? LEVEL_BADGE[problems[0].level] : ['ok', 'Не мешает'];
  const text = serious
    ? 'Нашли, что может мешать обходу. Начните с верхнего пункта, а после исправления проверьте сервис ещё раз.'
    : 'На компьютере нет того, что обычно мешает обходу. Если сервис всё равно не работает, проверьте его отдельно.';
  return `${subHeader('Компьютер · результат')}
    <section class="panel result">
      <div class="result-head"><span class="row-title">Компьютер</span>${badge(kind, label)}</div>
      <p class="result-text">${text}</p>
    </section>
    ${errorLine()}
    ${problems.length ? `<div class="panel">${problems.map((f) => findingItem(f, d.fixing)).join('')}</div>` : ''}
    ${
      passed.length
        ? `<section class="stack">
        <div class="section-label"><span>Без замечаний</span><span>${passed.length}</span></div>
        <div class="panel">${passed.map((f) => `<div class="check-row">${icon('check', 14, 2)}<span>${esc(f.title)}</span></div>`).join('')}</div>
      </section>`
        : ''
    }
    ${retry}`;
}

function diagnoseView(st) {
  const d = state.diagnose;
  if (!d.service) {
    const options = Object.keys(TITLES)
      .map((id) => {
        const action = id === 'sites' ? 'data-action="open" data-screen="site"' : `data-action="diagnose" data-service="${id}"`;
        return `<button class="option" ${action}>${icon(SERVICE_META[id].icon)}<span class="option-text"><span class="row-title-sm">${TITLES[id]}</span><span class="option-hint">${SERVICE_META[id].hint}</span></span></button>`;
      })
      .join('');
    const computer = `<button class="option" data-action="diagnose" data-service="computer">${icon('monitor')}<span class="option-text"><span class="row-title-sm">Компьютер</span><span class="option-hint">Что мешает обходу</span></span></button>`;
    return `${subHeader('Что не работает?')}
      <div class="grid-2">${options}${computer}</div>
      <p class="hint">Проверим сервис при текущем обходе и подскажем, что делать.</p>`;
  }
  if (d.service === 'computer') return computerView(d);

  const title = TITLES[d.service];
  const service = st.services?.find((s) => s.id === d.service);
  if (d.checking || !service) {
    return `${subHeader(`${title} · проверка`)}
      <section class="panel notice">${spinner()}<span>Проверяем ${esc(title)} при текущем обходе…</span></section>
      ${errorLine()}`;
  }

  const tg = st.telegram;
  if (d.service === 'telegram' && tg?.enabled && !tg.error && !tg.total) {
    return `${subHeader(`${title} · результат`)}
      <section class="panel result">
        <div class="result-head"><span class="row-title">Telegram</span>${badge('warn', 'Не подключён')}</div>
        <p class="result-text">${tg.bad_secret ? 'В Telegram сохранён прокси FI со старым ключом, поэтому соединения отклоняются.' : 'Telegram ещё не пользуется прокси FI.'} Нажмите кнопку с вашей программой (Telegram, AyuGram…): она откроется и предложит включить прокси — согласитесь. Если вашей программы нет среди кнопок, укажите её файл сами.</p>
      </section>
      ${errorLine()}
      <div class="setup-actions">${tgClientButtons(true)}</div>`;
  }

  const [kind, label] = STATE_BADGE[service.state] ?? STATE_BADGE.unknown;
  let explanation;
  if (service.state === 'ok') {
    explanation = 'Сейчас все проверки проходят. Если в приложении всё равно не работает, перезапустите его или подберите стратегию заново.';
  } else if (service.state === 'unknown') {
    explanation = 'Не открывается даже контрольный сайт — похоже, пропал интернет. Проверьте подключение.';
  } else {
    explanation = `Причина: ${service.detail || 'часть проверок не прошла'}. Подбор проверит все стратегии и выберет ту, при которой сервис работает.`;
  }
  const primary =
    service.state === 'ok' || service.state === 'unknown'
      ? ''
      : `<button class="btn primary full" data-action="select" ${st.task || st.vpn ? 'disabled' : ''}>Подобрать стратегию заново</button>`;
  const hindrances = problemsOf(d.report).filter((f) => f.level !== 'info');

  return `${subHeader(`${title} · результат`)}
    <section class="panel result">
      <div class="result-head"><span class="row-title">${esc(title)}</span>${badge(kind, label)}</div>
      <p class="result-text">${esc(explanation)}</p>
      ${service.total ? `<p class="result-meta">Проверок пройдено: ${service.passed} из ${service.total}</p>` : ''}
    </section>
    ${errorLine()}
    ${
      hindrances.length
        ? `<section class="stack">
        <div class="section-label"><span>Что может мешать</span></div>
        <div class="panel">${hindrances.map((f) => findingItem(f, d.fixing)).join('')}</div>
      </section>`
        : ''
    }
    <div class="setup-actions">
      ${primary}
      <button class="btn ghost full" data-action="diagnose" data-service="${d.service}">Проверить ещё раз</button>
    </div>`;
}

function gameChips(st) {
  const selected = st.games ?? [];
  const chips = (st.game_profiles ?? [])
    .map((g) => {
      const on = selected.includes(g.id);
      const ports = [g.tcp && `TCP ${g.tcp}`, g.udp && `UDP ${g.udp}`].filter(Boolean).join(' · ');
      return `<button class="chip ${on ? 'on' : ''}" role="checkbox" aria-checked="${on}" data-action="game" data-game="${esc(g.id)}" title="${esc(ports)}">${esc(g.title)}</button>`;
    })
    .join('');
  const empty = selected.length ? '' : '<span class="row-sub wrap">Выберите игры — пока фильтр ничего не перехватывает.</span>';
  return `<div class="chips">${chips}</div>${empty}`;
}

// tgClientList — найденные клиенты Telegram; выбранный в прошлый раз идёт первым.
function tgClientList() {
  const preferred = (state.tgClients?.preferred ?? '').toLowerCase();
  const first = (c) => (c.path.toLowerCase() === preferred ? 0 : 1);
  return [...(state.tgClients?.clients ?? [])].sort((a, b) => first(a) - first(b));
}

// tgClientButtons — «Подключить в AyuGram» для каждого найденного клиента, выбор другой программы
// и копирование ссылки. full — крупные кнопки во всю ширину для экрана диагностики.
function tgClientButtons(full = false) {
  const clients = tgClientList();
  const size = full ? 'full' : 'sm';
  const minor = full ? 'ghost' : '';
  const buttons = clients.map(
    (c, i) =>
      `<button class="btn ${size} ${i === 0 ? 'primary' : ''}" data-action="tg-open" data-path="${esc(c.path)}" title="${esc(c.path)}">Подключить в ${esc(c.name)}</button>`,
  );
  if (!clients.length) buttons.push(`<button class="btn ${size} primary" data-action="tg-connect">Подключить Telegram</button>`);
  buttons.push(`<button class="btn ${size} ${minor}" data-action="tg-choose">${clients.length ? 'Другая программа…' : 'Выбрать программу…'}</button>`);
  buttons.push(`<button class="btn ${size} ${minor}" data-action="tg-copy">${state.copied ? 'Ссылка скопирована' : 'Скопировать ссылку'}</button>`);
  return full ? buttons.join('') : `<div class="button-row">${buttons.join('')}</div>`;
}

function settingsView(st) {
  const selecting = st.task?.kind === 'select';
  const tg = st.telegram ?? {};
  const modes = [['off', 'Выкл'], ['games', 'Игры'], ['all', 'Все порты']];
  if (st.game_mode === 'tcp' || st.game_mode === 'udp') modes.push([st.game_mode, st.game_mode.toUpperCase()]);
  const gameSub =
    {
      off: 'Игровой трафик обход не трогает',
      games: 'Только порты выбранных игр',
      all: 'Все порты 1024–65535 — больше нагрузка на процессор',
      tcp: 'Все порты TCP 1024–65535',
      udp: 'Все порты UDP 1024–65535',
    }[st.game_mode] ?? '';
  return `${subHeader('Настройки')}
    ${errorLine()}
    <section class="stack">
      <div class="section-label"><span>Приложение</span></div>
      <div class="panel">
        <div class="settings-row">
          <span class="row-text"><span class="row-title-sm">Запускать вместе с Windows</span><span class="row-sub wrap">Значок появится в трее сразу после входа в систему</span></span>
          <button class="toggle ${state.autostart ? 'on' : ''}" role="switch" aria-checked="${Boolean(state.autostart)}" aria-label="Запускать вместе с Windows" data-action="autostart" ${state.autostart === null ? 'disabled' : ''}></button>
        </div>
        <div class="settings-row">
          <span class="row-text"><span class="row-title-sm">Версия</span><span class="row-sub" title="${esc(appLine(st.app))}">${esc(appLine(st.app))}</span></span>
          ${appButton(st)}
        </div>
        ${
          st.app?.configured
            ? `<div class="settings-row">
          <span class="row-text"><span class="row-title-sm">Обновлять FI автоматически</span><span class="row-sub wrap">Раз в сутки; установщик подписан и сверяется перед запуском</span></span>
          <button class="toggle ${st.app.auto_update ? 'on' : ''}" role="switch" aria-checked="${Boolean(st.app.auto_update)}" aria-label="Обновлять FI автоматически" data-action="app-auto"></button>
        </div>`
            : ''
        }
      </div>
    </section>
    <section class="stack">
      <div class="section-label"><span>Обход</span></div>
      <div class="panel">
        <div class="settings-row">
          <span class="row-text"><span class="row-title-sm">Чинить автоматически</span><span class="row-sub wrap">Если сервис перестал работать и через минуту это подтвердилось, подобрать стратегию заново. Пока включён VPN, не срабатывает.</span></span>
          <button class="toggle ${st.auto_fix ? 'on' : ''}" role="switch" aria-checked="${st.auto_fix}" aria-label="Чинить автоматически" data-action="auto-fix"></button>
        </div>
        <div class="settings-row column">
          <span class="row-text"><span class="row-title-sm">Игровой режим</span><span class="row-sub wrap">${gameSub}</span></span>
          <div class="seg" role="radiogroup" aria-label="Игровой режим">${modes
            .map(([value, label]) => `<button role="radio" aria-checked="${st.game_mode === value}" class="${st.game_mode === value ? 'on' : ''}" data-action="game-mode" data-mode="${value}" ${selecting ? 'disabled' : ''}>${label}</button>`)
            .join('')}</div>
          ${st.game_mode === 'games' ? gameChips(st) : ''}
        </div>
        <div class="settings-row column">
          <span class="row-text"><span class="row-title-sm">Стратегия</span><span class="row-sub wrap">Подбор проверяет все стратегии набора и оставляет лучшую. Можно выбрать и вручную.</span></span>
          <div class="input-row">
            <select class="select" id="strategy-select" ${st.task ? 'disabled' : ''}>
              ${(st.strategy_list ?? [])
                .map((name) => `<option value="${esc(name)}" ${name === st.strategy ? 'selected' : ''}>${esc(strategyLabel(name))}</option>`)
                .join('')}
            </select>
            <button class="btn sm" data-action="select" ${st.task || st.vpn ? 'disabled' : ''} title="${st.vpn ? 'Выключите VPN: через него подбор выбрал бы наугад' : ''}">Подобрать</button>
          </div>
        </div>
        <div class="settings-row">
          <span class="row-text"><span class="row-title-sm">Набор стратегий</span><span class="row-sub" title="${esc(baseLine(st.base))}">${esc(baseLine(st.base))}</span></span>
          ${
            st.base?.update_available
              ? `<button class="btn sm primary" data-action="base-update" ${st.task ? 'disabled' : ''}>Обновить</button>`
              : `<button class="btn sm ghost" data-action="base-check" ${st.task ? 'disabled' : ''}>Проверить</button>`
          }
        </div>
        <div class="settings-row">
          <span class="row-text"><span class="row-title-sm">Обновлять набор автоматически</span><span class="row-sub wrap">Раз в сутки; выбранная стратегия сохраняется</span></span>
          <button class="toggle ${st.base?.auto_update ? 'on' : ''}" role="switch" aria-checked="${Boolean(st.base?.auto_update)}" aria-label="Обновлять набор автоматически" data-action="base-auto"></button>
        </div>
      </div>
    </section>
    <section class="stack">
      <div class="section-label"><span>Telegram</span></div>
      <div class="panel">
        <div class="settings-row">
          <span class="row-text"><span class="row-title-sm">Прокси для Telegram и AyuGram</span><span class="row-sub">${
            tg.enabled ? `<span class="mono">${esc(tg.address)}</span>${tg.error ? ' · не запущен' : tg.route ? ` · ${esc(routeText(tg.route))}` : ''}` : 'Выключен'
          }</span></span>
          <button class="toggle ${tg.enabled ? 'on' : ''}" role="switch" aria-checked="${Boolean(tg.enabled)}" aria-label="Прокси для Telegram" data-action="tg-toggle"></button>
        </div>
        ${
          tg.enabled
            ? `<div class="settings-row column">
                <span class="row-text"><span class="row-title-sm">Подключить прокси</span><span class="row-sub wrap">Выберите программу: она откроется и предложит включить прокси — согласитесь.</span></span>
                ${tgClientButtons()}
              </div>
              <div class="settings-row">
                <span class="row-text"><span class="row-title-sm">Общие домены Cloudflare</span><span class="row-sub wrap">Когда провайдер закрыл адреса Telegram. Домены из проекта tg-ws-proxy (Flowseal)${
                  tg.shared_cf && tg.cf_domains ? ` · ${tg.cf_domains} ${plural(tg.cf_domains, 'домен', 'домена', 'доменов')}` : ''
                }</span></span>
                <button class="toggle ${tg.shared_cf ? 'on' : ''}" role="switch" aria-checked="${Boolean(tg.shared_cf)}" aria-label="Общие домены Cloudflare" data-action="tg-shared-cf"></button>
              </div>
              <div class="settings-row column">
                <span class="row-text"><span class="row-title-sm">Свой Cloudflare Worker</span><span class="row-sub wrap">Личный путь надёжнее общих доменов: его никто не делит с вами. Как создать — в README. Пусто — без воркера.</span></span>
                <form class="input-row" data-form="worker">
                  <label class="input"><input id="worker-input" name="host" autocomplete="off" spellcheck="false" placeholder="имя-1234.логин.workers.dev" value="${esc(state.worker ?? tg.worker ?? '')}"></label>
                  <button class="btn sm" type="submit">Сохранить</button>
                </form>
              </div>`
            : ''
        }
      </div>
    </section>
    <section class="stack">
      <div class="section-label"><span>Журнал и отчёт</span></div>
      <div class="panel">
        <div class="settings-row column">
          <span class="row-text"><span class="row-title-sm">Если что-то не работает</span><span class="row-sub wrap">Отчёт собирает журналы, настройки и диагностику в один архив — его можно приложить к сообщению о проблеме. Ключ прокси Telegram в отчёт не попадает.</span></span>
          <div class="button-row">
            <button class="btn sm" data-action="report">Сохранить отчёт</button>
            <button class="btn sm ghost" data-action="logs">Открыть папку журналов</button>
          </div>
          ${noticeLine()}
        </div>
      </div>
    </section>
    <p class="hint">Стратегий в наборе: ${st.strategies}</p>`;
}

function unavailableView() {
  return `<section class="stack onboarding">
      <span class="eyebrow">Служба не отвечает</span>
      <h1 class="display">FI не запущен</h1>
      <p class="lead">Окно только показывает состояние, а обходом управляет служба. Установите и запустите её в терминале администратора:</p>
      <div class="code">fi-service install</div>
    </section>
    <div class="setup-actions"><button class="btn full" data-action="refresh">Проверить снова</button></div>`;
}

// ─── Отрисовка ────────────────────────────────────────────────────────────

const root = document.getElementById('app');
let lastHTML = '';

const FOCUS_KEYS = ['action', 'screen', 'mode', 'host', 'service', 'fix', 'game', 'path'];

function render() {
  const html = view();
  if (html === lastHTML) return; // опрос без изменений не сбивает фокус и анимации
  lastHTML = html;

  const active = document.activeElement;
  const focus = active && root.contains(active)
    ? { id: active.id, data: FOCUS_KEYS.map((k) => active.dataset?.[k]), start: active.selectionStart, end: active.selectionEnd }
    : null;
  root.innerHTML = html;
  if (!focus) return;
  const target = focus.id
    ? document.getElementById(focus.id)
    : [...root.querySelectorAll('[data-action]')].find((el) => FOCUS_KEYS.every((k, i) => el.dataset[k] === focus.data[i]));
  if (target && !target.disabled) {
    target.focus();
    if (focus.start != null && target.setSelectionRange) target.setSelectionRange(focus.start, focus.end);
  }
}

function go(screen) {
  state.screen = screen;
  state.message = '';
  state.notice = '';
  if (screen === 'site') state.site = { ...state.site, result: null, error: '' };
  if (screen === 'diagnose') state.diagnose = { service: '', checking: false, report: null, fixing: '' };
  if (screen === 'settings') {
    loadAutostart();
    loadTgClients();
  }
  render();
  if (screen === 'site') document.getElementById('site-input')?.focus();
}

// ─── Команды ──────────────────────────────────────────────────────────────

function handleError(err, onMessage = (m) => (state.message = m)) {
  if (err.code === 'service_unavailable') state.unavailable = true;
  else onMessage(err.message);
}

async function refresh() {
  try {
    state.status = await api('status');
    state.unavailable = false;
  } catch (err) {
    handleError(err);
  }
  render();
}

async function command(method, params) {
  state.message = '';
  try {
    const result = await api(method, params);
    if (result && typeof result === 'object' && 'enabled' in result) state.status = result;
    state.unavailable = false;
  } catch (err) {
    handleError(err);
  }
  render();
  schedule();
}

// addSite проверяет и добавляет сайт из поля ввода; force — добавить тот, что открывается и без обхода.
async function addSite(force = false) {
  const s = state.site;
  const host = force ? s.result?.host : s.input;
  if (!host?.trim()) {
    s.error = 'Введите адрес сайта';
    render();
    return;
  }
  s.busy = true;
  s.error = '';
  s.result = null;
  state.notice = '';
  render();
  try {
    s.result = await api('add_site', { host, force });
    if (!force) s.input = '';
  } catch (err) {
    handleError(err, (m) => (s.error = m));
  } finally {
    s.busy = false;
  }
  await refresh();
}

// fileAction — действие с файлом через окно Windows: отчёт, сохранение и загрузка списка сайтов.
async function fileAction(path, done) {
  state.message = '';
  state.notice = '';
  const data = await postJSON(path);
  if (!data) state.message = 'Окно потеряло связь со своей частью на Go';
  else if (data.error) state.message = data.error;
  else if (data.status) state.status = data.status;
  if (data && !data.error) state.notice = done(data) ?? '';
  render();
}

async function paste() {
  const res = await post('/app/clipboard');
  const data = res && res.ok ? await res.json().catch(() => null) : null;
  if (!data?.text) return;
  state.site.input = data.text.trim();
  state.site.error = '';
  render();
  document.getElementById('site-input')?.focus();
}

async function loadAutostart() {
  const data = await postJSON('/app/autostart');
  state.autostart = typeof data?.enabled === 'boolean' ? data.enabled : null;
  render();
}

async function setAutostart(on) {
  const data = await postJSON('/app/autostart', { on });
  if (data?.error) state.message = data.error;
  state.autostart = typeof data?.enabled === 'boolean' ? data.enabled : state.autostart;
  render();
}

async function loadTgClients() {
  const data = await postJSON('/app/telegram/clients');
  if (Array.isArray(data?.clients)) state.tgClients = data;
  render();
}

// openTelegram открывает ссылку прокси в клиенте Telegram; path пустой — в последнем выбранном.
async function openTelegram(path = '') {
  const data = await postJSON('/app/telegram/open', { path });
  state.message = data?.error ?? '';
  if (path && !data?.error && state.tgClients) state.tgClients.preferred = path;
  render();
}

// chooseTelegram даёт указать файл программы, например AyuGram.exe на рабочем столе.
async function chooseTelegram() {
  const data = await postJSON('/app/telegram/choose');
  state.message = data?.error ?? '';
  await loadTgClients();
}

async function copyTelegram() {
  const data = await postJSON('/app/telegram/copy');
  if (data?.error) {
    state.message = data.error;
    render();
    return;
  }
  state.copied = true;
  render();
  setTimeout(() => {
    state.copied = false;
    render();
  }, 2000);
}

// loadReport берёт у окна диагностику компьютера: службу и настройки пользователя.
async function loadReport() {
  const res = await post('/app/diagnose');
  const data = res ? await res.json().catch(() => null) : null;
  if (res?.ok && data?.result) return data.result;
  if (data?.code === 'service_unavailable') state.unavailable = true;
  else state.message = data?.error || 'Не удалось проверить компьютер';
  return null;
}

// diagnose проверяет сервис при текущем обходе и заодно ищет, что на компьютере мешает обходу.
async function diagnose(service) {
  const d = (state.diagnose = { service, checking: true, report: null, fixing: '' });
  state.message = '';
  render();
  if (service === 'telegram') loadTgClients();
  const check =
    service === 'computer'
      ? null
      : api('check')
          .then((st) => (state.status = st))
          .catch(handleError);
  [, d.report] = await Promise.all([check, loadReport()]);
  d.checking = false;
  render();
}

async function fix(id) {
  const d = state.diagnose;
  d.fixing = id;
  state.message = '';
  render();
  try {
    await api('fix', { id });
    d.report = (await loadReport()) ?? d.report;
  } catch (err) {
    handleError(err);
  }
  d.fixing = '';
  render();
}

document.addEventListener('click', (event) => {
  const el = event.target.closest('[data-action]');
  if (!el || el.disabled) return;
  const { action } = el.dataset;
  switch (action) {
    case 'hide':
      post('/app/hide');
      break;
    case 'minimize':
      post('/app/minimize');
      break;
    case 'autostart':
      setAutostart(!state.autostart);
      break;
    case 'tg-toggle':
      command('set_telegram', { on: !state.status?.telegram?.enabled });
      break;
    case 'tg-connect':
      openTelegram();
      break;
    case 'tg-open':
      openTelegram(el.dataset.path);
      break;
    case 'tg-choose':
      chooseTelegram();
      break;
    case 'tg-shared-cf':
      command('set_telegram_shared_cf', { on: !state.status?.telegram?.shared_cf });
      break;
    case 'tg-copy':
      copyTelegram();
      break;
    case 'base-update':
      command('update_base');
      break;
    case 'base-check':
      command('check_base');
      break;
    case 'base-auto':
      command('set_auto_update', { on: !state.status?.base?.auto_update });
      break;
    case 'back':
      go('main');
      break;
    case 'open':
      go(el.dataset.screen);
      break;
    case 'refresh':
      refresh();
      break;
    case 'toggle':
      command('set_enabled', { on: !state.status?.enabled });
      break;
    case 'check':
      command('check');
      break;
    case 'select':
      state.screen = 'main';
      command('select');
      break;
    case 'cancel':
      command('cancel');
      break;
    case 'auto-fix':
      command('set_auto_fix', { on: !state.status?.auto_fix });
      break;
    case 'game-mode':
      command('set_game_mode', { mode: el.dataset.mode });
      break;
    case 'force-site':
      addSite(true);
      break;
    case 'sites-export':
      fileAction('/app/sites/export', (d) => (d.path ? `Список сохранён: ${d.path}` : ''));
      break;
    case 'sites-import':
      fileAction('/app/sites/import', (d) =>
        d.added === undefined ? '' : d.added ? `Добавлено сайтов: ${d.added}` : 'Все сайты из файла уже были в списке',
      );
      break;
    case 'report':
      fileAction('/app/report', (d) => (d.path ? `Отчёт сохранён: ${d.path}` : ''));
      break;
    case 'logs':
      fileAction('/app/logs', () => '');
      break;
    case 'remove-site':
      command('remove_site', { host: el.dataset.host });
      break;
    case 'paste':
      paste();
      break;
    case 'diagnose':
      diagnose(el.dataset.service);
      break;
    case 'fix':
      fix(el.dataset.fix);
      break;
    case 'conflict':
      go('diagnose');
      diagnose('computer');
      break;
    case 'game': {
      const games = new Set(state.status?.games ?? []);
      if (!games.delete(el.dataset.game)) games.add(el.dataset.game);
      command('set_games', { games: [...games] });
      break;
    }
    case 'app-check':
      command('check_app');
      break;
    case 'app-update':
      command('update_app');
      break;
    case 'app-auto':
      command('set_app_auto_update', { on: !state.status?.app?.auto_update });
      break;
  }
});

document.addEventListener('submit', (event) => {
  const form = event.target.dataset.form;
  if (form !== 'site' && form !== 'worker') return;
  event.preventDefault();
  if (form === 'site') {
    addSite();
    return;
  }
  const host = document.getElementById('worker-input')?.value ?? '';
  state.worker = null;
  command('set_telegram_worker', { host });
});

document.addEventListener('input', (event) => {
  if (event.target.id === 'worker-input') {
    state.worker = event.target.value;
    return;
  }
  if (event.target.id !== 'site-input') return;
  state.site.input = event.target.value;
  if (state.site.error) {
    state.site.error = '';
    render();
  }
});

// ─── Опрос службы ─────────────────────────────────────────────────────────

let timer;
function schedule() {
  clearTimeout(timer);
  const busy = Boolean(state.status?.task) || state.site.busy;
  timer = setTimeout(async () => {
    if (document.visibilityState === 'visible') await refresh();
    schedule();
  }, busy ? 1000 : 4000);
}

document.addEventListener('change', (event) => {
  if (event.target.id !== 'strategy-select') return;
  command('set_strategy', { name: event.target.value });
});

// Окно можно тянуть за края: вёрстка всегда считается по ширине 400, а потом растягивается
// под размер окна — вместе с текстом, отступами и значками.
const DESIGN_WIDTH = 400;

let scaledTo = '';

function applyScale() {
  const root = document.documentElement;
  const width = root.clientWidth;
  const height = root.clientHeight;
  const size = `${width}x${height}`;
  if (size === scaledTo || width === 0) return;
  scaledTo = size;

  const scale = Math.min(2, Math.max(0.8, width / DESIGN_WIDTH));
  const box = document.getElementById('scale');
  box.style.transform = `scale(${scale})`;
  box.style.width = `${width / scale}px`;
  box.style.height = `${height / scale}px`;
}

addEventListener('resize', applyScale);
// Событие resize приходит не во всех окружениях, поэтому размер ещё и проверяется: это дёшево.
setInterval(applyScale, 250);
applyScale();

document.addEventListener('visibilitychange', () => {
  if (document.visibilityState === 'visible') refresh().then(schedule);
});

// Вызывается из меню трея «Добавить сайт из буфера».
window.fi = {
  addSite(text) {
    state.screen = 'site';
    state.site = { input: String(text ?? '').trim(), busy: false, result: null, error: '' };
    render();
    addSite();
  },
};

refresh().then(schedule);
