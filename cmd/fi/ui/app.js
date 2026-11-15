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
  launch: '<path d="M13.5 4.5h6v6"/><path d="M19.5 4.5L11 13"/><path d="M17.5 14v4a1.5 1.5 0 0 1-1.5 1.5H6A1.5 1.5 0 0 1 4.5 18V8A1.5 1.5 0 0 1 6 6.5h4"/>',
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

// idlePrograms — программы через Cloudflare, пока ничего не запускают. confirm — путь программы,
// про которую спрашиваем «закрыть и запустить»; path — к какой программе относятся notice и error.
function idlePrograms() {
  return { busy: false, confirm: '', notice: '', error: '', path: '' };
}

const state = {
  screen: 'main', // main, site, diagnose, settings, look, cloudflare
  from: 'main', // с какого экрана открыт вложенный (оформление, Cloudflare): туда ведёт «Назад»
  status: null,
  unavailable: false,
  message: '',
  site: { input: '', busy: false, result: null, error: '' },
  diagnose: { service: '', checking: false, report: null, fixing: '' }, // report — что мешает обходу на компьютере
  autostart: null, // запуск окна вместе с Windows; null — ещё не узнали
  copied: false, // ссылка прокси Telegram только что скопирована
  worker: null, // домен Cloudflare Worker, пока его правят в поле; null — показываем сохранённый
  viaWorker: null, // сайт в поле «Сайты через воркер», пока его вводят
  viaWorkerError: '', // почему сайт не добавлен: показывается под полем
  tgClients: null, // клиенты Telegram на компьютере (Telegram, AyuGram…); null — ещё не искали
  notice: '', // что получилось: «Отчёт сохранён…»; сбрасывается при переходе между экранами
  look: null, // оформление: тема, акцент, шрифт, углы, масштаб, анимации (readLook ниже)
  // Мастер Cloudflare: wizard — открыт поверх настроенного воркера; token живёт только в памяти
  // окна, пока воркер не создан; manual и formHelp — раскрытые подсказки.
  cf: { wizard: false, token: '', region: 'de', remember: true, error: '', hint: '', manual: false, formHelp: false },
  programs: idlePrograms(),
};

// ─── Оформление ──────────────────────────────────────────────────────────
// Тема, свой акцентный цвет, шрифт, углы, масштаб и анимации. Хранятся в настройках окна
// (/app/theme), а копия — в localStorage: index.html применяет её до первой отрисовки.

const THEMES = [
  ['dark', 'Тёмная', ['#0B0A0D', '#131116', '#7C4FE0']],
  ['gray', 'Серая', ['#1E1F22', '#2B2D31', '#7C4FE0']],
  ['light', 'Светлая', ['#F6F4F8', '#FFFFFF', '#6E3FD8']],
  ['anime', 'Аниме', ['#1A101F', '#2E1A38', '#FF5FA8']],
  ['mono', 'Ч/Б', ['#000000', '#171717', '#FFFFFF']],
  ['neon', 'Неон', ['#070812', '#0D1020', '#00D9F5']],
  ['forest', 'Лес', ['#0C1410', '#121D17', '#3FB66B']],
  ['ocean', 'Океан', ['#08111C', '#0E1A28', '#2F8CF0']],
];
const ACCENTS = ['#7C4FE0', '#FF5FA8', '#E5484D', '#F59E0B', '#3FB66B', '#00D9F5', '#2F8CF0', '#FFFFFF'];
const FONTS = [
  ['', 'Как в теме'],
  ['onest', 'Onest'],
  ['segoe', 'Segoe UI'],
  ['bahnschrift', 'Bahnschrift'],
  ['mono', 'Моноширинный'],
  ['georgia', 'С засечками'],
  ['handwritten', 'Рукописный'],
];
const RADII = [['sharp', 'Острые'], ['', 'Обычные'], ['round', 'Круглые']];
const SCALES = [[90, 'Мельче'], [0, 'Обычный'], [110, 'Крупнее'], [120, 'Ещё крупнее']];
const DEFAULT_LOOK = { theme: 'dark', accent: '', font: '', radius: '', scale: 0, motion: true };

function readLook() {
  try {
    return { ...DEFAULT_LOOK, ...JSON.parse(localStorage.getItem('fi-look') || '{}') };
  } catch {
    return { ...DEFAULT_LOOK };
  }
}

const rgbOf = (hex) => {
  const n = parseInt(hex.slice(1), 16);
  return [n >> 16, (n >> 8) & 255, n & 255];
};
const mixRGB = (rgb, to, t) => rgb.map((c, i) => Math.round(c + (to[i] - c) * t));
const hexOf = (rgb) => `#${rgb.map((c) => c.toString(16).padStart(2, '0')).join('')}`;

// accentVars — оттенки акцента для кнопок, меток и рамок; текст на акценте — чёрный или белый.
function accentVars(hex, light) {
  const rgb = rgbOf(hex);
  const lum = (0.2126 * rgb[0] + 0.7152 * rgb[1] + 0.0722 * rgb[2]) / 255;
  return {
    '--violet': hex,
    '--violet-hover': hexOf(mixRGB(rgb, lum > 0.6 ? [0, 0, 0] : [255, 255, 255], 0.12)),
    '--violet-pressed': hexOf(mixRGB(rgb, [0, 0, 0], 0.18)),
    '--violet-text': hexOf(light ? mixRGB(rgb, [0, 0, 0], 0.25) : mixRGB(rgb, [255, 255, 255], 0.3)),
    '--violet-tint': `rgba(${rgb.join(', ')}, 0.14)`,
    '--violet-line': `rgba(${rgb.join(', ')}, 0.45)`,
    '--on-accent': lum > 0.6 ? '#000000' : '#FFFFFF',
  };
}

function applyLook(look) {
  const next = { ...DEFAULT_LOOK, ...look };
  if (!THEMES.some(([id]) => id === next.theme)) next.theme = DEFAULT_LOOK.theme;
  state.look = next;
  const root = document.documentElement;
  root.dataset.theme = next.theme;
  for (const key of ['font', 'radius']) {
    if (next[key]) root.dataset[key] = next[key];
    else delete root.dataset[key];
  }
  root.dataset.motion = next.motion ? 'on' : 'off';
  const vars = accentVars(next.accent || '#000000', next.theme === 'light');
  for (const name of Object.keys(vars)) {
    if (next.accent) root.style.setProperty(name, vars[name]);
    else root.style.removeProperty(name);
  }
  scaledTo = ''; // масштаб интерфейса пересчитается
  try {
    localStorage.setItem('fi-look', JSON.stringify(next));
  } catch {}
}

async function loadLook() {
  const data = await postJSON('/app/theme');
  if (data?.theme) {
    applyLook(data);
    applyScale();
    render();
  }
}

// setLook меняет оформление сразу, а потом сохраняет его.
async function setLook(change) {
  applyLook({ ...state.look, ...change });
  applyScale();
  render();
  const data = await postJSON('/app/theme', change);
  if (data?.error) {
    state.message = data.error;
    render();
  }
}

const themeLabel = (id) => THEMES.find(([value]) => value === id)?.[1] ?? id;

function lookView() {
  const l = state.look;
  const cards = THEMES.map(
    ([id, label, [bg, panel, accent]]) =>
      `<button class="theme-card ${l.theme === id ? 'on' : ''}" data-action="look-theme" data-value="${id}" aria-pressed="${l.theme === id}">
        <span class="theme-preview" style="background:${bg}"><span style="background:${panel}"></span><i style="background:${accent}"></i></span>
        <span>${label}</span>
      </button>`,
  ).join('');
  const custom = Boolean(l.accent) && !ACCENTS.includes(l.accent.toUpperCase());
  const swatches = ACCENTS.map(
    (c) =>
      `<button class="swatch ${l.accent.toUpperCase() === c ? 'on' : ''}" style="background:${c}" data-action="look-accent" data-value="${c}" aria-label="Цвет ${c}"></button>`,
  ).join('');
  const seg = (items, value, action, label) =>
    `<div class="seg" role="radiogroup" aria-label="${label}">${items
      .map(([v, text]) => `<button role="radio" aria-checked="${value === v}" class="${value === v ? 'on' : ''}" data-action="${action}" data-value="${v}">${text}</button>`)
      .join('')}</div>`;
  return `${subHeader('Оформление')}
    ${errorLine()}
    <section class="stack">
      <div class="section-label"><span>Тема</span></div>
      <div class="theme-grid">${cards}</div>
    </section>
    <section class="stack">
      <div class="section-label"><span>Акцентный цвет</span></div>
      <div class="panel"><div class="settings-row column">
        <div class="swatches">${swatches}<label class="swatch custom ${custom ? 'on' : ''}" title="Свой цвет" style="${custom ? `background:${esc(l.accent)}` : ''}"><input type="color" id="accent-input" value="${esc(l.accent || '#7C4FE0')}" aria-label="Свой цвет"></label></div>
        <span class="row-sub wrap">Кнопки, переключатели и метки. Последний кружок — свой цвет.</span>
        <div class="button-row"><button class="btn sm ghost" data-action="look-accent" data-value="" ${l.accent ? '' : 'disabled'}>Цвет темы</button></div>
      </div></div>
    </section>
    <section class="stack">
      <div class="section-label"><span>Текст и форма</span></div>
      <div class="panel">
        <div class="settings-row">
          <span class="row-text"><span class="row-title-sm">Шрифт</span></span>
          <select class="select" id="font-select" style="flex: none; width: 160px">${FONTS.map(
            ([v, text]) => `<option value="${v}" ${l.font === v ? 'selected' : ''}>${text}</option>`,
          ).join('')}</select>
        </div>
        <div class="settings-row column">
          <span class="row-text"><span class="row-title-sm">Углы</span></span>
          ${seg(RADII, l.radius, 'look-radius', 'Углы')}
        </div>
        <div class="settings-row column">
          <span class="row-text"><span class="row-title-sm">Размер интерфейса</span></span>
          ${seg(SCALES, l.scale, 'look-scale', 'Размер интерфейса')}
        </div>
        <div class="settings-row">
          <span class="row-text"><span class="row-title-sm">Анимации</span><span class="row-sub wrap">Плавные переходы, лепестки в теме «Аниме». Не показываются и тогда, когда анимации выключены в Windows.</span></span>
          <button class="toggle ${l.motion ? 'on' : ''}" role="switch" aria-checked="${l.motion}" aria-label="Анимации" data-action="look-motion"></button>
        </div>
      </div>
    </section>
    <div class="button-row"><button class="btn sm" data-action="look-reset">Сбросить оформление</button></div>`;
}

const TITLES = { youtube: 'YouTube', discord: 'Discord', telegram: 'Telegram', calls: 'Звонки', sites: 'Сайты' };

const SERVICE_META = {
  youtube: { icon: 'play', sub: 'Сайт и видео', hint: 'Видео, превью' },
  discord: { icon: 'headset', sub: 'Сайт, вход и файлы', hint: 'Вход, сообщения' },
  telegram: { icon: 'bubble', sub: 'Сайт и веб-клиент', hint: 'Сообщения, медиа' },
  calls: { icon: 'phone', sub: 'Звонки в браузере и мессенджерах', hint: 'Голос, видеосвязь' },
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
  vpn: ['off', 'Пауза'],
};

const VERDICT_BADGE = {
  fixed: ['fixed', 'Исправлено'],
  works: ['ok', 'Открывается'],
  added: ['ok', 'Добавлен'],
  not_fixed: ['fail', 'Не помогло'],
  dns: ['warn', 'Нужен DNS'],
  tunnel: ['warn', 'Нужен туннель'],
  vpn: ['warn', 'Нужен VPN'],
  via_worker: ['proxy', 'Через воркер'],
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
  if (app.update_available && app.latest) return `${text} · доступна ${app.latest}`;
  return app.checked_at ? `${text} · последняя версия` : text;
}

function appButton(st) {
  const app = st.app ?? {};
  if (app.update_available && !app.configured) {
    // Сборка без ключа обновлений сама не ставит новую версию — открываем страницу загрузки.
    return `<button class="btn sm primary" data-action="app-release">Скачать</button>`;
  }
  return app.update_available
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
    case 'look':
      return lookView();
    case 'cloudflare':
      return cloudflareView(st);
    default:
      return mainView(st);
  }
}

// clockText — время суток: «14:30».
const clockText = (iso) => new Date(iso).toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit' });

function heroView(st) {
  const task = st.task;
  let title;
  let sub;
  let taskRow = '';
  if (task?.kind === 'select') {
    title = task.quick ? 'Быстрый подбор стратегии' : 'Подбираем стратегию';
    sub = task.done > 0 ? `${selectStep(task)} · <span class="mono">${esc(strategyLabel(task.current))}</span>` : 'Проверяем сеть без обхода';
    taskRow = `<div class="hero-task">${progress(task.done, task.total)}<button class="btn ghost sm" data-action="cancel">Отменить</button></div>`;
  } else if (task?.kind === 'app') {
    title = 'Обновляем FI';
    sub = esc(task.current || '');
    taskRow = `<div class="hero-task">${progress(task.done, task.total)}</div>`;
  } else if (task?.kind === 'base') {
    title = 'Обновляем набор стратегий';
    sub = esc(task.current || '');
    taskRow = `<div class="hero-task">${progress(task.done, task.total)}</div>`;
  } else if (!st.enabled && st.paused_until) {
    title = 'Обход на паузе';
    sub = `Включится сам в ${esc(clockText(st.paused_until))}`;
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

// selectStep — где подбор: «Отбор 5 из 26» (коротко все стратегии) или «Проверка лучших 2 из 4».
// В быстром подборе отбора нет — тогда просто «Стратегия 2 из 4».
function selectStep(task) {
  const steps = Math.max(0, task.total - 1);
  const n = state.status?.strategies ?? steps;
  if (steps > n) {
    return task.done <= n ? `Отбор ${task.done} из ${n}` : `Проверка лучших ${task.done - n} из ${steps - n}`;
  }
  return `Стратегия ${task.done} из ${steps}`;
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
    : Object.keys(TITLES).map((id) => ({ id, title: TITLES[id], state: st.vpn ? 'vpn' : 'pending' })); // при VPN проверки на паузе
  const checking = st.task?.kind === 'check';
  const vpn = st.vpn
    ? `<section class="panel notice">${icon('globe', 16)}<span>Интернет идёт через VPN (${esc(st.vpn)}). Обход работает только для трафика мимо VPN, проверки и автоподбор на паузе, уведомлений не будет.</span></section>`
    : '';
  const dns = st.dns_problem
    ? `<section class="panel notice warn">${icon('globe', 16)}<span>DNS роутера или провайдера не отвечает — браузер может не открывать сайты (YouTube и другие), хотя проверки FI проходят: FI ищет адреса в обход него. <button class="btn sm" data-action="public-dns">Использовать публичный DNS</button></span></section>`
    : '';
  // Серверы Google со свежими роликами провайдер закрывает по адресу: стратегия их не откроет.
  const tgw = st.telegram ?? {};
  const fresh =
    st.youtube_blocked && !st.youtube_via_worker && !st.vpn
      ? `<section class="panel notice warn">${icon('play', 16)}<span>Часть видео YouTube не грузится: провайдер закрыл серверы Google со свежими и редкими роликами, и обход их не откроет. ${
          tgw.enabled && tgw.worker
            ? 'FI поведёт их через ваш воркер Cloudflare — в Windows появится сценарий прокси только для них. <button class="btn sm" data-action="youtube-via-worker-on">Включить</button>'
            : 'Поможет бесплатный сервер в Cloudflare — настройка в три шага. <button class="btn sm" data-action="open" data-screen="cloudflare">Настроить</button>'
        }</span></section>`
      : '';
  // Cloudflare: исчерпанный лимит и обрывы не должны проходить молча — человек иначе гадает,
  // почему всё встало. Новый код воркера с сохранённым токеном FI ставит сам; без токена — зовём.
  const cf = st.cloudflare ?? {};
  const cfUsed = tgw.enabled && tgw.worker;
  let cloud = '';
  if (cfUsed && cf.limited_until) {
    cloud = `<section class="panel notice warn">${icon('clock', 16)}<span>Суточный лимит Cloudflare исчерпан: воркер снова заработает в ${esc(clockText(cf.limited_until))}. Telegram идёт запасным путём; сайты через воркер и закрытые ролики YouTube до тех пор не откроются. <button class="btn sm" data-action="open" data-screen="cloudflare">Подробнее</button></span></section>`;
  } else if (cfUsed && (cf.cuts ?? 0) >= 5) {
    cloud = `<section class="panel notice warn">${icon('pulse', 16)}<span>Cloudflare обрывает загрузки через воркер: бесплатному тарифу не хватает процессорного времени на долгое видео и большие файлы. Переписка и сайты идут как прежде. <button class="btn sm" data-action="open" data-screen="cloudflare">Подробнее</button></span></section>`;
  } else if (cfUsed && (tgw.worker_outdated || (tgw.worker_update && (!cf.has_token || cf.token_error)))) {
    cloud = `<section class="panel notice warn">${icon('refresh', 16)}<span>Вышел новый код воркера Cloudflare: с ним загрузки обрываются реже. Обновление — пара минут. <button class="btn sm" data-action="open" data-screen="cloudflare">Обновить</button></span></section>`;
  }
  // Программы через Cloudflare — запуск одним нажатием прямо с главного экрана.
  const apps = st.programs?.apps ?? [];
  const run = state.programs;
  const asking = apps.find((a) => a.path === run.confirm);
  const quick = apps.length
    ? `<section class="panel quick">
        <div class="quick-row">
          <span class="quick-label">Через Cloudflare</span>
          ${apps.map((a) => `<button class="chip run" data-action="program-run" data-path="${esc(a.path)}" title="Запустить через Cloudflare" ${run.busy ? 'disabled' : ''}>${icon('launch', 13, 2)}<span>${esc(a.name)}</span></button>`).join('')}
        </div>
        ${asking ? programAsk(asking) : ''}
        ${run.notice ? `<p class="notice-line">${icon('check', 14, 2)}<span>${esc(run.notice)}</span></p>` : ''}
        ${run.error ? `<p class="field-error">${esc(run.error)}</p>` : ''}
      </section>`
    : '';
  const game = st.anti_cheat
    ? `<section class="panel notice">${icon('play', 16)}<span>Идёт игра с античитом ${esc(st.anti_cheat)}. ${
        st.running
          ? 'Обход работает, но FI не перезапускает его до выхода из игры: иначе античит заблокирует драйвер до перезагрузки.'
          : 'Обход выключен и не запустится, пока идёт игра: иначе античит заблокирует драйвер до перезагрузки. После игры FI включит его сам.'
      }</span></section>`
    : '';
  return `${heroView(st)}
    ${dns}
    ${fresh}
    ${cloud}
    ${game}
    ${errorLine()}
    ${vpn}
    ${selectNote(st)}
    ${quick}
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
        <span class="mark-anim">${mark(52)}</span>
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
      <span class="mark-anim">${mark(52)}</span>
      <span class="eyebrow">Первый запуск</span>
      <h1 class="display">${verb} способ обхода для вашей сети</h1>
      <p class="lead">Проверим ${n} ${plural(n, 'стратегию', 'стратегии', 'стратегий')} на настоящих сайтах. Сначала коротко проверим все, потом подробно — лучшие. Это займёт около трёх минут, интернет может ненадолго прерываться.</p>
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
        <span>${task.done === 0 ? 'Проверяем сеть без обхода' : selectStep(task)}</span>
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
  const canWorker = Boolean(st.telegram?.enabled && st.telegram?.worker);
  const result = s.result
    ? (() => {
        const [kind, label] = VERDICT_BADGE[s.result.verdict] ?? VERDICT_BADGE.unknown;
        // Главная страница открывается, а музыка или видео на сайте — нет: такой сайт можно добавить вручную.
        const force =
          s.result.verdict === 'works'
            ? `<p class="result-text">Если на сайте не играет музыка или не грузится видео, добавьте его всё равно — вместе с доменами, с которых он их берёт.</p>
               <button class="btn sm primary" data-action="force-site" ${s.busy || st.task ? 'disabled' : ''}>Всё равно добавить</button>`
            : '';
        // Сайт закрыт самим сервисом или по адресу — обход DPI не поможет, а воркер с не российским IP — да.
        const viaWorker =
          ['tunnel', 'unreachable'].includes(s.result.verdict) && canWorker
            ? `<button class="btn sm primary" data-action="site-via-worker" ${s.busy ? 'disabled' : ''}>Открыть через другую страну</button>`
            : '';
        const browsers = s.result.browsers ? '<button class="btn sm primary" data-action="site-browsers">Открывать в браузерах</button>' : '';
        return `<section class="panel result">
          <div class="result-head"><span class="mono strong">${esc(s.result.host)}</span>${badge(kind, label)}</div>
          <p class="result-text">${esc(s.result.message)}</p>
          ${force}${viaWorker}${browsers}
        </section>`;
      })()
    : '';
  // Домены, добавленные вместе с сайтом, показываем под ним: удаляются они тоже вместе.
  const main = sites.filter((site) => !site.parent || !sites.some((p) => p.host === site.parent));
  const rows = main
    .map((site) => {
      const related = sites.filter((c) => c.parent === site.host).map((c) => c.host);
      const subs = [
        site.note ? `<span class="row-sub">${site.note.startsWith('набор: ') ? esc(site.note[0].toUpperCase() + site.note.slice(1)) : `Было: ${esc(site.note)}`}</span>` : '',
        related.length ? `<span class="row-sub wrap">Вместе с ним: <span class="mono">${esc(related.join(', '))}</span></span>` : '',
      ].join('');
      return `<div class="row site-row">
        <span class="row-text"><span class="mono">${esc(site.host)}</span>${subs}</span>
        <button class="icon-btn" data-action="remove-site" data-host="${esc(site.host)}" title="Убрать из списка" aria-label="Убрать ${esc(site.host)} из списка">${icon('trash', 16)}</button>
      </div>`;
    })
    .join('');

  const presets = (st.presets ?? [])
    .map(
      (p) => `<div class="settings-row">
        <span class="row-text"><span class="row-title-sm">${esc(p.title)}</span><span class="row-sub wrap">${esc(p.sub)}</span></span>
        <button class="toggle ${p.on ? 'on' : ''}" role="switch" aria-checked="${Boolean(p.on)}" aria-label="${esc(p.title)}" data-action="preset" data-id="${esc(p.id)}" data-on="${p.on ? '' : '1'}"></button>
      </div>`,
    )
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
      <button type="button" class="btn ghost full" data-action="site-via-worker" ${s.busy || !canWorker ? 'disabled' : ''}>${icon('globe', 16)}<span>Через другую страну</span></button>
      <p class="hint">Для сайтов, закрытых для России самими сервисами: сайт пойдёт через ваш Cloudflare Worker с не российского IP — как VPN только для него.${
        canWorker ? '' : ' Сначала задайте свой воркер в Настройки → Telegram.'
      }</p>
    </form>
    ${result}
    ${
      (st.via_worker ?? []).length
        ? `<section class="stack">
      <div class="section-label"><span>Через другую страну</span><span>${st.via_worker.length}</span></div>
      <div class="chips">${st.via_worker
        .map((h) => {
          const behind = (st.via_worker_blocked ?? []).includes(h);
          return `<button class="chip ${behind ? 'warn' : ''}" data-action="via-worker-remove" data-host="${esc(h)}" title="${behind ? 'Стоит за Cloudflare — через воркер не откроется. Убрать' : 'Убрать'}">${esc(h)} ×</button>`;
        })
        .join('')}</div>
    </section>`
        : ''
    }
    ${
      presets
        ? `<section class="stack">
      <div class="section-label"><span>Готовые наборы</span></div>
      <div class="panel">${presets}</div>
    </section>`
        : ''
    }
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
  const ipset = st.ipset || 'none';
  const ipsetSub =
    {
      none: 'Только сайты из списков. Подходит почти всем.',
      loaded: 'Ещё и адреса хостингов из набора (Hetzner, OVH, Cloudflare и другие). Помогает, если сайты обрываются на загрузке, но может мешать другим сайтам на этих хостингах.',
      any: 'Весь трафик на этих портах, кроме исключений. Крайняя мера: может ломать сайты, которым обход не нужен.',
    }[ipset] ?? '';
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
          <span class="row-text"><span class="row-title-sm">Оформление</span><span class="row-sub">Тема «${esc(themeLabel(state.look.theme))}»${
            state.look.accent || state.look.font || state.look.radius || state.look.scale ? ', свои настройки' : ''
          }</span></span>
          <button class="btn sm" data-action="open" data-screen="look">Настроить</button>
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
          <span class="row-text"><span class="row-title-sm">Чинить автоматически</span><span class="row-sub wrap">Если сервис перестал работать и через минуту это подтвердилось, подобрать стратегию заново. Пока включён VPN, проверки на паузе.</span></span>
          <button class="toggle ${st.auto_fix ? 'on' : ''}" role="switch" aria-checked="${st.auto_fix}" aria-label="Чинить автоматически" data-action="auto-fix"></button>
        </div>
        <div class="settings-row column">
          <span class="row-text"><span class="row-title-sm">Пауза</span><span class="row-sub wrap">${
            st.paused_until
              ? `Обход включится сам в ${esc(clockText(st.paused_until))}. Раньше — переключателем на главном экране.`
              : 'Выключить обход на время, например чтобы зайти в банк. Потом он включится сам.'
          }</span></span>
          <div class="button-row">${[[15, '15 минут'], [60, '1 час'], [180, '3 часа']]
            .map(([minutes, label]) => `<button class="btn sm ghost" data-action="pause" data-minutes="${minutes}" ${selecting ? 'disabled' : ''}>${label}</button>`)
            .join('')}</div>
        </div>
        <div class="settings-row column">
          <span class="row-text"><span class="row-title-sm">Игровой режим</span><span class="row-sub wrap">${gameSub}</span></span>
          <div class="seg" role="radiogroup" aria-label="Игровой режим">${modes
            .map(([value, label]) => `<button role="radio" aria-checked="${st.game_mode === value}" class="${st.game_mode === value ? 'on' : ''}" data-action="game-mode" data-mode="${value}" ${selecting ? 'disabled' : ''}>${label}</button>`)
            .join('')}</div>
          ${st.game_mode === 'games' ? gameChips(st) : ''}
        </div>
        <div class="settings-row column">
          <span class="row-text"><span class="row-title-sm">Обход по адресам (IPSet)</span><span class="row-sub wrap">${esc(ipsetSub)}</span></span>
          <div class="seg" role="radiogroup" aria-label="Обход по адресам">${[['none', 'Выкл'], ['loaded', 'Хостинги'], ['any', 'Все адреса']]
            .map(([value, label]) => `<button role="radio" aria-checked="${ipset === value}" class="${ipset === value ? 'on' : ''}" data-action="ipset" data-mode="${value}" ${selecting ? 'disabled' : ''}>${label}</button>`)
            .join('')}</div>
        </div>
        <div class="settings-row column">
          <span class="row-text"><span class="row-title-sm">Стратегия</span><span class="row-sub wrap">Подбор проверяет все стратегии и оставляет лучшую. Можно выбрать и вручную. Конфиги FI: STABLE — на каждый день, LIGHT — меньше нагрузки, для игр, HARD — когда другие не проходят, SOCIAL — соцсети и RuTracker без добавления в список, DISORDER и SEQOVL — для других провайдеров (SEQOVL — без поддельных пакетов).</span></span>
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
          <span class="row-text"><span class="row-title-sm">Публичный DNS</span><span class="row-sub wrap">Адреса сайтов спрашивать у Cloudflare (1.1.1.1) и Яндекса (77.88.8.8), а не у роутера — если его DNS не отвечает. Выключите — DNS снова от роутера.</span></span>
          <button class="toggle ${st.public_dns ? 'on' : ''}" role="switch" aria-checked="${Boolean(st.public_dns)}" aria-label="Публичный DNS" data-action="public-dns-toggle"></button>
        </div>
        <div class="settings-row">
          <span class="row-text"><span class="row-title-sm">Выключить QUIC в браузерах</span><span class="row-sub wrap">YouTube и сайты Google пойдут по TCP — обход держит его надёжнее, чем QUIC. Chrome, Edge, Brave и Яндекс покажут «управляется организацией» — это из-за этой настройки. Перезапустите браузер.</span></span>
          <button class="toggle ${st.browser_quic_off ? 'on' : ''}" role="switch" aria-checked="${Boolean(st.browser_quic_off)}" aria-label="Выключить QUIC в браузерах" data-action="quic-off"></button>
        </div>
        <div class="settings-row">
          <span class="row-text"><span class="row-title-sm">Выключать QUIC самому</span><span class="row-sub wrap">Если QUIC у YouTube стабильно не проходит, FI выключит его вместо подбора стратегии</span></span>
          <button class="toggle ${st.quic_auto ? 'on' : ''}" role="switch" aria-checked="${Boolean(st.quic_auto)}" aria-label="Выключать QUIC самому" data-action="quic-auto"></button>
        </div>
        <div class="settings-row column">
          <span class="row-text"><span class="row-title-sm">Свои стратегии</span><span class="row-sub wrap">Из .bat другого набора zapret или файла с аргументами winws. FI пропустит только безопасные параметры и файлы из своего набора; свои стратегии участвуют в подборе.</span></span>
          ${
            (st.custom_strategies ?? []).length
              ? `<div class="chips">${st.custom_strategies
                  .map(
                    (name) =>
                      `<button class="chip" data-action="custom-remove" data-name="${esc(name)}" title="Удалить" ${st.task ? 'disabled' : ''}>${esc(strategyLabel(name))} ×</button>`,
                  )
                  .join('')}</div>`
              : ''
          }
          <div class="button-row"><button class="btn sm" data-action="custom-add" ${st.task || !st.strategies ? 'disabled' : ''}>Добавить из файла…</button></div>
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
            tg.enabled
              ? `<span class="mono">${esc(tg.address)}</span>${tg.error ? ' · не запущен' : tg.route ? ` · ${esc(routeText(tg.route))}` : ''}${
                  tg.edge && !tg.error ? ` · вход Cloudflare ${esc(tg.edge)}` : ''
                }`
              : 'Выключен'
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
              <div class="settings-row">
                <span class="row-text"><span class="row-title-sm">Мини-приложения</span><span class="row-sub wrap">${
                  tg.mini_apps_error
                    ? esc(tg.mini_apps_error)
                    : tg.worker_outdated
                    ? 'Код воркера устарел — мини-приложения через него не откроются. Обновите его в разделе «Cloudflare» ниже.'
                    : `Сайты Telegram для мини-приложений и ботов — через FI. Если провайдер закрыл их адреса, нужен свой Cloudflare Worker${
                        tg.worker ? '' : ' — раздел «Cloudflare» ниже'
                      }. После включения перезапустите Telegram.`
                }</span></span>
                <button class="toggle ${tg.mini_apps ? 'on' : ''}" role="switch" aria-checked="${Boolean(tg.mini_apps)}" aria-label="Мини-приложения Telegram" data-action="tg-mini-apps"></button>
              </div>`
            : ''
        }
      </div>
    </section>
    <section class="stack">
      <div class="section-label"><span>Cloudflare</span></div>
      <div class="panel">
        <div class="settings-row">
          <span class="row-text"><span class="row-title-sm">Свой воркер</span><span class="row-sub wrap">${esc(cfSummary(st))}</span></span>
          <button class="btn sm ${tg.worker ? '' : 'primary'}" data-action="open" data-screen="cloudflare">${tg.worker ? 'Открыть' : 'Настроить'}</button>
        </div>
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

// ─── Cloudflare ───────────────────────────────────────────────────────────
// Свой воркер: мастер настройки из трёх шагов, расход бесплатного тарифа и всё, что идёт через
// воркер, — ролики YouTube, закрытые провайдером, сайты, недоступные из России, и программы.

const CF_REGIONS = [['de', 'Германия'], ['nl', 'Нидерланды'], ['us', 'США']];
const CF_STEPS = {
  token: 'Проверяем токен…',
  upload: 'Загружаем воркер в Cloudflare…',
  wait: 'Ждём, пока воркер начнёт отвечать, — до минуты…',
  check: 'Подключаем воркер к FI…',
};
const regionName = (id) => CF_REGIONS.find(([v]) => v === id)?.[1] ?? '';
const count = (n) => Number(n ?? 0).toLocaleString('ru-RU');
// Токен Cloudflare — 40 и больше латинских букв, цифр, «_» и «-».
const looksLikeToken = (s) => /^[A-Za-z0-9_-]{40,200}$/.test(s);

// cfWizardOpen — показывать мастер, а не состояние воркера: воркера ещё нет, человек сам открыл
// настройку заново или она идёт прямо сейчас.
function cfWizardOpen(st) {
  const setup = st.cloudflare?.setup;
  return state.cf.wizard || !st.telegram?.worker || Boolean(setup?.busy && !setup.update);
}

function cloudflareView(st) {
  const wizard = cfWizardOpen(st);
  return `${subHeader('Cloudflare')}
    ${errorLine()}
    ${wizard ? cfWizard(st) : `${cfStatus(st)}${cfYouTube(st)}${cfSites(st)}${cfPrograms(st)}`}`;
}

// cfWizard — три шага: аккаунт, токен, «Создать воркер». Дальше служба всё делает сама.
function cfWizard(st) {
  const cf = st.cloudflare ?? {};
  const tg = st.telegram ?? {};
  const setup = cf.setup;
  const busy = Boolean(setup?.busy);
  const s = state.cf;
  const error = s.error || (setup && !setup.busy ? setup.error : '') || '';
  const off = busy ? 'disabled' : '';
  return `<section class="stack onboarding compact">
      <span class="eyebrow">${tg.worker ? 'Настроить заново' : 'Три шага · около двух минут'}</span>
      <h1 class="display">Свой сервер в Cloudflare</h1>
      <p class="lead">Бесплатный личный воркер. Через него FI ведёт Telegram, когда его адреса закрыты, сайты, недоступные из России, и ролики YouTube, которые закрыл провайдер. Банковская карта не нужна.</p>
      ${s.hint ? `<p class="notice-line">${icon('check', 14, 2)}<span>${esc(s.hint)}</span></p>` : ''}
    </section>
    <ol class="steps">
      <li class="step">
        <span class="step-num">1</span>
        <div class="step-body">
          <span class="row-title-sm">Аккаунт Cloudflare</span>
          <span class="row-sub wrap">Нужны только почта и пароль. Аккаунт уже есть — просто войдите. После регистрации подтвердите почту: Cloudflare пришлёт письмо со ссылкой.</span>
          <div class="button-row"><button class="btn sm" data-action="cf-open" data-page="signup">Открыть Cloudflare</button></div>
        </div>
      </li>
      <li class="step">
        <span class="step-num">2</span>
        <div class="step-body">
          <span class="row-title-sm">Токен для FI</span>
          <span class="row-sub wrap">Это разрешение создать воркер в вашем аккаунте. Откроется страница с уже заполненной формой — менять в ней ничего не нужно:</span>
          <ol class="mini-steps">
            <li>внизу страницы нажмите <b>Continue to summary</b></li>
            <li>затем <b>Create Token</b></li>
            <li>и <b>Copy</b> — токен показывают один раз</li>
          </ol>
          <div class="button-row"><button class="btn sm" data-action="cf-open" data-page="token">Создать токен</button></div>
          <button class="link-btn" data-action="cf-flag" data-name="formHelp">${s.formHelp ? 'Скрыть подсказку' : 'Форма открылась пустой?'}</button>
          ${
            s.formHelp
              ? '<span class="row-sub wrap">На странице API Tokens нажмите <b>Create Token</b>, найдите шаблон <b>Edit Cloudflare Workers</b> и нажмите <b>Use template</b> — дальше те же три кнопки. Если Cloudflare просит войти — войдите и нажмите «Создать токен» ещё раз.</span>'
              : ''
          }
        </div>
      </li>
      <li class="step">
        <span class="step-num">3</span>
        <div class="step-body">
          <span class="row-title-sm">Вставьте токен</span>
          <form class="stack-sm" data-form="cf-setup">
            <div class="input-row">
              <label class="input"><input id="cf-token-input" type="password" autocomplete="off" spellcheck="false" placeholder="токен из Cloudflare" value="${esc(s.token)}" ${off}></label>
              <button class="btn sm" type="button" data-action="cf-paste" ${off}>Вставить</button>
            </div>
            <label class="field"><span class="row-sub">Страна воркера — её увидят сайты</span>
              <select class="select" id="cf-region" ${off}>${CF_REGIONS.map(([v, l]) => `<option value="${v}" ${s.region === v ? 'selected' : ''}>${l}${v === 'de' ? ' — советуем' : ''}</option>`).join('')}</select>
            </label>
            <label class="check"><input type="checkbox" id="cf-remember" ${s.remember ? 'checked' : ''} ${off}><span>Запомнить токен на этом компьютере: FI сам обновит воркер, когда выйдет новая версия. Токен лежит в папке, доступной только администратору.</span></label>
            ${busy ? `<div class="busy-line">${spinner()}<span>${esc(CF_STEPS[setup.step] ?? 'Настраиваем…')}</span></div>` : ''}
            ${error ? `<p class="field-error">${esc(error)}</p>` : ''}
            <button class="btn primary full" type="submit" ${off}>${busy ? 'Создаём воркер…' : 'Создать воркер'}</button>
          </form>
        </div>
      </li>
    </ol>
    ${
      tg.worker
        ? `<button class="btn ghost full" data-action="cf-wizard-close" ${off}>Отмена</button>`
        : `<button class="link-btn" data-action="cf-flag" data-name="manual">${s.manual ? 'Скрыть ручную настройку' : 'У меня уже есть воркер FI'}</button>${s.manual ? `<div class="panel">${cfManualRows(st)}</div>` : ''}`
    }`;
}

// cfManualRows — ручная настройка: адрес готового воркера и его код для вставки в Cloudflare.
function cfManualRows(st) {
  const tg = st.telegram ?? {};
  const cf = st.cloudflare ?? {};
  return `<div class="settings-row column">
      <span class="row-text"><span class="row-title-sm">Адрес воркера</span><span class="row-sub wrap">Если воркер создан руками в панели Cloudflare. Пусто — без воркера.</span></span>
      <form class="input-row" data-form="worker">
        <label class="input"><input id="worker-input" name="host" autocomplete="off" spellcheck="false" placeholder="fi-tg.имя.workers.dev" value="${esc(state.worker ?? tg.worker ?? '')}"></label>
        <button class="btn sm" type="submit">Сохранить</button>
      </form>
    </div>
    <div class="settings-row column">
      <span class="row-text"><span class="row-title-sm">Код воркера</span><span class="row-sub wrap">Для вставки руками: в Cloudflare откройте воркер → Edit code → сотрите всё, вставьте → Deploy, затем «Проверить» здесь.</span></span>
      <div class="button-row">
        <button class="btn sm" data-action="worker-copy">Скопировать код</button>
        <button class="btn sm ghost" data-action="cf-open" data-page="dashboard">Открыть панель Cloudflare</button>
      </div>
    </div>
    ${
      cf.has_token
        ? `<div class="settings-row">
      <span class="row-text"><span class="row-title-sm">Сохранённый токен</span><span class="row-sub wrap">С ним FI сам обновляет воркер. Сотрёте — воркер продолжит работать, но обновлять его придётся руками.</span></span>
      <button class="btn sm ghost" data-action="cf-forget">Стереть</button>
    </div>`
        : ''
    }`;
}

// cfStatus — воркер настроен: работает ли, сколько осталось от суточного лимита, что с обновлением.
function cfStatus(st) {
  const tg = st.telegram ?? {};
  const cf = st.cloudflare ?? {};
  const s = state.cf;
  const setup = cf.setup;
  const updating = Boolean(setup?.busy && setup.update);
  const limit = cf.limit || 100000;
  const share = Math.min(1, (cf.requests ?? 0) / limit);
  const limited = Boolean(cf.limited_until);

  let [kind, label] = ['ok', 'Работает'];
  if (limited) [kind, label] = ['fail', 'Лимит исчерпан'];
  else if (!tg.enabled) [kind, label] = ['off', 'Выключен'];
  else if (updating) [kind, label] = ['check', 'Обновляется'];
  else if (tg.worker_outdated || tg.worker_key_ok === false) [kind, label] = ['warn', 'Нужно обновить'];
  else if (tg.worker_update) [kind, label] = ['warn', 'Есть обновление'];

  const facts = [regionName(cf.region), cf.version ? `код версии ${cf.version}` : '', tg.edge ? `вход ${tg.edge}` : ''].filter(Boolean).join(' · ');
  const notes = [];
  if (limited) {
    notes.push(`<p class="note fail">Бесплатному воркеру Cloudflare даёт ${count(limit)} запросов в сутки, и на сегодня они кончились. Воркер снова заработает в <b>${esc(clockText(cf.limited_until))}</b>. Telegram уже идёт запасным путём; сайты через воркер и закрытые ролики YouTube до тех пор не откроются.</p>`);
  } else if (cf.saving) {
    notes.push('<p class="note warn">Израсходовано больше 80% суточного лимита. Telegram пока идёт запасным путём, чтобы запросов хватило сайтам и видео.</p>');
  }
  if ((cf.cuts ?? 0) >= 3) {
    notes.push(`<p class="note warn">Cloudflare оборвал ${count(cf.cuts)} ${plural(cf.cuts, 'загрузку', 'загрузки', 'загрузок')} за последние десять минут. Бесплатному воркеру не хватает процессорного времени на долгое видео и большие файлы — это предел тарифа, а не поломка. Переписка и обычные сайты идут как прежде; оборванное видео плеер обычно догружает сам, а для долгого просмотра надёжнее VPN.</p>`);
  }
  if (!tg.enabled) {
    notes.push('<p class="note warn">Воркер работает вместе с прокси для Telegram, а он выключен: Настройки → Telegram.</p>');
  }
  if (cf.token_error) {
    notes.push(`<p class="note warn">${esc(cf.token_error)}: без него FI не сможет обновлять воркер сам.</p>`);
  }
  if (setup && !setup.busy && setup.update && setup.error) {
    notes.push(`<p class="note fail">Обновить воркер не удалось: ${esc(setup.error)}</p>`);
  }
  if (tg.worker_outdated || tg.worker_key_ok === false) {
    notes.push('<p class="note warn">В воркере старый код или нет ключа FI: сайты через воркер и мини-приложения через него не откроются. Обновите код.</p>');
  } else if (tg.worker_update && !updating) {
    notes.push(
      cf.has_token && !cf.token_error
        ? '<p class="note">Вышел новый код воркера: он тратит меньше процессорного времени Cloudflare, и загрузки обрываются реже. FI обновит его сам — или нажмите «Обновить код».</p>'
        : '<p class="note warn">Вышел новый код воркера: он тратит меньше процессорного времени Cloudflare, и загрузки обрываются реже. Нажмите «Обновить код» — понадобится токен (шаги 2 и 3 мастера), и дальше FI будет обновлять воркер сам.</p>',
    );
  }

  const needsUpdate = tg.worker_update || tg.worker_outdated || tg.worker_key_ok === false;
  return `<section class="stack">
      <div class="panel">
        <div class="settings-row column">
          <div class="status-head">
            <span class="row-text"><span class="row-title-sm">Свой воркер</span><span class="row-sub mono" title="${esc(tg.worker)}">${esc(tg.worker)}</span></span>
            ${badge(kind, label)}
          </div>
          ${facts ? `<span class="row-sub">${esc(facts)}</span>` : ''}
          <div class="field">
            <div class="meter ${limited ? 'fail' : cf.saving ? 'warn' : ''}"><span style="width: ${Math.round(share * 100)}%"></span></div>
            <span class="row-sub wrap">Сегодня ${count(cf.requests)} из ${count(limit)} запросов${cf.reset_at ? ` · счётчик обнулится в ${esc(clockText(cf.reset_at))}` : ''}</span>
          </div>
          ${notes.join('')}
          ${updating ? `<div class="busy-line">${spinner()}<span>${esc(CF_STEPS[setup.step] ?? 'Обновляем…')}</span></div>` : ''}
          <div class="button-row">
            ${needsUpdate ? `<button class="btn sm primary" data-action="cf-update" ${updating ? 'disabled' : ''}>Обновить код</button>` : ''}
            <button class="btn sm" data-action="worker-recheck" ${updating ? 'disabled' : ''}>Проверить</button>
            <button class="btn sm ghost" data-action="cf-wizard" ${updating ? 'disabled' : ''}>Настроить заново</button>
          </div>
          ${noticeLine()}
          <button class="link-btn" data-action="cf-flag" data-name="manual">${s.manual ? 'Скрыть ручную настройку' : 'Ручная настройка'}</button>
        </div>
        ${s.manual ? cfManualRows(st) : ''}
      </div>
    </section>`;
}

// cfYouTube — ролики с серверов Google, которые провайдер закрыл по адресу.
function cfYouTube(st) {
  const tg = st.telegram ?? {};
  const sub = st.vpn
    ? 'Сейчас включён VPN — YouTube ведёт он. Когда VPN выключат, FI снова поведёт закрытые серверы через воркер.'
    : st.youtube_blocked && !st.youtube_via_worker
      ? 'Провайдер сейчас закрывает серверы Google со свежими и редкими роликами — обход их не откроет. Включите, и FI поведёт их через воркер; в Windows появится сценарий прокси только для них.'
      : 'Свежие и редкие ролики YouTube отдаёт с серверов Google, которые провайдер может закрыть целиком. FI ведёт их через воркер (сценарий прокси в Windows — только для них), остальное видео идёт напрямую.';
  return `<section class="stack">
      <div class="section-label"><span>YouTube</span></div>
      <div class="panel">
        <div class="settings-row">
          <span class="row-text"><span class="row-title-sm">Ролики, закрытые провайдером</span><span class="row-sub wrap">${esc(sub)}</span></span>
          <button class="toggle ${st.youtube_via_worker ? 'on' : ''}" role="switch" aria-checked="${Boolean(st.youtube_via_worker)}" aria-label="Ролики YouTube, закрытые провайдером" data-action="youtube-via-worker" ${!tg.enabled || !tg.worker ? 'disabled' : ''}></button>
        </div>
      </div>
    </section>`;
}

// cfSites — сайты, которые закрыли для России сами сервисы: они идут через воркер.
function cfSites(st) {
  const tg = st.telegram ?? {};
  const sites = st.via_worker ?? [];
  const ready = tg.enabled && tg.worker;
  // Сайт, который сам стоит за Cloudflare, воркер открыть не может: он идёт напрямую.
  const blocked = (st.via_worker_blocked ?? []).filter((h) => sites.includes(h));
  return `<section class="stack">
      <div class="section-label"><span>Сайты через воркер</span></div>
      <div class="panel">
        <div class="settings-row column">
          <span class="row-text"><span class="row-title-sm">Сайты, закрытые для России самими сервисами</span><span class="row-sub wrap">Например, SoundCloud («Not available in Russian Federation»): обход тут не поможет, сервис смотрит на страну. Через воркер сайт видит не российский адрес.</span></span>
          ${
            sites.length
              ? `<div class="chips">${sites.map((h) => `<button class="chip ${blocked.includes(h) ? 'warn' : ''}" data-action="via-worker-remove" data-host="${esc(h)}" title="${blocked.includes(h) ? 'Стоит за Cloudflare — через воркер не откроется. Убрать' : 'Убрать'}">${esc(h)} ×</button>`).join('')}</div>`
              : ''
          }
          ${
            blocked.length
              ? `<p class="note warn">${blocked.length === 1 ? `Сайт <b>${esc(blocked[0])}</b> сам стоит` : `Сайты <b>${esc(blocked.join(', '))}</b> сами стоят`} за Cloudflare, а воркер Cloudflare к таким не подключается: ${blocked.length === 1 ? 'он идёт' : 'они идут'} напрямую, как без FI. Сменить для ${blocked.length === 1 ? 'него' : 'них'} страну поможет только VPN.</p>`
              : ''
          }
          <form class="input-row" data-form="via-worker">
            <label class="input ${state.viaWorkerError ? 'is-error' : ''}"><input id="via-worker-input" name="host" autocomplete="off" spellcheck="false" placeholder="soundcloud.com" value="${esc(state.viaWorker ?? '')}"></label>
            <button class="btn sm" type="submit" ${ready ? '' : 'disabled'}>Добавить</button>
          </form>
          ${state.viaWorkerError ? `<p class="field-error">${esc(state.viaWorkerError)}</p>` : ''}
        </div>
        <div class="settings-row">
          <span class="row-text"><span class="row-title-sm">Открывать их в браузерах</span><span class="row-sub wrap">FI ставит в Windows сценарий настройки прокси: через FI идут только эти сайты и закрытые серверы YouTube, остальное — напрямую. Пока включён VPN, FI сценарий убирает, а после возвращает сам. Без этого сайты из списка идут через воркер только в Telegram.</span></span>
          <button class="toggle ${st.via_worker_browsers ? 'on' : ''}" role="switch" aria-checked="${Boolean(st.via_worker_browsers)}" aria-label="Открывать сайты через воркер в браузерах" data-action="via-worker-browsers" ${!sites.length && !st.youtube_via_worker ? 'disabled' : ''}></button>
        </div>
      </div>
    </section>`;
}

// programAsk — программа уже работает: о прокси она узнаёт только при запуске, поэтому спрашиваем,
// закрыть ли её. Вопрос стоит там, где человек нажал «Запустить», — искать его не приходится.
function programAsk(a) {
  return `<p class="note warn">Программа «${esc(a.name)}» уже работает, а о прокси она узнаёт только при запуске. Закрыть её и запустить заново через Cloudflare? Несохранённое в ней пропадёт.</p>
    <div class="button-row">
      <button class="btn sm primary" data-action="program-restart" data-path="${esc(a.path)}">Закрыть и запустить</button>
      <button class="btn sm ghost" data-action="program-cancel">Отмена</button>
    </div>`;
}

// cfPrograms — программы, которые FI запускает так, что их интернет идёт через воркер.
function cfPrograms(st) {
  const p = st.programs ?? {};
  const apps = p.apps ?? [];
  const s = state.programs;
  // Итог запуска стоит в строке той программы, которую запускали: список бывает длинным, и
  // сообщение над ним осталось бы за кромкой окна.
  const said = [
    s.notice ? `<p class="notice-line">${icon('check', 14, 2)}<span>${esc(s.notice)}</span></p>` : '',
    s.error ? `<p class="field-error">${esc(s.error)}</p>` : '',
  ].join('');
  const inRow = apps.some((a) => a.path === s.path);
  const rows = apps
    .map(
      (a) => `<div class="settings-row column">
          <div class="app-row">
            <span class="row-text"><span class="row-title-sm">${esc(a.name)}</span><span class="row-sub" title="${esc(a.path)}">${esc(a.path)}</span></span>
            <button class="btn sm primary" data-action="program-run" data-path="${esc(a.path)}" ${s.busy ? 'disabled' : ''}>Запустить</button>
            <button class="icon-btn" data-action="program-remove" data-path="${esc(a.path)}" title="Убрать из списка" aria-label="Убрать ${esc(a.name)} из списка">${icon('trash', 16)}</button>
          </div>
          ${s.confirm === a.path ? programAsk(a) : ''}
          ${s.path === a.path ? said : ''}
        </div>`,
    )
    .join('');
  const notes = [];
  if (p.error) notes.push(`<p class="note warn">${esc(p.error)}</p>`);
  else if (apps.length && p.old_worker) notes.push('<p class="note warn">В воркере старый код: программам он открывает только порт 443. Обновите код воркера выше — тогда откроются и остальные порты.</p>');
  if (!inRow) notes.push(said);
  if (apps.length && p.proxy && (p.conns || p.failed)) {
    const viaWorker = Math.max(0, (p.conns ?? 0) - (p.direct ?? 0));
    notes.push(
      `<p class="note">Через Cloudflare прошло ${count(viaWorker)} ${plural(viaWorker, 'соединение', 'соединения', 'соединений')} программ${
        p.direct ? `, напрямую — ${count(p.direct)} (сайты за Cloudflare)` : ''
      }${p.failed ? `, не открылось ${count(p.failed)}${p.last_error ? ` — ${esc(p.last_error)}` : ''}` : ''}.</p>`,
    );
  }
  return `<section class="stack">
      <div class="section-label"><span>Программы через Cloudflare</span></div>
      <div class="panel">
        <div class="settings-row column">
          <span class="row-text"><span class="row-title-sm">Запуск программы через воркер</span><span class="row-sub wrap">FI запускает программу так, что её интернет идёт через ваш воркер — с не российского адреса. Подходит программам, которые умеют работать через прокси: браузеры, Spotify и другие на Chromium и Electron, многие лаунчеры и утилиты. Сайты, которые сами стоят за Cloudflare (Discord, ChatGPT), воркер открыть не может — к ним программа пойдёт напрямую. Игры и звонки ходят по UDP — их так не провести.</span></span>
          <div class="button-row"><button class="btn sm" data-action="program-add" ${s.busy ? 'disabled' : ''}>Добавить программу…</button></div>
          ${notes.join('')}
        </div>
        ${rows}
      </div>
    </section>`;
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
let shownScreen = ''; // какой экран уже показан: при смене он появляется с анимацией
let enteredAt = 0; // когда он показан: от этого времени считаются анимации (--since в app.css)

// appearKey — по чему узнать итог проверки или строку сообщения: появившиеся только что выезжают.
const appearKey = (el) => `${el.className.replace(' appear', '')}|${el.textContent}`;

const FOCUS_KEYS = ['action', 'screen', 'mode', 'host', 'service', 'fix', 'game', 'path', 'value', 'name'];

// Пока кнопка мыши нажата, окно не перерисовывается: перерисовка заменяет все элементы, и щелчок,
// начатый на старой кнопке, пропал бы — человек нажал, а ничего не случилось. Отложенная
// перерисовка проходит сразу после отпускания. Срок нужен на случай, когда отпускание до окна не
// дошло (окно потащили за заголовок): без него оно перестало бы обновляться.
const PRESS_MAX = 1200;
let pressTimer = 0;
let renderHeld = false;

function pressStart() {
  clearTimeout(pressTimer);
  pressTimer = setTimeout(pressEnd, PRESS_MAX);
}

function pressEnd() {
  if (!pressTimer) return;
  clearTimeout(pressTimer);
  pressTimer = 0;
  if (!renderHeld) return;
  renderHeld = false;
  setTimeout(render, 0); // после click: его обработчик перерисует окно сам, если что-то изменил
}

document.addEventListener('pointerdown', pressStart, true);
document.addEventListener('pointerup', pressEnd, true);
document.addEventListener('pointercancel', pressEnd, true);
window.addEventListener('blur', pressEnd);

function render() {
  if (pressTimer) {
    renderHeld = true;
    return;
  }
  const html = view();
  if (html === lastHTML) return; // опрос без изменений не сбивает фокус и анимации
  lastHTML = html;

  const active = document.activeElement;
  const focus = active && root.contains(active)
    ? { id: active.id, data: FOCUS_KEYS.map((k) => active.dataset?.[k]), start: active.selectionStart, end: active.selectionEnd }
    : null;
  // Новый экран выезжает снизу (анимации в app.css; при выключенных анимациях класс ничего не делает).
  // Перерисовка того же экрана заменяет все элементы, поэтому анимации считаются от времени его
  // показа: иначе каждое обновление (новые цифры, ход подбора) проигрывало бы появление заново.
  const screen = state.unavailable || !state.status ? '' : `${state.screen}:${Boolean(state.status.strategy)}`;
  const entering = screen !== shownScreen;
  if (entering) {
    shownScreen = screen;
    enteredAt = performance.now();
  }
  const had = entering ? null : new Set([...root.querySelectorAll('.notice-line, .result')].map(appearKey));
  root.style.setProperty('--since', `${Math.round(performance.now() - enteredAt)}ms`);
  root.classList.add('screen-enter');
  root.innerHTML = html;
  if (had) {
    for (const el of root.querySelectorAll('.notice-line, .result')) {
      if (!had.has(appearKey(el))) el.classList.add('appear');
    }
  }
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
  if (state.screen === 'cloudflare' && screen !== 'cloudflare') leaveCloudflare();
  if (screen !== state.screen) state.programs = idlePrograms(); // итог запуска программы — до ухода с экрана
  if (screen === 'look' || screen === 'cloudflare') state.from = state.screen === screen ? state.from : state.screen;
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
    const prev = state.status;
    state.status = await api('status');
    state.unavailable = false;
    noteSetup(prev, state.status);
  } catch (err) {
    handleError(err);
  }
  render();
}

async function command(method, params) {
  state.message = '';
  try {
    const result = await api(method, params);
    if (result && typeof result === 'object' && 'enabled' in result) {
      const prev = state.status;
      state.status = result;
      noteSetup(prev, result);
    }
    state.unavailable = false;
  } catch (err) {
    handleError(err);
  }
  render();
  showError();
  schedule();
}

// showError прокручивает окно к сообщению об ошибке: оно стоит вверху экрана, а кнопку человек
// мог нажать далеко внизу — и тогда казалось бы, что ничего не случилось.
function showError() {
  if (state.message) document.querySelector('main > .field-error')?.scrollIntoView({ block: 'nearest' });
}

// ─── Cloudflare: действия ─────────────────────────────────────────────────

// cfSummary — одна строка про воркер для экрана настроек.
function cfSummary(st) {
  const tg = st.telegram ?? {};
  const cf = st.cloudflare ?? {};
  if (!tg.worker) {
    return 'Личный бесплатный сервер: через него идут Telegram, когда его адреса закрыты, сайты, недоступные из России, закрытые ролики YouTube и программы. Настройка — три шага.';
  }
  if (cf.limited_until) return `Суточный лимит исчерпан — заработает в ${clockText(cf.limited_until)}`;
  let head = 'Работает';
  if (tg.worker_outdated || tg.worker_key_ok === false) head = 'Нужно обновить код';
  else if (tg.worker_update) head = 'Есть обновление кода';
  return `${head} · сегодня ${count(cf.requests)} из ${count(cf.limit || 100000)} запросов`;
}

// leaveCloudflare — уходим с экрана Cloudflare: итог настройки уже показан, а токен в памяти
// окна держать незачем.
function leaveCloudflare() {
  const setup = state.status?.cloudflare?.setup;
  if (setup && !setup.busy) api('clear_worker_setup').catch(() => {});
  state.cf = { ...state.cf, wizard: false, token: '', error: '', hint: '', formHelp: false };
  state.programs = idlePrograms();
  state.viaWorkerError = '';
}

async function cfOpen(page) {
  const data = await postJSON('/app/cloudflare/open', { page });
  if (!data || data.error) {
    state.cf.error = data?.error ?? 'Не удалось открыть браузер';
    render();
  }
}

// cfPaste вставляет токен из буфера обмена. quiet — буфер проверяется сам, когда человек вернулся
// из браузера: подставляем, только если там точно токен, и ни о чём не спрашиваем.
async function cfPaste(quiet = false) {
  const data = await postJSON('/app/clipboard');
  const text = (data?.text ?? '').trim();
  if (!looksLikeToken(text)) {
    if (!quiet) {
      state.cf.error = text
        ? 'В буфере обмена не токен Cloudflare. Скопируйте его кнопкой Copy на странице Cloudflare.'
        : 'Буфер обмена пуст: скопируйте токен кнопкой Copy на странице Cloudflare.';
      render();
    }
    return;
  }
  if (state.cf.token === text) return;
  state.cf.token = text;
  state.cf.error = '';
  state.cf.hint = quiet ? 'Токен вставлен из буфера обмена — осталось нажать «Создать воркер».' : '';
  render();
}

// setupWorker отдаёт токен службе: она проверяет его, загружает воркер и подключает к FI.
async function setupWorker() {
  const s = state.cf;
  const token = (document.getElementById('cf-token-input')?.value ?? s.token).trim();
  s.token = token;
  s.hint = '';
  if (!token) {
    s.error = 'Вставьте токен: шаг 2 выше, потом кнопка «Вставить».';
    render();
    return;
  }
  if (!looksLikeToken(token)) {
    s.error = 'Это не похоже на токен Cloudflare: он состоит из 40 латинских букв и цифр. Скопируйте его кнопкой Copy сразу после создания.';
    render();
    return;
  }
  s.error = '';
  try {
    state.status = await api('setup_worker', { token, region: s.region, remember: s.remember });
  } catch (err) {
    handleError(err, (m) => (s.error = m));
  }
  render();
  schedule();
}

// noteSetup следит за настройкой воркера между опросами: закончилась удачно — мастер закрывается,
// а токен из памяти окна убирается.
function noteSetup(prev, cur) {
  const was = prev?.cloudflare?.setup;
  const now = cur?.cloudflare?.setup;
  if (!was?.busy || !now || now.busy || !now.done) return;
  state.cf = { ...state.cf, wizard: false, token: '', error: '', hint: '' };
  if (now.update) state.notice = 'Код воркера обновлён.';
  else if (cur.youtube_blocked && !cur.youtube_via_worker && !cur.vpn) state.notice = 'Готово: воркер создан и подключён. Остался один шаг — включите ниже «Ролики, закрытые провайдером», и YouTube пойдёт через него.';
  else state.notice = 'Готово: воркер создан и подключён. Telegram уже идёт через него; сайты и программы добавляются ниже.';
}

// cfUpdate обновляет код воркера: сохранённым токеном — сразу, без него — через шаги мастера.
function cfUpdate() {
  const cf = state.status?.cloudflare ?? {};
  if (cf.has_token && !cf.token_error) {
    command('update_worker');
    return;
  }
  state.cf = { ...state.cf, wizard: true, error: '', hint: 'Чтобы обновить воркер, FI нужен токен: пройдите шаги 2 и 3. С галочкой «Запомнить» дальше FI будет обновлять воркер сам.' };
  state.programs = idlePrograms();
  render();
}

// ─── Программы через Cloudflare ───────────────────────────────────────────

// showProgram прокручивает окно к строке программы: вопрос или итог запуска удлинили её, и без
// прокрутки они остались бы под кромкой окна.
function showProgram(path) {
  const row = [...document.querySelectorAll('[data-action="program-run"]')].find((el) => el.dataset.path === path);
  (row?.closest('.quick, .settings-row') ?? document.querySelector('.quick'))?.scrollIntoView({ block: 'nearest', behavior: state.look?.motion === false ? 'auto' : 'smooth' });
}

async function programAdd() {
  const s = state.programs;
  Object.assign(s, { busy: true, error: '', notice: '', confirm: '', path: '' });
  render();
  const data = await postJSON('/app/programs/add');
  s.busy = false;
  if (!data) s.error = 'Окно потеряло связь со своей частью на Go';
  else if (data.error) s.error = data.error;
  else if (data.status) {
    state.status = data.status;
    s.path = data.path ?? '';
    s.notice = 'Программа добавлена. Нажмите «Запустить» — она откроется через Cloudflare.';
  }
  render();
  if (s.path && s === state.programs) showProgram(s.path);
}

// programRun запускает программу через прокси службы. Если она уже запущена, сперва спрашиваем:
// прокси программа узнаёт только при запуске, поэтому её придётся закрыть.
async function programRun(path, restart = false) {
  const s = state.programs;
  const name = (state.status?.programs?.apps ?? []).find((a) => a.path === path)?.name ?? 'Программа';
  Object.assign(s, { busy: true, error: '', notice: '', confirm: '', path });
  render();
  const data = await postJSON('/app/programs/run', { path, restart });
  s.busy = false;
  if (!data) s.error = 'Окно потеряло связь со своей частью на Go';
  else if (data.error) s.error = data.error;
  else if (data.running) s.confirm = path;
  else if (data.started) {
    s.notice = data.chromium
      ? `Программа «${name}» запущена через Cloudflare.`
      : `Программа «${name}» запущена. Через Cloudflare пойдёт всё, что она открывает с учётом настроек прокси. Если программа их не читает и её сайты не открываются, добавьте их в «Сайты через воркер».`;
  }
  render();
  if (s === state.programs) showProgram(path);
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

// addViaWorkerSite добавляет сайт из поля на экране Cloudflare. Отказ (сайт стоит за Cloudflare,
// и воркер его не откроет) показывается под полем, а введённое остаётся — можно поправить.
async function addViaWorkerSite() {
  const host = (document.getElementById('via-worker-input')?.value ?? '').trim();
  state.viaWorkerError = '';
  if (!host) {
    state.viaWorkerError = 'Введите адрес сайта, например soundcloud.com';
    render();
    return;
  }
  state.viaWorker = host;
  try {
    const result = await api('add_via_worker', { host });
    if (result && typeof result === 'object' && 'enabled' in result) state.status = result;
    state.viaWorker = null;
  } catch (err) {
    handleError(err, (m) => (state.viaWorkerError = m));
  }
  render();
  document.querySelector('[data-form="via-worker"] + .field-error')?.scrollIntoView({ block: 'nearest' });
  schedule();
}

// addViaWorker пускает сайт через свой воркер — с не российского IP, как VPN только для него.
async function addViaWorker() {
  const s = state.site;
  const host = (s.result?.host || s.input || '').trim();
  if (!host) {
    s.error = 'Введите адрес сайта';
    render();
    return;
  }
  s.busy = true;
  s.error = '';
  render();
  try {
    await api('add_via_worker', { host });
    const browsers = state.status?.via_worker_browsers;
    s.result = {
      host,
      verdict: 'via_worker',
      message: browsers
        ? 'Сайт пойдёт через ваш Cloudflare Worker — с не российского IP. Обновите страницу в браузере.'
        : 'Сайт пойдёт через ваш Cloudflare Worker — с не российского IP. В Telegram — сразу, а в браузерах — когда включите «Открывать в браузерах».',
      browsers: !browsers,
    };
    s.input = '';
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
    case 'look-theme':
      setLook({ theme: el.dataset.value });
      break;
    case 'look-accent':
      setLook({ accent: el.dataset.value });
      break;
    case 'look-radius':
      setLook({ radius: el.dataset.value });
      break;
    case 'look-scale':
      setLook({ scale: Number(el.dataset.value) });
      break;
    case 'look-motion':
      setLook({ motion: !state.look.motion });
      break;
    case 'look-reset':
      setLook({ ...DEFAULT_LOOK, theme: state.look.theme });
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
    case 'public-dns':
      command('set_public_dns', { on: true });
      break;
    case 'public-dns-toggle':
      command('set_public_dns', { on: !state.status?.public_dns });
      break;
    case 'quic-off':
      command('set_browser_quic_off', { on: !state.status?.browser_quic_off });
      break;
    case 'quic-auto':
      command('set_quic_auto', { on: !state.status?.quic_auto });
      break;
    case 'via-worker-browsers':
      command('set_via_worker_browsers', { on: !state.status?.via_worker_browsers });
      break;
    case 'youtube-via-worker-on':
      command('set_youtube_via_worker', { on: true });
      break;
    case 'youtube-via-worker':
      command('set_youtube_via_worker', { on: !state.status?.youtube_via_worker });
      break;
    case 'via-worker-remove':
      command('remove_via_worker', { host: el.dataset.host });
      break;
    case 'worker-copy':
      fileAction('/app/worker/copy', () => 'Код воркера скопирован. В Cloudflare: воркер → Edit code → стереть всё, вставить → Deploy, затем «Проверить» здесь.');
      break;
    case 'worker-recheck':
      command('recheck_worker');
      break;
    case 'cf-open':
      cfOpen(el.dataset.page);
      break;
    case 'cf-paste':
      cfPaste();
      break;
    case 'cf-flag': // раскрыть или скрыть подсказку
      state.cf[el.dataset.name] = !state.cf[el.dataset.name];
      render();
      break;
    case 'cf-wizard':
      state.cf = { ...state.cf, wizard: true, error: '', hint: '' };
      state.programs = idlePrograms();
      state.notice = '';
      render();
      break;
    case 'cf-wizard-close':
      state.cf = { ...state.cf, wizard: false, token: '', error: '', hint: '' };
      render();
      break;
    case 'cf-update':
      cfUpdate();
      break;
    case 'cf-forget':
      command('forget_cf_token');
      break;
    case 'program-add':
      programAdd();
      break;
    case 'program-run':
      programRun(el.dataset.path);
      break;
    case 'program-restart':
      programRun(el.dataset.path, true);
      break;
    case 'program-cancel':
      state.programs.confirm = '';
      render();
      break;
    case 'program-remove':
      state.programs = idlePrograms();
      command('remove_app', { path: el.dataset.path });
      break;
    case 'custom-add':
      fileAction('/app/strategy/import', (d) => (d.name ? `Стратегия «${d.name}» добавлена — её можно выбрать в списке или запустить подбор` : ''));
      break;
    case 'custom-remove':
      command('remove_custom_strategy', { name: el.dataset.name });
      break;
    case 'tg-mini-apps':
      command('set_telegram_mini_apps', { on: !state.status?.telegram?.mini_apps });
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
      // Вложенные экраны возвращают туда, откуда их открыли: Cloudflare — с главного или из настроек.
      go(state.screen === 'look' ? 'settings' : state.screen === 'cloudflare' && state.from === 'settings' ? 'settings' : 'main');
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
    case 'ipset':
      command('set_ipset', { mode: el.dataset.mode });
      break;
    case 'pause':
      command('pause', { minutes: Number(el.dataset.minutes) });
      break;
    case 'preset':
      command('set_preset', { id: el.dataset.id, on: el.dataset.on === '1' });
      break;
    case 'force-site':
      addSite(true);
      break;
    case 'site-via-worker':
      addViaWorker();
      break;
    case 'site-browsers':
      command('set_via_worker_browsers', { on: true });
      if (state.site.result) state.site.result.browsers = false;
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
    case 'app-release':
      postJSON('/app/release').then((data) => {
        if (data?.error) {
          state.message = data.error;
          render();
        }
      });
      break;
    case 'app-auto':
      command('set_app_auto_update', { on: !state.status?.app?.auto_update });
      break;
  }
});

document.addEventListener('submit', (event) => {
  const form = event.target.dataset.form;
  if (form !== 'site' && form !== 'worker' && form !== 'via-worker' && form !== 'cf-setup') return;
  event.preventDefault();
  if (form === 'cf-setup') {
    setupWorker();
    return;
  }
  if (form === 'site') {
    addSite();
    return;
  }
  if (form === 'via-worker') {
    addViaWorkerSite();
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
  if (event.target.id === 'via-worker-input') {
    state.viaWorker = event.target.value;
    if (state.viaWorkerError) {
      state.viaWorkerError = '';
      render();
    }
    return;
  }
  if (event.target.id === 'cf-token-input') {
    state.cf.token = event.target.value; // переживает перерисовку окна, пока воркер не создан
    if (state.cf.error || state.cf.hint) {
      state.cf.error = '';
      state.cf.hint = '';
      render();
    }
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
  const busy = Boolean(state.status?.task) || state.site.busy || Boolean(state.status?.cloudflare?.setup?.busy);
  timer = setTimeout(async () => {
    if (document.visibilityState === 'visible') await refresh();
    schedule();
  }, busy ? 1000 : 4000);
}

// Человек создал токен в браузере и вернулся в окно FI: если в буфере обмена токен, а поле ещё
// пустое, подставляем его сами — одним действием меньше.
window.addEventListener('focus', () => {
  const st = state.status;
  if (state.screen === 'cloudflare' && st && cfWizardOpen(st) && !state.cf.token && !st.cloudflare?.setup?.busy) cfPaste(true);
});

document.addEventListener('change', (event) => {
  if (event.target.id === 'font-select') {
    setLook({ font: event.target.value });
    return;
  }
  if (event.target.id === 'accent-input') {
    setLook({ accent: event.target.value.toUpperCase() });
    return;
  }
  if (event.target.id === 'cf-region') {
    state.cf.region = event.target.value;
    return;
  }
  if (event.target.id === 'cf-remember') {
    state.cf.remember = event.target.checked;
    return;
  }
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

  // «Размер интерфейса» в оформлении — поверх подгонки под ширину окна.
  const zoom = (state.look?.scale || 100) / 100;
  const scale = Math.min(2.4, Math.max(0.72, (width / DESIGN_WIDTH) * zoom));
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

applyLook(readLook());
applyScale();
loadLook();
refresh().then(schedule);
