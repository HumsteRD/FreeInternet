// Генератор артбордов дизайна FI.
// Запуск: node design/build.mjs → design/artboards/*.dc.html и canvas.json.
// Все экраны собираются из одних и тех же токенов и компонентов, поэтому правки
// цвета или геометрии делаются здесь, а не в каждом файле.

import { mkdirSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const OUT = join(dirname(fileURLToPath(import.meta.url)), 'artboards');
mkdirSync(OUT, { recursive: true });

const UI = "'Onest', 'Segoe UI Variable Text', 'Segoe UI', system-ui, sans-serif";
const MONO = "'JetBrains Mono', 'Cascadia Mono', Consolas, monospace";

// ─── Токены ───────────────────────────────────────────────────────────────

const DARK = {
  name: 'dark',
  bg: '#0B0A0D', sunken: '#08070A', panel: '#131116', raised: '#1B1820', hover: '#24202A',
  line: '#26222B', lineStrong: '#36313C',
  text: '#F1EEF3', text2: '#A7A1AD', text3: '#6F6977',
  violet: '#7C4FE0', violetHover: '#8A5FEA', violetPressed: '#6A40CC',
  violetText: '#B294F7', violetTint: 'rgba(154, 108, 240, 0.14)', violetLine: 'rgba(154, 108, 240, 0.45)',
  red: '#E5484D', redText: '#F2787C', redTint: 'rgba(229, 72, 77, 0.14)', redTint2: 'rgba(229, 72, 77, 0.22)', redTint3: 'rgba(229, 72, 77, 0.30)',
  amber: '#EBA945', amberText: '#F0BA62', amberTint: 'rgba(235, 169, 69, 0.14)',
  neutralTint: 'rgba(255, 255, 255, 0.06)', segSel: '#2A2530', knobOff: '#A7A1AD',
  glow: 'radial-gradient(closest-side, rgba(229, 72, 77, 0.17), rgba(140, 92, 242, 0.08) 60%, transparent)',
};

const LIGHT = {
  name: 'light',
  bg: '#F6F4F8', sunken: '#EDEAF0', panel: '#FFFFFF', raised: '#F3F0F5', hover: '#EAE6EE',
  line: '#E6E2EA', lineStrong: '#D4CEDA',
  text: '#16131A', text2: '#5E5865', text3: '#8E8895',
  violet: '#6E3FD8', violetHover: '#7B4EE3', violetPressed: '#5E33C0',
  violetText: '#6437CC', violetTint: 'rgba(110, 63, 216, 0.10)', violetLine: 'rgba(110, 63, 216, 0.40)',
  red: '#D1343A', redText: '#C42B31', redTint: 'rgba(209, 52, 58, 0.10)', redTint2: 'rgba(209, 52, 58, 0.16)', redTint3: 'rgba(209, 52, 58, 0.22)',
  amber: '#C98A2B', amberText: '#9A6412', amberTint: 'rgba(201, 138, 43, 0.14)',
  neutralTint: 'rgba(22, 19, 26, 0.05)', segSel: '#FFFFFF', knobOff: '#FFFFFF',
  glow: 'radial-gradient(closest-side, rgba(229, 72, 77, 0.10), rgba(110, 63, 216, 0.05) 60%, transparent)',
};

const BRAND_RED = '#E5484D';
const BRAND_VIOLET = '#9A6CF0';
const TASKBAR_DARK = '#141216';
const TASKBAR_LIGHT = '#EEEAF1';

// ─── Разметка ─────────────────────────────────────────────────────────────

const kebab = (k) => k.replace(/[A-Z]/g, (m) => '-' + m.toLowerCase());
const st = (o = {}) =>
  Object.entries(o)
    .filter(([, v]) => v !== undefined && v !== null && v !== false && v !== '')
    .map(([k, v]) => `${kebab(k)}: ${v}`)
    .join('; ');
const tag = (name) => (style, ...children) =>
  `<${name} style="${st(style)}">${children.flat(Infinity).filter((c) => c !== undefined && c !== null && c !== false).join('')}</${name}>`;
const div = tag('div');
const span = tag('span');

let uid = 0;
const nextId = (p) => `${p}${++uid}`;

// ─── Иконки: 24-пиксельная сетка, линия 1.75, скруглённые концы ───────────

const P = (d) => `<path d="${d}"></path>`;
const C = (cx, cy, r) => `<circle cx="${cx}" cy="${cy}" r="${r}"></circle>`;
const R = (x, y, w, h, rx) => `<rect x="${x}" y="${y}" width="${w}" height="${h}" rx="${rx}"></rect>`;

const ICONS = {
  power: P('M12 3.5v8') + P('M6.5 6.9a7.6 7.6 0 1 0 11 0'),
  sliders: P('M4 7h9') + P('M17 7h3') + P('M4 17h3') + P('M11 17h9') + C(15, 7, 2) + C(9, 17, 2),
  plus: P('M12 5v14') + P('M5 12h14'),
  pulse: P('M3 12h4l2.5-6 5 12 2.5-6h4'),
  refresh: P('M19.5 10A8 8 0 0 0 5.2 7.2') + P('M4.5 4v4h4') + P('M4.5 14a8 8 0 0 0 14.3 2.8') + P('M19.5 20v-4h-4'),
  check: P('M5 12.5l4.5 4.5L19 7.5'),
  cross: P('M6.5 6.5l11 11') + P('M17.5 6.5l-11 11'),
  alert: P('M12 4.2L2.8 19.5h18.4z') + P('M12 10v4.2') + P('M12 17h.01'),
  info: C(12, 12, 9) + P('M12 11v5.5') + P('M12 7.8h.01'),
  pause: P('M9 6v12') + P('M15 6v12'),
  play: R(3, 5, 18, 14, 3.5) + P('M10 9.3v5.4l4.6-2.7z'),
  headset: P('M4 15.5V12a8 8 0 0 1 16 0v3.5') + R(3, 14, 4, 6, 1.5) + R(17, 14, 4, 6, 1.5),
  bubble: P('M5 4.5h14A1.5 1.5 0 0 1 20.5 6v9a1.5 1.5 0 0 1-1.5 1.5h-9l-5 4v-4A1.5 1.5 0 0 1 3.5 15V6A1.5 1.5 0 0 1 5 4.5z'),
  globe: C(12, 12, 9) + P('M3 12h18') + P('M12 3c2.4 2.6 3.7 5.6 3.7 9s-1.3 6.4-3.7 9c-2.4-2.6-3.7-5.6-3.7-9s1.3-6.4 3.7-9z'),
  gamepad: P('M7.5 7.5h9a4.5 4.5 0 0 1 4.5 4.5v1.2a3.3 3.3 0 0 1-5.9 2L14 13.8h-4l-1.1 1.4a3.3 3.3 0 0 1-5.9-2V12a4.5 4.5 0 0 1 4.5-4.5z') + P('M7.5 10.2v3') + P('M6 11.7h3') + P('M16 10.8h.01') + P('M17.6 12.6h.01'),
  server: R(4, 4, 16, 7, 2) + R(4, 13, 16, 7, 2) + P('M8 7.5h.01') + P('M8 16.5h.01'),
  route: C(6, 18, 2.2) + C(18, 6, 2.2) + P('M8.2 18H15a3 3 0 0 0 0-6H9a3 3 0 0 1 0-6h6.8'),
  plug: P('M9 3.5v4') + P('M15 3.5v4') + P('M6.5 7.5h11V11a5.5 5.5 0 0 1-11 0z') + P('M12 16.5V21'),
  clipboard: R(5.5, 5, 13, 16, 2) + R(9, 3, 6, 4, 1.2),
  search: C(11, 11, 6.5) + P('M16 16l4.5 4.5'),
  trash: P('M4.5 7h15') + P('M9.5 7V4.5h5V7') + P('M6.5 7l.9 12.5h9.2L17.5 7'),
  download: P('M12 4v11') + P('M7 10.5l5 5 5-5') + P('M5 20h14'),
  external: P('M14 4h6v6') + P('M20 4l-8.5 8.5') + P('M18 13.5V19a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h5.5'),
  clock: C(12, 12, 9) + P('M12 7v5l3.5 2'),
  window: R(3.5, 5, 17, 14, 2) + P('M3.5 9h17'),
  exit: P('M10 4H5.5A1.5 1.5 0 0 0 4 5.5v13A1.5 1.5 0 0 0 5.5 20H10') + P('M15 8l4 4-4 4') + P('M19 12H9'),
  moon: P('M19.5 14.5A7.5 7.5 0 1 1 9.5 4.5a6 6 0 0 0 10 10z'),
  scale: P('M4 9V4h5') + P('M20 15v5h-5') + P('M4 4l6 6') + P('M20 20l-6-6'),
  bolt: P('M13 3L5 13.5h6L10 21l8-10.5h-6z'),
  list: P('M9 6h11') + P('M9 12h11') + P('M9 18h11') + P('M4.5 6h.01') + P('M4.5 12h.01') + P('M4.5 18h.01'),
  chevronLeft: P('M14.5 6l-6 6 6 6'),
  chevronRight: P('M9.5 6l6 6-6 6'),
  chevronDown: P('M6 9.5l6 6 6-6'),
  chevronUp: P('M6 14.5l6-6 6 6'),
  more: P('M6 12h.01') + P('M12 12h.01') + P('M18 12h.01'),
  minimize: P('M6 12h12'),
  close: P('M7 7l10 10') + P('M17 7L7 17'),
  spinner: P('M12 3.5a8.5 8.5 0 1 0 8.5 8.5'),
  wifi: P('M3 9a13 13 0 0 1 18 0') + P('M6 12.5a8.5 8.5 0 0 1 12 0') + P('M9 16a4 4 0 0 1 6 0') + P('M12 19.5h.01'),
  volume: P('M4 9.5h3.5L12 5.5v13l-4.5-4H4z') + P('M15.5 9a4 4 0 0 1 0 6') + P('M18 6.5a7.5 7.5 0 0 1 0 11'),
};

function icon(name, size = 20, color = 'currentColor', sw = 1.75) {
  if (!ICONS[name]) throw new Error(`нет иконки ${name}`);
  return `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="${color}" stroke-width="${sw}" stroke-linecap="round" stroke-linejoin="round" style="display: block; flex: none;">${ICONS[name]}</svg>`;
}

// ─── Знак: «П» с просветом, в который проходит свет ───────────────────────

function appIcon(size) {
  if (size <= 16) return mark16();
  if (size <= 48) return markSimple(size);
  const id = nextId('pv');
  return `<svg width="${size}" height="${size}" viewBox="0 0 256 256" style="display: block; flex: none;">` +
    `<defs>` +
    `<linearGradient id="${id}t" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#1B171F"></stop><stop offset="1" stop-color="#08070A"></stop></linearGradient>` +
    `<radialGradient id="${id}g" cx="128" cy="212" r="112" gradientUnits="userSpaceOnUse"><stop offset="0" stop-color="${BRAND_RED}" stop-opacity="0.55"></stop><stop offset="0.42" stop-color="#8C5CF2" stop-opacity="0.22"></stop><stop offset="1" stop-color="#8C5CF2" stop-opacity="0"></stop></radialGradient>` +
    `<linearGradient id="${id}s" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#FF9A86"></stop><stop offset="0.4" stop-color="${BRAND_RED}"></stop><stop offset="1" stop-color="#A93A63"></stop></linearGradient>` +
    `<clipPath id="${id}c"><rect width="256" height="256" rx="56"></rect></clipPath>` +
    `</defs>` +
    `<rect width="256" height="256" rx="56" fill="url(#${id}t)"></rect>` +
    `<g clip-path="url(#${id}c)"><rect x="0" y="96" width="256" height="160" fill="url(#${id}g)"></rect></g>` +
    `<path d="M64 72a20 20 0 0 1 20-20h88a20 20 0 0 1 20 20v132h-54V104h-20v100H64z" fill="#F1EEF3"></path>` +
    `<rect x="118" y="104" width="20" height="100" fill="url(#${id}s)"></rect>` +
    `<rect x="1.5" y="1.5" width="253" height="253" rx="54.5" fill="none" stroke="#FFFFFF" stroke-opacity="0.08" stroke-width="3"></rect>` +
    `</svg>`;
}

function markSimple(size) {
  return `<svg width="${size}" height="${size}" viewBox="0 0 32 32" style="display: block; flex: none;">` +
    `<rect width="32" height="32" rx="7" fill="#0E0C11"></rect>` +
    `<rect x="0.5" y="0.5" width="31" height="31" rx="6.5" fill="none" stroke="#FFFFFF" stroke-opacity="0.1"></rect>` +
    `<path d="M8 9.5A2.5 2.5 0 0 1 10.5 7h11A2.5 2.5 0 0 1 24 9.5V25h-7V13h-2v12H8z" fill="#F1EEF3"></path>` +
    `<rect x="15" y="13" width="2" height="12" fill="${BRAND_RED}"></rect>` +
    `</svg>`;
}

function mark16() {
  return `<svg width="16" height="16" viewBox="0 0 16 16" style="display: block; flex: none;">` +
    `<rect width="16" height="16" rx="3.5" fill="#0E0C11"></rect>` +
    `<path d="M4 3h8v10H9V6H7v7H4z" fill="#F1EEF3" shape-rendering="crispEdges"></path>` +
    `<rect x="7" y="6" width="2" height="7" fill="${BRAND_RED}" shape-rendering="crispEdges"></rect>` +
    `</svg>`;
}

// Значок трея: отдельная геометрия под каждый размер, линии выровнены по пикселям.
const TRAY = {
  16: { b: [3, 2, 13, 14], s: [7, 6, 9], k: [12.5, 3.5, 2.5] },
  20: { b: [4, 3, 16, 17], s: [9, 8, 11], k: [15.5, 4.5, 3] },
  24: { b: [5, 3, 19, 21], s: [11, 9, 13], k: [18.5, 5, 3.5] },
  32: { b: [7, 4, 25, 28], s: [14, 12, 18], k: [24.5, 7, 4.5] },
};

function trayInner(size, state, onLight) {
  const { b: [x1, y1, x2, y2], s: [sx1, sy, sx2], k: [kx, ky, kr] } = TRAY[size];
  const ink = onLight ? '#16131A' : '#F1EEF3';
  const ring = onLight ? TASKBAR_LIGHT : TASKBAR_DARK;
  const dim = onLight ? '#BDB6C4' : '#4A4450';
  const body = (opacity = 1) =>
    `<path d="M${x1} ${y1}H${x2}V${y2}H${sx2}V${sy}H${sx1}V${y2}H${x1}Z" fill="${ink}" fill-opacity="${opacity}" shape-rendering="crispEdges"></path>`;
  const slit = (color) =>
    `<rect x="${sx1}" y="${sy}" width="${sx2 - sx1}" height="${y2 - sy}" fill="${color}" shape-rendering="crispEdges"></rect>`;
  const badge = (color) =>
    `<circle cx="${kx}" cy="${ky}" r="${kr + 1.25}" fill="${ring}"></circle><circle cx="${kx}" cy="${ky}" r="${kr}" fill="${color}"></circle>`;
  switch (state) {
    case 'active': return body() + slit(BRAND_RED);
    case 'select': return body() + slit(BRAND_VIOLET);
    case 'attention': return body() + slit(BRAND_RED) + badge('#EBA945');
    case 'failure': return body() + slit(dim) + badge(BRAND_RED);
    case 'paused': return body(0.5) + slit(dim);
    case 'off': {
      const h = 0.5;
      return `<path d="M${x1 + h} ${y1 + h}H${x2 - h}V${y2 - h}H${sx2 + h}V${sy + h}H${sx1 - h}V${y2 - h}H${x1 + h}Z" fill="none" stroke="${ink}" stroke-opacity="0.7" stroke-width="1"></path>`;
    }
  }
  throw new Error(`нет состояния ${state}`);
}

function tray(size, state, { onLight = false, render = size } = {}) {
  return `<svg width="${render}" height="${render}" viewBox="0 0 ${size} ${size}" style="display: block; flex: none;">${trayInner(size, state, onLight)}</svg>`;
}

function pixelGlyph(size, zoom, state = 'active') {
  const id = nextId('px');
  return `<svg width="${size * zoom}" height="${size * zoom}" viewBox="0 0 ${size} ${size}" style="display: block; flex: none; background: ${TASKBAR_DARK}; border-radius: 6px;">` +
    `<defs><pattern id="${id}" width="1" height="1" patternUnits="userSpaceOnUse"><path d="M1 0H0V1" fill="none" stroke="#FFFFFF" stroke-opacity="0.1" stroke-width="${(1 / zoom).toFixed(3)}"></path></pattern></defs>` +
    trayInner(size, state, false) +
    `<rect width="${size}" height="${size}" fill="url(#${id})"></rect></svg>`;
}

// ─── Компоненты ───────────────────────────────────────────────────────────

const mono = (text, style = {}) => span({ fontFamily: MONO, fontSize: '12px', ...style }, text);

function badge(t, kind, label) {
  const spec = {
    ok: [t.violetText, t.violetTint, 'dot', 'Работает'],
    slow: [t.amberText, t.amberTint, 'dot', 'Замедлено'],
    warn: [t.amberText, t.amberTint, 'dot', 'Внимание'],
    fail: [t.redText, t.redTint, 'dot', 'Не работает'],
    check: [t.text2, t.neutralTint, 'spinner', 'Проверка'],
    proxy: [t.violetText, 'transparent', 'plug', 'Через прокси'],
    tunnel: [t.violetText, 'transparent', 'route', 'Через туннель'],
    direct: [t.text2, t.neutralTint, 'check', 'Без обхода'],
    off: [t.text3, t.neutralTint, 'dot', 'Выключено'],
    fixed: [t.violetText, t.violetTint, 'check', 'Исправлено'],
  }[kind];
  const [fg, bg, lead, fallback] = spec;
  const outline = bg === 'transparent';
  return span(
    {
      display: 'inline-flex', alignItems: 'center', gap: '6px', height: '22px', padding: '0 8px', borderRadius: '6px',
      background: bg, border: `1px solid ${outline ? t.violetLine : 'transparent'}`, color: fg,
      fontSize: '12px', fontWeight: 600, lineHeight: '1', whiteSpace: 'nowrap', flex: 'none',
    },
    lead === 'dot' ? span({ width: '6px', height: '6px', borderRadius: '3px', background: fg, flex: 'none' }) : icon(lead, 13, 'currentColor', 2),
    span({}, label || fallback),
  );
}

function chip(t, text, tone = 'neutral') {
  const [fg, bg] = { violet: [t.violetText, t.violetTint], amber: [t.amberText, t.amberTint], neutral: [t.text2, t.neutralTint] }[tone];
  return span({ display: 'inline-flex', alignItems: 'center', height: '22px', padding: '0 8px', borderRadius: '6px', background: bg, color: fg, fontSize: '12px', fontWeight: 500, whiteSpace: 'nowrap', flex: 'none' }, text);
}

function buttonColors(t, variant, state) {
  const disabled = state === 'disabled';
  switch (variant) {
    case 'primary':
      return disabled
        ? { bg: t.raised, fg: t.text3, border: t.line }
        : { bg: { default: t.violet, hover: t.violetHover, pressed: t.violetPressed }[state], fg: '#FFFFFF', border: 'transparent' };
    case 'secondary':
      return disabled
        ? { bg: 'transparent', fg: t.text3, border: t.line }
        : { bg: { default: t.raised, hover: t.hover, pressed: t.line }[state], fg: t.text, border: t.lineStrong };
    case 'ghost':
      return { bg: { default: 'transparent', hover: t.neutralTint, pressed: t.line, disabled: 'transparent' }[state], fg: disabled ? t.text3 : state === 'default' ? t.text2 : t.text, border: 'transparent' };
    case 'danger':
      return disabled
        ? { bg: 'transparent', fg: t.text3, border: t.line }
        : { bg: { default: t.redTint, hover: t.redTint2, pressed: t.redTint3 }[state], fg: t.redText, border: 'transparent' };
  }
  throw new Error(`нет варианта кнопки ${variant}`);
}

function button(t, label, { variant = 'secondary', icon: ic, size = 'md', state = 'default', full = false, extra = {} } = {}) {
  const c = buttonColors(t, variant, state);
  const small = size === 'sm';
  return div(
    {
      height: small ? '30px' : '36px', width: full ? '100%' : undefined, flex: full ? undefined : 'none',
      display: 'flex', alignItems: 'center', justifyContent: 'center', gap: '8px', padding: small ? '0 10px' : '0 14px',
      borderRadius: '8px', background: c.bg, color: c.fg, border: `1px solid ${c.border}`,
      fontSize: small ? '12.5px' : '13.5px', fontWeight: 600, whiteSpace: 'nowrap', ...extra,
    },
    ic ? icon(ic, small ? 15 : 17) : '',
    span({}, label),
  );
}

function iconButton(t, name, { size = 32, color, iconSize = 18, bg } = {}) {
  return div({ width: `${size}px`, height: `${size}px`, flex: 'none', display: 'flex', alignItems: 'center', justifyContent: 'center', borderRadius: '8px', color: color || t.text2, background: bg }, icon(name, iconSize));
}

function toggle(t, on, { disabled = false } = {}) {
  return div(
    { width: '40px', height: '22px', flex: 'none', position: 'relative', borderRadius: '11px', background: on ? t.violet : t.lineStrong, opacity: disabled ? '0.45' : undefined },
    div({ position: 'absolute', top: '3px', left: on ? '21px' : '3px', width: '16px', height: '16px', borderRadius: '8px', background: on ? '#FFFFFF' : t.knobOff }),
  );
}

function segmented(t, items, selected) {
  return div(
    { display: 'flex', gap: '2px', padding: '3px', background: t.sunken, border: `1px solid ${t.line}`, borderRadius: '8px', alignSelf: 'flex-start' },
    items.map((label, i) =>
      div({
        height: '26px', display: 'flex', alignItems: 'center', padding: '0 10px', borderRadius: '6px',
        fontSize: '12.5px', fontWeight: 600, whiteSpace: 'nowrap',
        color: i === selected ? t.text : t.text2, background: i === selected ? t.segSel : 'transparent',
        boxShadow: i === selected ? `inset 0 0 0 1px ${t.lineStrong}` : undefined,
      }, label)),
  );
}

function select(t, value) {
  return div(
    { height: '30px', flex: 'none', display: 'flex', alignItems: 'center', gap: '6px', padding: '0 8px 0 10px', background: t.raised, border: `1px solid ${t.lineStrong}`, borderRadius: '8px', fontSize: '12.5px', fontWeight: 500, color: t.text },
    span({}, value), span({ display: 'flex', color: t.text2 }, icon('chevronDown', 14)),
  );
}

function input(t, value, { state = 'default', placeholder = '', trailing = '', leading = 'globe' } = {}) {
  const border = state === 'error' ? t.red : state === 'focus' ? t.violet : t.lineStrong;
  return div(
    {
      height: '40px', display: 'flex', alignItems: 'center', gap: '8px', padding: trailing ? '0 5px 0 12px' : '0 12px',
      background: t.sunken, border: `1px solid ${border}`, borderRadius: '8px',
      boxShadow: state === 'focus' ? `0 0 0 3px ${t.violetTint}` : state === 'error' ? `0 0 0 3px ${t.redTint}` : undefined,
    },
    span({ display: 'flex', color: t.text3 }, icon(leading, 16)),
    span({ flex: '1', minWidth: '0', fontFamily: value ? MONO : UI, fontSize: '13px', color: value ? t.text : t.text3 }, value || placeholder),
    trailing,
  );
}

function progress(t, pct) {
  return div({ height: '6px', borderRadius: '3px', background: t.line, overflow: 'hidden' }, div({ width: `${pct}%`, height: '100%', borderRadius: '3px', background: t.violet }));
}

function tile(t, name, { size = 36, color } = {}) {
  return div({ width: `${size}px`, height: `${size}px`, flex: 'none', display: 'flex', alignItems: 'center', justifyContent: 'center', borderRadius: '9px', background: t.raised, border: `1px solid ${t.line}`, color: color || t.text2 }, icon(name, 20));
}

const panel = (t, style, ...children) => div({ background: t.panel, border: `1px solid ${t.line}`, borderRadius: '12px', overflow: 'hidden', ...style }, ...children);

const sectionLabel = (t, text, right = '') =>
  div({ display: 'flex', alignItems: 'baseline', justifyContent: 'space-between', padding: '0 4px' },
    span({ fontSize: '12px', fontWeight: 600, color: t.text3 }, text),
    right ? span({ fontSize: '12px', color: t.text3 }, right) : '');

function serviceRow(t, { icon: ic, name, sub, badge: b, badgeLabel, action, last = false }) {
  return div(
    { display: 'flex', alignItems: 'center', gap: '12px', minHeight: '58px', padding: '10px 12px', borderBottom: last ? undefined : `1px solid ${t.line}` },
    tile(t, ic),
    div({ flex: '1', minWidth: '0', display: 'flex', flexDirection: 'column', gap: '2px' },
      span({ fontSize: '14px', fontWeight: 600, color: t.text }, name),
      span({ fontSize: '12.5px', color: t.text2, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }, sub)),
    action || badge(t, b, badgeLabel),
  );
}

// ─── Окно ─────────────────────────────────────────────────────────────────

function titleBar(t) {
  return div(
    { height: '40px', flex: 'none', display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '0 4px 0 14px' },
    div({ display: 'flex', alignItems: 'center', gap: '8px' }, markSimple(18), span({ fontSize: '13px', fontWeight: 600, color: t.text }, 'FI')),
    div({ display: 'flex', alignItems: 'center', gap: '2px' },
      div({ width: '36px', height: '30px', display: 'flex', alignItems: 'center', justifyContent: 'center', borderRadius: '6px', color: t.text2 }, icon('minimize', 16)),
      div({ width: '36px', height: '30px', display: 'flex', alignItems: 'center', justifyContent: 'center', borderRadius: '6px', color: t.text2 }, icon('close', 16))),
  );
}

function frame(t, content, { w = 400, h = 640 } = {}) {
  return div({ width: `${w}px`, height: `${h}px`, display: 'flex', flexDirection: 'column', overflow: 'hidden', background: t.bg, color: t.text, fontFamily: UI }, titleBar(t), content);
}

const screen = (style, ...children) => div({ flex: '1', minHeight: '0', display: 'flex', flexDirection: 'column', gap: '14px', padding: '4px 16px 16px', ...style }, ...children);

const subHeader = (t, title, right = '') =>
  div({ display: 'flex', alignItems: 'center', gap: '4px', margin: '0 -8px' },
    iconButton(t, 'chevronLeft'),
    span({ flex: '1', fontSize: '16px', fontWeight: 600, letterSpacing: '-0.005em' }, title),
    right);

// ─── Экраны ───────────────────────────────────────────────────────────────

function mainScreen(t) {
  const hero = div(
    { position: 'relative', overflow: 'hidden', display: 'flex', alignItems: 'center', gap: '14px', padding: '16px', background: t.panel, border: `1px solid ${t.line}`, borderRadius: '12px' },
    div({ position: 'absolute', left: '-60px', top: '-50px', width: '240px', height: '170px', background: t.glow }),
    div({ position: 'relative', display: 'flex', flex: 'none' }, markSimple(44)),
    div({ position: 'relative', flex: '1', minWidth: '0', display: 'flex', flexDirection: 'column', gap: '4px' },
      span({ fontSize: '17px', lineHeight: '22px', fontWeight: 700, letterSpacing: '-0.01em' }, 'Обход работает'),
      span({ fontSize: '12.5px', lineHeight: '17px', color: t.text2 }, 'Стратегия ', mono('SIMPLE FAKE', { color: t.text }), ' подобрана автоматически')),
    div({ position: 'relative', display: 'flex' }, toggle(t, true)),
  );

  const rows = [
    { icon: 'play', name: 'YouTube', sub: 'Сайт и видео', badge: 'ok' },
    { icon: 'headset', name: 'Discord', sub: 'Сообщения и голос', badge: 'ok' },
    { icon: 'bubble', name: 'Telegram', sub: 'Через встроенный прокси', badge: 'proxy' },
    { icon: 'globe', name: 'Сайты', sub: '12 в списке · 1 не открывается', badge: 'warn' },
    { icon: 'gamepad', name: 'Игры', sub: 'Игровой режим выключен', badge: 'off', last: true },
  ];

  return frame(t, screen({ gap: '12px' },
    hero,
    div({ display: 'flex', flexDirection: 'column', gap: '8px' },
      sectionLabel(t, 'Сервисы'),
      panel(t, {}, rows.map((r) => serviceRow(t, r)))),
    div({ display: 'grid', gridTemplateColumns: 'repeat(2, minmax(0, 1fr))', gap: '8px' },
      button(t, 'Не работает?', { icon: 'pulse' }),
      button(t, 'Добавить сайт', { icon: 'plus' })),
    div({ marginTop: 'auto', display: 'flex', alignItems: 'center', justifyContent: 'space-between', paddingTop: '10px', borderTop: `1px solid ${t.line}` },
      div({ display: 'flex', alignItems: 'center', gap: '6px', fontSize: '12px', color: t.text3 }, icon('clock', 14), span({}, 'Проверено 2 мин назад')),
      div({ display: 'flex', gap: '2px', marginRight: '-6px' }, iconButton(t, 'refresh'), iconButton(t, 'sliders'))),
  ));
}

function firstRunScreen(t) {
  const live = (ic, name, sub, right, last = false) =>
    div({ display: 'flex', alignItems: 'center', gap: '10px', padding: '9px 12px', borderBottom: last ? undefined : `1px solid ${t.line}` },
      span({ display: 'flex', color: t.text2 }, icon(ic, 18)),
      div({ flex: '1', minWidth: '0', display: 'flex', flexDirection: 'column', gap: '1px' },
        span({ fontSize: '13.5px', fontWeight: 600 }, name),
        span({ fontSize: '12px', color: t.text2 }, sub)),
      right);

  return frame(t, screen({ gap: '16px', padding: '16px 16px 16px' },
    div({ display: 'flex', flexDirection: 'column', gap: '8px' },
      span({ fontSize: '12px', fontWeight: 600, color: t.text3 }, 'Первый запуск'),
      span({ fontSize: '21px', lineHeight: '27px', fontWeight: 700, letterSpacing: '-0.015em', textWrap: 'pretty' }, 'Подбираем способ обхода для вашей сети'),
      span({ fontSize: '13px', lineHeight: '19px', color: t.text2, textWrap: 'pretty' }, 'Проверяем 21 стратегию на настоящих сайтах. Это займёт 3–5 минут, интернет может ненадолго прерываться.')),
    div({ display: 'flex', flexDirection: 'column', gap: '8px' },
      div({ display: 'flex', alignItems: 'baseline', justifyContent: 'space-between' },
        span({ fontSize: '13px', fontWeight: 600 }, 'Стратегия 9 из 21'),
        mono('ALT8', { color: t.text2 })),
      progress(t, 43)),
    panel(t, {},
      live('play', 'YouTube', 'Лучшая пока — ALT2', badge(t, 'ok', 'Найдено')),
      live('headset', 'Discord', 'Лучшая пока — ALT2', badge(t, 'ok', 'Найдено')),
      live('bubble', 'Telegram', 'Встроенный прокси', badge(t, 'proxy', 'Готов')),
      live('globe', 'Заблокированные сайты', 'Проверяем 3 сайта', badge(t, 'check')),
      live('server', 'Зарубежные хостинги', 'Обрыв на 16 КБ', badge(t, 'off', 'В очереди'), true)),
    div({ marginTop: 'auto', display: 'flex', flexDirection: 'column', gap: '10px' },
      button(t, 'Свернуть в трей', { full: true }),
      span({ fontSize: '12px', color: t.text3, textAlign: 'center' }, 'FI сообщит, когда закончит')),
  ));
}

function diagnoseScreen(t) {
  const option = (ic, name, hint, selected = false) =>
    div({ display: 'flex', flexDirection: 'column', gap: '10px', minHeight: '92px', padding: '12px', borderRadius: '10px', background: selected ? t.violetTint : t.panel, border: `1px solid ${selected ? t.violetLine : t.line}` },
      span({ display: 'flex', color: selected ? t.violetText : t.text2 }, icon(ic, 20)),
      div({ display: 'flex', flexDirection: 'column', gap: '2px' },
        span({ fontSize: '13.5px', fontWeight: 600 }, name),
        span({ fontSize: '12px', color: t.text2 }, hint)));
  const symptom = (label, on = false) =>
    div({ height: '30px', display: 'flex', alignItems: 'center', gap: '6px', padding: '0 10px', borderRadius: '8px', fontSize: '12.5px', fontWeight: 500, color: on ? t.text : t.text2, background: on ? t.violetTint : 'transparent', border: `1px solid ${on ? t.violetLine : t.lineStrong}` },
      on ? span({ display: 'flex', color: t.violetText }, icon('check', 14, 'currentColor', 2)) : '',
      span({}, label));

  return frame(t, screen({},
    subHeader(t, 'Что не работает?'),
    div({ display: 'grid', gridTemplateColumns: 'repeat(2, minmax(0, 1fr))', gap: '8px' },
      option('play', 'YouTube', 'Видео, превью'),
      option('headset', 'Discord', 'Вход, голос, файлы', true),
      option('bubble', 'Telegram', 'Сообщения, медиа'),
      option('gamepad', 'Игра', 'Подключение, пинг')),
    input(t, '', { placeholder: 'Или введите адрес сайта' }),
    div({ display: 'flex', flexDirection: 'column', gap: '8px' },
      sectionLabel(t, 'Что именно с Discord'),
      div({ display: 'flex', flexWrap: 'wrap', gap: '6px' },
        symptom('Не загружается'), symptom('Нет голоса', true), symptom('Не грузятся картинки'), symptom('Всё медленно'))),
    div({ marginTop: 'auto' }, button(t, 'Проверить', { variant: 'primary', icon: 'pulse', full: true })),
  ));
}

function step(t, { status, title, sub, last = false }) {
  const [bg, fg, ic] = { ok: [t.violetTint, t.violetText, 'check'], fail: [t.redTint, t.redText, 'cross'], skip: [t.neutralTint, t.text3, 'more'] }[status];
  return div({ display: 'flex', gap: '12px' },
    div({ width: '24px', flex: 'none', display: 'flex', flexDirection: 'column', alignItems: 'center' },
      div({ width: '24px', height: '24px', flex: 'none', display: 'flex', alignItems: 'center', justifyContent: 'center', borderRadius: '12px', background: bg, color: fg }, icon(ic, 14, 'currentColor', 2.2)),
      last ? '' : div({ width: '1px', flex: '1', minHeight: '12px', margin: '4px 0', background: t.line })),
    div({ display: 'flex', flexDirection: 'column', gap: '2px', paddingTop: '2px', paddingBottom: last ? '0' : '14px' },
      span({ fontSize: '13.5px', fontWeight: 600, color: status === 'skip' ? t.text2 : t.text }, title),
      span({ fontSize: '12.5px', lineHeight: '17px', color: status === 'fail' ? t.redText : t.text2 }, sub)));
}

function diagnoseResultScreen(t) {
  return frame(t, screen({},
    subHeader(t, 'Discord · результат'),
    panel(t, { padding: '14px' },
      step(t, { status: 'ok', title: 'Адрес сайта', sub: 'DNS отвечает правильно' }),
      step(t, { status: 'ok', title: 'Подключение', sub: 'Сервер доступен' }),
      step(t, { status: 'fail', title: 'Защищённое соединение', sub: 'Провайдер обрывает соединение по имени сайта' }),
      step(t, { status: 'fail', title: 'Голосовой канал', sub: 'UDP-пакеты не доходят до сервера' }),
      step(t, { status: 'skip', title: 'Скорость', sub: 'Не проверялась', last: true })),
    panel(t, { padding: '14px', display: 'flex', flexDirection: 'column', gap: '6px' },
      div({ display: 'flex', alignItems: 'center', gap: '6px', color: t.redText }, icon('alert', 15), span({ fontSize: '12px', fontWeight: 600 }, 'Причина')),
      span({ fontSize: '15px', lineHeight: '20px', fontWeight: 700 }, 'Блокировка по анализу трафика (DPI)'),
      span({ fontSize: '12.5px', lineHeight: '18px', color: t.text2, textWrap: 'pretty' }, 'Текущая стратегия перестала работать для Discord. Подберём другую — YouTube и сайты это не затронет.')),
    div({ marginTop: 'auto', display: 'flex', flexDirection: 'column', gap: '6px' },
      button(t, 'Исправить', { variant: 'primary', full: true }),
      button(t, 'Подробный отчёт', { variant: 'ghost', full: true })),
  ));
}

function fixingScreen(t) {
  const log = (name, result, kind, last = false) => {
    const [fg, ic] = { fail: [t.redText, 'cross'], part: [t.amberText, 'alert'], run: [t.text2, 'spinner'] }[kind];
    return div({ display: 'flex', alignItems: 'center', gap: '10px', padding: '9px 12px', borderBottom: last ? undefined : `1px solid ${t.line}` },
      span({ display: 'flex', color: fg }, icon(ic, 15, 'currentColor', 2)),
      mono(name, { width: '88px', flex: 'none', fontSize: '12.5px', color: t.text }),
      span({ flex: '1', minWidth: '0', fontSize: '12.5px', color: t.text2 }, result));
  };
  return frame(t, screen({},
    subHeader(t, 'Discord · исправление'),
    panel(t, { padding: '16px', display: 'flex', flexDirection: 'column', gap: '10px' },
      div({ display: 'flex', alignItems: 'baseline', justifyContent: 'space-between' },
        span({ fontSize: '15px', fontWeight: 700 }, 'Пробуем стратегии'),
        span({ fontSize: '13px', color: t.text2 }, '5 из 21')),
      progress(t, 24),
      span({ fontSize: '12.5px', lineHeight: '18px', color: t.text2, textWrap: 'pretty' }, 'Проверяем только Discord. Остальные сервисы работают на прежней стратегии.')),
    div({ display: 'flex', flexDirection: 'column', gap: '8px' },
      sectionLabel(t, 'Журнал'),
      panel(t, {},
        log('SIMPLE FAKE', 'Голос не проходит', 'fail'),
        log('ALT', 'Голос не проходит', 'fail'),
        log('ALT2', 'Сообщения есть, голос обрывается', 'part'),
        log('ALT3', 'Голос не проходит', 'fail'),
        log('ALT4', 'Проверяем голосовой канал…', 'run', true))),
    div({ marginTop: 'auto' }, button(t, 'Отменить', { full: true })),
  ));
}

function addSiteScreen(t) {
  const kv = (k, v) => div({ display: 'flex', gap: '10px', fontSize: '12.5px', lineHeight: '17px' },
    span({ width: '64px', flex: 'none', color: t.text3 }, k), span({ flex: '1', color: t.text }, v));
  const site = (domain, tag, sub = '', { last = false, hovered = false } = {}) =>
    div({ display: 'flex', alignItems: 'center', gap: '10px', minHeight: '50px', padding: '8px 6px 8px 12px', background: hovered ? t.raised : undefined, borderBottom: last ? undefined : `1px solid ${t.line}` },
      div({ flex: '1', minWidth: '0', display: 'flex', flexDirection: 'column', gap: '3px' },
        mono(domain, { fontSize: '13px', color: t.text }),
        sub ? span({ fontSize: '12px', color: t.text2 }, sub) : ''),
      tag,
      hovered ? iconButton(t, 'trash', { size: 28, iconSize: 16 }) : div({ width: '28px', flex: 'none' }));

  return frame(t, screen({},
    subHeader(t, 'Добавить сайт'),
    div({ display: 'flex', flexDirection: 'column', gap: '8px' },
      input(t, 'x.com', { state: 'focus', trailing: button(t, 'Вставить', { variant: 'ghost', size: 'sm', icon: 'clipboard' }) }),
      button(t, 'Проверить и добавить', { variant: 'primary', full: true })),
    panel(t, { padding: '14px', display: 'flex', flexDirection: 'column', gap: '8px' },
      div({ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: '8px', marginBottom: '2px' },
        mono('x.com', { fontSize: '14px', fontWeight: 500, color: t.text }),
        badge(t, 'fixed')),
      kv('Было', 'Соединение зависает сразу после подключения'),
      kv('Причина', 'Блокировка по анализу трафика (DPI)'),
      kv('Сделано', 'Сайт добавлен в список обхода и открывается')),
    div({ display: 'flex', flexDirection: 'column', gap: '8px' },
      sectionLabel(t, 'Ваши сайты', '4'),
      panel(t, {},
        site('x.com', chip(t, 'Обход DPI', 'violet')),
        site('rutracker.org', chip(t, 'Обход DPI', 'violet'), '', { hovered: true }),
        site('chatgpt.com', chip(t, 'Нужен туннель', 'amber'), 'Сайт сам не пускает пользователей из России'),
        site('hypixel.net', chip(t, 'IP-список'), '', { last: true }))),
    span({ marginTop: 'auto', fontSize: '12px', lineHeight: '17px', color: t.text3, textWrap: 'pretty' }, 'Скопируйте адрес и выберите в трее «Добавить сайт из буфера» — открывать окно не нужно.'),
  ), { h: 720 });
}

function settingsScreen(t) {
  const group = (title, ...rows) => div({ display: 'flex', flexDirection: 'column', gap: '8px' }, sectionLabel(t, title), panel(t, {}, rows));
  const row = (label, sub, control, { last = false, column = false } = {}) =>
    div({ display: 'flex', flexDirection: column ? 'column' : 'row', alignItems: column ? 'stretch' : 'center', gap: column ? '10px' : '12px', padding: '12px 14px', borderBottom: last ? undefined : `1px solid ${t.line}` },
      div({ flex: '1', minWidth: '0', display: 'flex', flexDirection: 'column', gap: '2px' },
        span({ fontSize: '13.5px', fontWeight: 600 }, label),
        sub ? span({ fontSize: '12px', lineHeight: '16px', color: t.text2, textWrap: 'pretty' }, sub) : ''),
      control);

  return frame(t, screen({ gap: '16px' },
    subHeader(t, 'Настройки'),
    group('Общие',
      row('Запускать вместе с Windows', '', toggle(t, true)),
      row('Тема', '', segmented(t, ['Системная', 'Тёмная', 'Светлая'], 0), { column: true }),
      row('Масштаб интерфейса', 'Авто — как в параметрах экрана Windows', segmented(t, ['Авто', '100%', '125%', '150%', '200%'], 0), { column: true, last: true })),
    group('Обход',
      row('Чинить автоматически', 'Если сервис перестал работать, подобрать стратегию заново', toggle(t, true)),
      row('Проверять сервисы', '', select(t, 'Каждые 15 минут')),
      row('Игровой режим', 'Обход для игровых портов 1024–65535', segmented(t, ['Выкл', 'TCP', 'UDP', 'Всё'], 0), { column: true }),
      row('Защищённый DNS', 'Помогает, если провайдер подменяет адреса сайтов', toggle(t, false), { last: true })),
    group('Модули',
      row('Прокси для Telegram', span({}, 'Адрес ', mono('127.0.0.1:1080', { color: t.text })), toggle(t, true)),
      row('Туннель', 'Для сайтов, которые сами блокируют Россию', button(t, 'Добавить сервер', { size: 'sm' }), { last: true })),
    group('Обновления',
      row('База стратегий', 'Обновлена 14 сентября', button(t, 'Проверить', { variant: 'ghost', size: 'sm', icon: 'refresh' })),
      row('Версия', '', mono('0.1.0', { fontSize: '12.5px', color: t.text2 }), { last: true })),
  ), { h: 1040 });
}

function trayScreen(t) {
  const sep = div({ height: '1px', margin: '4px 2px', background: t.line });
  const item = (ic, label, { hover = false, chevron = false } = {}) =>
    div({ height: '34px', display: 'flex', alignItems: 'center', gap: '10px', padding: '0 10px', borderRadius: '6px', background: hover ? t.hover : 'transparent', fontSize: '13px', fontWeight: 500, color: t.text },
      span({ display: 'flex', color: t.text2 }, icon(ic, 16)),
      span({ flex: '1' }, label),
      chevron ? span({ display: 'flex', color: t.text3 }, icon('chevronRight', 14)) : '');
  const sys = (ic) => div({ width: '32px', height: '40px', display: 'flex', alignItems: 'center', justifyContent: 'center', color: '#F1EEF3' }, icon(ic, 16, 'currentColor', 1.6));

  return div({ width: '760px', height: '600px', position: 'relative', overflow: 'hidden', fontFamily: UI, color: t.text, background: '#1C1921' },
    div({ position: 'absolute', left: '0', top: '0', right: '0', bottom: '0', background: 'radial-gradient(620px 420px at 18% 12%, rgba(140, 92, 242, 0.10), transparent 70%), radial-gradient(520px 380px at 88% 36%, rgba(229, 72, 77, 0.07), transparent 70%)' }),

    div({ position: 'absolute', right: '16px', top: '16px', width: '364px', display: 'flex', flexDirection: 'column', gap: '10px', padding: '12px 14px 16px', background: '#201D24', border: `1px solid ${t.lineStrong}`, borderRadius: '10px', boxShadow: '0 16px 40px rgba(0, 0, 0, 0.45)' },
      div({ display: 'flex', alignItems: 'center', gap: '8px' },
        mark16(),
        span({ fontSize: '12px', color: t.text2 }, 'FI'),
        span({ marginLeft: 'auto', display: 'flex', color: t.text3 }, icon('close', 14))),
      div({ display: 'flex', alignItems: 'flex-start', gap: '12px' },
        appIcon(48),
        div({ flex: '1', minWidth: '0', display: 'flex', flexDirection: 'column', gap: '3px' },
          span({ fontSize: '14px', fontWeight: 600 }, 'Discord снова работает'),
          span({ fontSize: '12.5px', lineHeight: '18px', color: t.text2, textWrap: 'pretty' }, 'Стратегия сменилась автоматически на ALT4. Остальные сервисы не затронуты.')))),

    div({ position: 'absolute', right: '64px', bottom: '56px', width: '284px', padding: '6px', background: t.raised, border: `1px solid ${t.lineStrong}`, borderRadius: '10px', boxShadow: '0 18px 44px rgba(0, 0, 0, 0.5), 0 2px 8px rgba(0, 0, 0, 0.35)' },
      div({ display: 'flex', alignItems: 'center', gap: '10px', padding: '8px 10px 10px' },
        markSimple(28),
        div({ flex: '1', minWidth: '0', display: 'flex', flexDirection: 'column', gap: '2px' },
          span({ fontSize: '13.5px', fontWeight: 600 }, 'Обход работает'),
          span({ fontSize: '12px', color: t.text2 }, 'SIMPLE FAKE · проверено 2 мин назад'))),
      sep,
      item('window', 'Открыть FI'),
      item('refresh', 'Проверить сейчас', { hover: true }),
      item('clipboard', 'Добавить сайт из буфера'),
      item('pause', 'Приостановить', { chevron: true }),
      sep,
      item('sliders', 'Настройки'),
      item('exit', 'Выход')),

    div({ position: 'absolute', left: '0', right: '0', bottom: '0', height: '48px', display: 'flex', alignItems: 'center', justifyContent: 'flex-end', gap: '2px', padding: '0 12px', background: TASKBAR_DARK, borderTop: `1px solid ${t.line}` },
      sys('chevronUp'),
      div({ width: '32px', height: '40px', display: 'flex', alignItems: 'center', justifyContent: 'center', borderRadius: '6px', background: 'rgba(255, 255, 255, 0.08)' }, tray(16, 'active')),
      sys('wifi'),
      sys('volume'),
      div({ display: 'flex', flexDirection: 'column', alignItems: 'flex-end', gap: '1px', padding: '0 6px 0 10px', fontSize: '12px', color: '#F1EEF3' }, span({}, '3:05'), span({}, '14.09.2026'))),
  );
}

// ─── Система ──────────────────────────────────────────────────────────────

const boardTitle = (t, title, sub) =>
  div({ display: 'flex', flexDirection: 'column', gap: '6px' },
    span({ fontSize: '28px', lineHeight: '34px', fontWeight: 700, letterSpacing: '-0.02em' }, title),
    sub ? span({ fontSize: '14px', lineHeight: '20px', color: t.text2, maxWidth: '760px', textWrap: 'pretty' }, sub) : '');

const block = (t, title, caption, ...content) =>
  div({ display: 'flex', flexDirection: 'column', gap: '14px' },
    div({ display: 'flex', flexDirection: 'column', gap: '3px' },
      span({ fontSize: '15px', fontWeight: 600 }, title),
      caption ? span({ fontSize: '12.5px', lineHeight: '18px', color: t.text3, textWrap: 'pretty' }, caption) : ''),
    ...content);

const board = (t, w, h, ...children) =>
  div({ width: `${w}px`, height: `${h}px`, display: 'flex', flexDirection: 'column', gap: '36px', padding: '40px', overflow: 'hidden', background: t.bg, color: t.text, fontFamily: UI }, ...children);

function tokensBoard() {
  const t = DARK;
  const swatch = (color, name, hex, onLightBoard) =>
    div({ display: 'flex', flexDirection: 'column', gap: '8px', padding: '10px', borderRadius: '10px', background: onLightBoard ? '#FFFFFF' : t.panel, border: `1px solid ${onLightBoard ? '#E6E2EA' : t.line}` },
      div({ height: '44px', borderRadius: '6px', background: color, border: `1px solid ${onLightBoard ? 'rgba(22, 19, 26, 0.08)' : 'rgba(255, 255, 255, 0.08)'}` }),
      div({ display: 'flex', flexDirection: 'column', gap: '1px' },
        span({ fontSize: '12.5px', fontWeight: 600, color: onLightBoard ? '#16131A' : t.text }, name),
        mono(hex, { fontSize: '11.5px', color: onLightBoard ? '#5E5865' : t.text2 })));
  const palette = (th, onLightBoard) => [
    [th.bg, 'Фон окна', th.bg], [th.panel, 'Панель', th.panel], [th.raised, 'Поднятый', th.raised], [th.line, 'Линия', th.line],
    [th.text, 'Текст', th.text], [th.text2, 'Текст 2', th.text2], [th.text3, 'Текст 3', th.text3], [th.violet, 'Фиолетовый', th.violet],
    [th.violetText, 'Фиолетовый текст', th.violetText], [th.red, 'Красный', th.red], [th.redText, 'Красный текст', th.redText], [th.amber, 'Янтарный', th.amber],
  ].map(([c, n, h]) => swatch(c, n, h, onLightBoard));

  const themeBlock = (title, th, onLightBoard) =>
    div({ flex: '1', display: 'flex', flexDirection: 'column', gap: '12px', padding: '16px', borderRadius: '14px', background: onLightBoard ? '#F6F4F8' : t.sunken, border: `1px solid ${t.line}` },
      span({ fontSize: '13px', fontWeight: 600, color: onLightBoard ? '#16131A' : t.text }, title),
      div({ display: 'grid', gridTemplateColumns: 'repeat(4, minmax(0, 1fr))', gap: '8px' }, palette(th, onLightBoard)));

  const meaning = (b, title, text) =>
    div({ display: 'flex', flexDirection: 'column', gap: '10px', padding: '14px', borderRadius: '12px', background: t.panel, border: `1px solid ${t.line}` },
      div({ display: 'flex' }, b),
      span({ fontSize: '13.5px', fontWeight: 600 }, title),
      span({ fontSize: '12.5px', lineHeight: '18px', color: t.text2, textWrap: 'pretty' }, text));

  const typeRow = (label, spec, style, sample) =>
    div({ display: 'grid', gridTemplateColumns: '180px minmax(0, 1fr)', alignItems: 'baseline', gap: '16px', padding: '12px 0', borderBottom: `1px solid ${t.line}` },
      div({ display: 'flex', flexDirection: 'column', gap: '2px' }, span({ fontSize: '12.5px', fontWeight: 600, color: t.text2 }, label), mono(spec, { fontSize: '11.5px', color: t.text3 })),
      span({ color: t.text, ...style }, sample));

  const measure = (label, px, extra = {}) =>
    div({ display: 'flex', alignItems: 'center', gap: '12px', height: '28px' },
      mono(String(px), { width: '28px', fontSize: '12px', color: t.text2, textAlign: 'right' }),
      div({ height: '10px', width: `${px * 6}px`, borderRadius: '2px', background: t.violet, ...extra }),
      span({ fontSize: '12px', color: t.text3 }, label));

  const radius = (r, label) =>
    div({ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: '8px' },
      div({ width: '64px', height: '48px', borderRadius: `${r}px`, background: t.raised, border: `1px solid ${t.lineStrong}` }),
      mono(`${r}`, { fontSize: '12px', color: t.text }),
      span({ fontSize: '11.5px', color: t.text3, textAlign: 'center' }, label));

  return board(t, 1280, 1340,
    boardTitle(t, 'Основа', 'Тёмная тема основная, светлая включается вместе с системной. Акценты — только со смыслом: цвет всегда подкреплён значком и текстом.'),
    div({ display: 'flex', gap: '16px' }, themeBlock('Тёмная тема', DARK, false), themeBlock('Светлая тема', LIGHT, true)),
    block(t, 'Смысл цветов', '',
      div({ display: 'grid', gridTemplateColumns: 'repeat(4, minmax(0, 1fr))', gap: '12px' },
        meaning(badge(t, 'ok'), 'Фиолетовый', 'Обход работает, основное действие, выбранный вариант.'),
        meaning(badge(t, 'fail'), 'Красный', 'Знак бренда — свет в просвете. В интерфейсе — только «не работает».'),
        meaning(badge(t, 'warn'), 'Янтарный', 'Частичные сбои: что-то открывается медленно или не всё.'),
        meaning(badge(t, 'off'), 'Серый', 'Выключено, в очереди или обход не нужен.'))),
    div({ display: 'grid', gridTemplateColumns: 'minmax(0, 1fr) 420px', gap: '48px' },
      block(t, 'Шрифты', 'Onest — интерфейс, хорошая кириллица. JetBrains Mono — адреса, порты, названия стратегий.',
        div({},
          typeRow('Заголовок', 'Onest 21/27 · 700', { fontSize: '21px', lineHeight: '27px', fontWeight: 700, letterSpacing: '-0.015em' }, 'Подбираем способ обхода'),
          typeRow('Статус', 'Onest 17/22 · 700', { fontSize: '17px', lineHeight: '22px', fontWeight: 700, letterSpacing: '-0.01em' }, 'Обход работает'),
          typeRow('Экран', 'Onest 16/22 · 600', { fontSize: '16px', lineHeight: '22px', fontWeight: 600 }, 'Добавить сайт'),
          typeRow('Название', 'Onest 14/20 · 600', { fontSize: '14px', lineHeight: '20px', fontWeight: 600 }, 'Discord'),
          typeRow('Текст', 'Onest 13/19 · 400', { fontSize: '13px', lineHeight: '19px' }, 'Провайдер обрывает соединение по имени сайта'),
          typeRow('Подпись', 'Onest 12/16 · 500–600', { fontSize: '12px', lineHeight: '16px', fontWeight: 500, color: t.text2 }, 'Проверено 2 мин назад'),
          typeRow('Моно', 'JetBrains Mono 12.5/18', { fontFamily: MONO, fontSize: '12.5px', lineHeight: '18px' }, 'rutracker.org · 127.0.0.1:1080'))),
      div({ display: 'flex', flexDirection: 'column', gap: '32px' },
        block(t, 'Отступы', 'Кратны 4. Внутри панелей 12–14, между блоками 12–16.',
          div({}, [4, 8, 12, 16, 20, 24, 32].map((px) => measure({ 4: 'между значком и текстом метки', 8: 'между кнопками', 12: 'между блоками окна', 16: 'поля окна', 20: '', 24: '', 32: '' }[px], px)))),
        block(t, 'Скругления и высоты', 'Углы окна рисует Windows 11 (8 px), приложение их не повторяет.',
          div({ display: 'flex', gap: '16px' }, radius(6, 'метки'), radius(8, 'кнопки, поля'), radius(10, 'плитки, меню'), radius(12, 'панели')),
          div({ display: 'flex', flexWrap: 'wrap', gap: '8px' },
            chip(t, 'метка 22'), chip(t, 'малая кнопка 30'), chip(t, 'кнопка 36'), chip(t, 'поле 40'), chip(t, 'строка сервиса 58'))))),
  );
}

function componentsBoard() {
  const t = DARK;
  const states = ['default', 'hover', 'pressed', 'disabled'];
  const stateNames = ['Обычная', 'Наведение', 'Нажата', 'Недоступна'];
  const variants = [['primary', 'Основная', 'Исправить'], ['secondary', 'Вторичная', 'Проверить'], ['ghost', 'Прозрачная', 'Подробнее'], ['danger', 'Опасная', 'Удалить']];
  const cellLabel = (text) => span({ fontSize: '12px', fontWeight: 600, color: t.text3 }, text);

  const buttons = div({ display: 'grid', gridTemplateColumns: '110px repeat(4, minmax(0, 1fr))', gap: '12px 16px', alignItems: 'center' },
    span({}), stateNames.map(cellLabel),
    variants.map(([v, name, label]) => [
      span({ fontSize: '13px', color: t.text2 }, name),
      states.map((s) => div({ display: 'flex' }, button(t, label, { variant: v, state: s }))),
    ]));

  const focus = div({ display: 'flex', alignItems: 'center', gap: '16px' },
    button(t, 'Проверить', { extra: { boxShadow: `0 0 0 2px ${t.bg}, 0 0 0 4px ${t.violetText}` } }),
    span({ fontSize: '12.5px', color: t.text3 }, 'Фокус с клавиатуры — кольцо 2 px с зазором'));

  const controls = div({ display: 'flex', flexDirection: 'column', gap: '16px' },
    div({ display: 'flex', alignItems: 'center', gap: '20px' },
      toggle(t, true), toggle(t, false), toggle(t, true, { disabled: true }), toggle(t, false, { disabled: true })),
    segmented(t, ['Выкл', 'TCP', 'UDP', 'Всё'], 1),
    div({ display: 'flex' }, select(t, 'Каждые 15 минут')));

  const inputs = div({ display: 'flex', flexDirection: 'column', gap: '12px', width: '400px' },
    input(t, '', { placeholder: 'Адрес сайта' }),
    input(t, 'rutracker.org', { state: 'focus' }),
    div({ display: 'flex', flexDirection: 'column', gap: '6px' },
      input(t, 'rutracker', { state: 'error' }),
      span({ fontSize: '12px', color: t.redText, paddingLeft: '2px' }, 'Не похоже на адрес сайта')));

  const rows = panel(t, { width: '400px' },
    serviceRow(t, { icon: 'play', name: 'YouTube', sub: 'Сайт и видео', badge: 'ok' }),
    serviceRow(t, { icon: 'play', name: 'YouTube', sub: 'Видео грузится медленно', badge: 'slow' }),
    serviceRow(t, { icon: 'headset', name: 'Discord', sub: 'Голос не проходит', action: button(t, 'Исправить', { variant: 'primary', size: 'sm' }) }),
    serviceRow(t, { icon: 'bubble', name: 'Telegram', sub: 'Проверяем подключение', badge: 'check', last: true }));

  const menuItems = div({ width: '284px', padding: '6px', background: t.raised, border: `1px solid ${t.lineStrong}`, borderRadius: '10px' },
    ...[['window', 'Обычный', false, t.text], ['refresh', 'Наведение', true, t.text], ['pause', 'Недоступен', false, t.text3]].map(([ic, label, hover, color]) =>
      div({ height: '34px', display: 'flex', alignItems: 'center', gap: '10px', padding: '0 10px', borderRadius: '6px', background: hover ? t.hover : 'transparent', fontSize: '13px', fontWeight: 500, color },
        span({ display: 'flex', color: color === t.text3 ? t.text3 : t.text2 }, icon(ic, 16)), span({}, label))));

  return board(t, 1280, 1070,
    boardTitle(t, 'Компоненты', 'Всё в тёмной теме; светлая получает те же компоненты с токенами из «Основы».'),
    block(t, 'Статусы', 'Цвет всегда вместе с меткой или значком — статус читается и без цвета.',
      div({ display: 'flex', flexWrap: 'wrap', gap: '10px' },
        ['ok', 'slow', 'warn', 'fail', 'check', 'proxy', 'tunnel', 'direct', 'off', 'fixed'].map((k) => badge(t, k))),
      div({ display: 'flex', flexWrap: 'wrap', gap: '10px' }, chip(t, 'Обход DPI', 'violet'), chip(t, 'Нужен туннель', 'amber'), chip(t, 'IP-список'))),
    div({ display: 'grid', gridTemplateColumns: 'minmax(0, 1fr) 420px', gap: '48px' },
      div({ display: 'flex', flexDirection: 'column', gap: '36px' },
        block(t, 'Кнопки', 'Одна основная кнопка на экран. Высота 36, малая — 30.', buttons, focus),
        block(t, 'Поле ввода', 'Обычное, в фокусе, с ошибкой.', inputs)),
      div({ display: 'flex', flexDirection: 'column', gap: '36px' },
        block(t, 'Переключатели и выбор', '', controls),
        block(t, 'Строка сервиса', 'Работает, замедлено, нужна починка, идёт проверка.', rows),
        block(t, 'Прогресс', '', div({ width: '400px', display: 'flex', flexDirection: 'column', gap: '8px' }, progress(t, 43), progress(t, 100))),
        block(t, 'Пункты меню', '', menuItems))),
  );
}

function iconsBoard() {
  const t = DARK;
  const sized = (content, label, color) =>
    div({ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: '10px' }, content, mono(label, { fontSize: '11.5px', color }));

  const appDark = div({ flex: '1', display: 'flex', alignItems: 'flex-end', justifyContent: 'space-between', padding: '28px', background: t.sunken, border: `1px solid ${t.line}`, borderRadius: '12px' },
    [256, 128, 64, 48, 32, 24, 16].map((s) => sized(appIcon(s), String(s), t.text3)));
  const appLight = div({ width: '400px', flex: 'none', display: 'flex', alignItems: 'flex-end', justifyContent: 'space-between', padding: '28px', background: '#F6F4F8', border: `1px solid ${t.line}`, borderRadius: '12px' },
    [128, 48, 32, 24, 16].map((s) => sized(appIcon(s), String(s), '#8E8895')));

  const STATES = [
    ['active', 'Работает', 'Обход включён, всё в порядке'],
    ['select', 'Подбор', 'Идёт проверка или подбор стратегии'],
    ['attention', 'Внимание', 'Часть сервисов с перебоями'],
    ['failure', 'Не работает', 'Автопочинка не помогла'],
    ['paused', 'Пауза', 'Обход приостановлен'],
    ['off', 'Выключено', 'Служба не запущена'],
  ];
  const cell = (content, bg) => div({ height: '72px', display: 'flex', alignItems: 'center', justifyContent: 'center', borderRadius: '8px', background: bg }, content);
  const headCell = (text, span_ = 1) => div({ gridColumn: `span ${span_}`, fontSize: '12px', fontWeight: 600, color: t.text3, textAlign: 'center' }, text);
  const trayTable = div({ display: 'grid', gridTemplateColumns: '220px repeat(6, minmax(0, 1fr)) 96px', gap: '8px', alignItems: 'center' },
    div({}), headCell('Тёмная панель задач', 4), headCell('Светлая', 2), headCell('16 px ×4'),
    div({}), ['16', '20', '24', '32', '16', '24'].map((s) => headCell(`${s} px`)), div({}),
    STATES.map(([state, name, desc]) => [
      div({ display: 'flex', flexDirection: 'column', gap: '2px' }, span({ fontSize: '13.5px', fontWeight: 600 }, name), span({ fontSize: '12px', color: t.text2 }, desc)),
      [16, 20, 24, 32].map((s) => cell(tray(s, state), TASKBAR_DARK)),
      [16, 24].map((s) => cell(tray(s, state, { onLight: true }), TASKBAR_LIGHT)),
      cell(pixelGlyph(16, 4, state), TASKBAR_DARK),
    ]));

  const UI_ICONS = [
    ['power', 'Питание'], ['sliders', 'Настройки'], ['plus', 'Добавить'], ['pulse', 'Диагностика'], ['refresh', 'Проверить'], ['check', 'Работает'], ['cross', 'Ошибка'], ['alert', 'Внимание'],
    ['info', 'Сведения'], ['pause', 'Пауза'], ['play', 'Видео'], ['headset', 'Голос'], ['bubble', 'Мессенджер'], ['globe', 'Сайт'], ['gamepad', 'Игры'], ['server', 'Хостинг'],
    ['route', 'Туннель'], ['plug', 'Прокси'], ['clipboard', 'Из буфера'], ['search', 'Поиск'], ['trash', 'Удалить'], ['download', 'Обновление'], ['external', 'Открыть'], ['clock', 'Время'],
    ['window', 'Окно'], ['exit', 'Выход'], ['moon', 'Тема'], ['scale', 'Масштаб'], ['bolt', 'Скорость'], ['list', 'Список'], ['chevronLeft', 'Назад'], ['spinner', 'Загрузка'],
  ];
  const iconGrid = div({ display: 'grid', gridTemplateColumns: 'repeat(8, minmax(0, 1fr))', gap: '8px' },
    UI_ICONS.map(([name, label]) =>
      div({ height: '84px', display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: '10px', borderRadius: '10px', background: t.panel, border: `1px solid ${t.line}`, color: t.text },
        icon(name, 24), span({ fontSize: '12px', color: t.text2 }, label))));

  return board(t, 1280, 1640,
    boardTitle(t, 'Значки', 'Знак — «П» с узким просветом, через который проходит свет. Красный просвет — всегда знак бренда; состояние в трее меняет цвет просвета и точку.'),
    block(t, 'Значок приложения', '256–64 — с подсветкой; 48 и меньше — плоская версия; 16 — нарисована по пикселям.',
      div({ display: 'flex', gap: '16px' }, appDark, appLight)),
    block(t, 'Значок в трее', 'Отдельный кадр под каждый масштаб: 16 · 20 · 24 · 32 px. Точка состояния вырезана из знака, чтобы читаться на любой панели.', trayTable),
    block(t, 'Значки интерфейса', 'Сетка 24, линия 1,75, скруглённые концы. Сервисы обозначены нейтральными знаками, а не чужими логотипами.', iconGrid),
  );
}

function scalingBoard() {
  const t = DARK;
  const SCALES = [[1, '100%', '96 DPI', 16], [1.25, '125%', '120 DPI', 20], [1.5, '150%', '144 DPI', 24], [2, '200%', '192 DPI', 32]];

  const sample = (k) => div({ zoom: String(k), width: '232px', display: 'flex', flexDirection: 'column', gap: '10px', padding: '12px', background: t.panel, border: `1px solid ${t.line}`, borderRadius: '12px' },
    div({ display: 'flex', alignItems: 'center', gap: '10px' },
      tile(t, 'headset'),
      div({ flex: '1', display: 'flex', flexDirection: 'column', gap: '2px' },
        span({ fontSize: '14px', fontWeight: 600 }, 'Discord'),
        span({ fontSize: '12.5px', color: t.text2 }, 'Сообщения и голос'))),
    div({ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }, badge(t, 'ok'), button(t, 'Проверить', { size: 'sm' })));

  const column = ([k, label, dpi, trayPx]) =>
    div({ display: 'flex', flexDirection: 'column', gap: '16px', alignItems: 'flex-start' },
      div({ display: 'flex', alignItems: 'baseline', gap: '10px' },
        span({ fontSize: '26px', fontWeight: 700, letterSpacing: '-0.02em' }, label),
        span({ fontSize: '12.5px', color: t.text3 }, dpi)),
      sample(k),
      div({ display: 'flex', alignItems: 'center', gap: '10px' },
        div({ width: '48px', height: '48px', display: 'flex', alignItems: 'center', justifyContent: 'center', borderRadius: '8px', background: TASKBAR_DARK }, tray(trayPx, 'active')),
        span({ fontSize: '12.5px', color: t.text2 }, `Трей: кадр ${trayPx} px`)));

  const th = (text) => span({ fontSize: '12px', fontWeight: 600, color: t.text3, padding: '10px 12px', borderBottom: `1px solid ${t.lineStrong}` }, text);
  const td = (text, isMono = false) => span({ fontSize: '13px', color: t.text, fontFamily: isMono ? MONO : UI, padding: '10px 12px', borderBottom: `1px solid ${t.line}` }, text);
  const table = div({ display: 'grid', gridTemplateColumns: 'repeat(7, minmax(0, 1fr))' },
    ['Масштаб', 'DPI', 'Трей', 'Окно, физ. пикс.', 'Основной текст', 'Кнопка', 'Значки интерфейса'].map(th),
    [['100%', '96', '16', '400 × 640', '14', '36', '20'], ['125%', '120', '20', '500 × 800', '17,5', '45', '25'], ['150%', '144', '24', '600 × 960', '21', '54', '30'], ['200%', '192', '32', '800 × 1280', '28', '72', '40']]
      .map((r) => r.map((v, i) => td(v, i > 0))));

  const rule = (n, text) => div({ display: 'flex', gap: '12px' },
    mono(String(n).padStart(2, '0'), { fontSize: '12px', color: t.violetText, paddingTop: '2px', flex: 'none' }),
    span({ fontSize: '13px', lineHeight: '19px', color: t.text, textWrap: 'pretty' }, text));

  const K = 0.28;
  const screenW = Math.round(1366 * K), screenH = Math.round(768 * K);
  const taskbarH = Math.round(60 * K), winW = Math.round(500 * K), winH = Math.round(688 * K);
  const smallScreen = div({ display: 'flex', flexDirection: 'column', gap: '12px' },
    div({ width: `${screenW}px`, height: `${screenH}px`, position: 'relative', borderRadius: '6px', background: '#1C1921', border: `1px solid ${t.lineStrong}`, overflow: 'hidden' },
      div({ position: 'absolute', right: '3px', bottom: `${taskbarH + 3}px`, width: `${winW}px`, height: `${winH}px`, borderRadius: '3px', background: t.bg, border: `1px solid ${t.violetLine}` },
        div({ height: '10px', margin: '6px 6px 0', borderRadius: '2px', background: t.panel }),
        div({ height: `${winH - 60}px`, margin: '6px', borderRadius: '2px', background: t.panel }),
        div({ height: '8px', margin: '0 6px', borderRadius: '2px', background: t.raised })),
      div({ position: 'absolute', left: '0', right: '0', bottom: '0', height: `${taskbarH}px`, background: TASKBAR_DARK })),
    span({ fontSize: '12.5px', lineHeight: '18px', color: t.text2, maxWidth: `${screenW}px`, textWrap: 'pretty' }, 'Ноутбук 1366 × 768, масштаб 125%: рабочая область 1093 × 566 DIP. Окно сжимается до 400 × 550, список сервисов прокручивается.'));

  const icoSizes = div({ display: 'flex', flexWrap: 'wrap', gap: '6px' }, ['16', '20', '24', '32', '40', '48', '64', '256'].map((s) => chip(t, `${s} px`, 'violet')));

  return board(t, 1600, 1150,
    boardTitle(t, 'Масштабирование', 'Интерфейс задаётся в независимых пикселях (DIP): WebView2 сам умножает их на масштаб Windows. Растровые значки — отдельными кадрами под точный размер.'),
    div({ display: 'flex', alignItems: 'flex-start', gap: '40px' }, SCALES.map(column)),
    block(t, 'Размеры по масштабам', '', table),
    div({ display: 'grid', gridTemplateColumns: 'minmax(0, 1fr) 400px 420px', gap: '48px' },
      block(t, 'Правила', '',
        div({ display: 'flex', flexDirection: 'column', gap: '12px' },
          rule(1, 'Макет в DIP. Окно 400 × 640, минимум 360 × 520, открывается у трея.'),
          rule(2, 'Высота окна — не больше рабочей области минус 16 DIP. Шапка и нижние кнопки на месте, прокручивается середина.'),
          rule(3, 'При переносе окна на монитор с другим масштабом размер пересчитывается сразу (WM_DPICHANGED).'),
          rule(4, 'Текст не мельче 12 DIP, кнопки не ниже 30 DIP, значки интерфейса 16–20 DIP.'),
          rule(5, 'Свой масштаб в настройках умножается на системный, итог не больше 250%.'))),
      block(t, 'Маленький экран', '', smallScreen),
      block(t, 'Пиксельная сетка трея', 'Кадры 16 и 32 px под лупой: все грани на целых пикселях.',
        div({ display: 'flex', alignItems: 'flex-end', gap: '20px' }, pixelGlyph(16, 12), pixelGlyph(32, 6)),
        div({ display: 'flex', flexDirection: 'column', gap: '8px' }, span({ fontSize: '12.5px', color: t.text2 }, 'Кадры в .ico приложения'), icoSizes))),
  );
}

// ─── Сборка ───────────────────────────────────────────────────────────────

function doc(bg, body) {
  return `<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <script src="./support.js"></script>
</head>
<body>
<x-dc>
<helmet>
  <link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Onest:wght@400;500;600;700&amp;family=JetBrains+Mono:wght@400;500&amp;display=swap">
  <style>
    body { margin: 0; background: ${bg}; }
    * { box-sizing: border-box; }
    a { color: #B294F7; text-decoration: none; }
    a:hover { color: #CBB6FA; }
  </style>
</helmet>
${body}
</x-dc>
</body>
</html>
`;
}

const ARTBOARDS = [
  // file, title, page, x, y, w, h, bg, render
  ['FirstRun.dc.html', 'Первый запуск', 'page-1', 0, 0, 400, 640, DARK.bg, () => firstRunScreen(DARK)],
  ['Main.dc.html', 'Главное окно', 'page-1', 480, 0, 400, 640, DARK.bg, () => mainScreen(DARK)],
  ['MainLight.dc.html', 'Главное окно · светлая тема', 'page-1', 960, 0, 400, 640, LIGHT.bg, () => mainScreen(LIGHT)],
  ['Diagnose.dc.html', 'Не работает? · выбор', 'page-1', 1440, 0, 400, 640, DARK.bg, () => diagnoseScreen(DARK)],
  ['DiagnoseResult.dc.html', 'Не работает? · результат', 'page-1', 1920, 0, 400, 640, DARK.bg, () => diagnoseResultScreen(DARK)],
  ['Fixing.dc.html', 'Не работает? · исправление', 'page-1', 2400, 0, 400, 640, DARK.bg, () => fixingScreen(DARK)],
  ['AddSite.dc.html', 'Добавить сайт', 'page-1', 0, 780, 400, 720, DARK.bg, () => addSiteScreen(DARK)],
  ['Settings.dc.html', 'Настройки', 'page-1', 480, 780, 400, 1040, DARK.bg, () => settingsScreen(DARK)],
  ['TrayMenu.dc.html', 'Трей и уведомление', 'page-1', 960, 780, 760, 600, '#1C1921', () => trayScreen(DARK)],
  ['Tokens.dc.html', 'Основа', 'page-2', 0, 0, 1280, 1340, DARK.bg, tokensBoard],
  ['Components.dc.html', 'Компоненты', 'page-2', 1360, 0, 1280, 1070, DARK.bg, componentsBoard],
  ['Icons.dc.html', 'Значки', 'page-2', 0, 1460, 1280, 1640, DARK.bg, iconsBoard],
  ['Scaling.dc.html', 'Масштабирование', 'page-2', 1360, 1460, 1600, 1150, DARK.bg, scalingBoard],
];

const canvas = {
  pages: [{ id: 'page-1', name: 'Экраны' }, { id: 'page-2', name: 'Система' }],
  artboards: ARTBOARDS.map(([file, title, page, x, y, w, h]) => ({ file, title, page, x, y, w, h })),
  annotations: [
    {
      id: 'screens-note', page: 'page-1', x: 0, y: -230, w: 560,
      text: 'Статичные макеты окна FI для Windows. Окно 400 × 640 DIP открывается у трея, высота растёт по содержимому (настройки, список сайтов прокручиваются).\n\nФиолетовый — работает и основное действие. Красный — знак бренда и «не работает». Янтарный — частичные сбои.\n\nСтратегии и цифры — пример на основе первого прогона стенда.',
    },
    {
      id: 'system-note', page: 'page-2', x: 0, y: -170, w: 520,
      text: 'Система: цвета и шрифты, компоненты, значки приложения и трея по состояниям, правила масштабирования 100–200%.',
    },
  ],
  launch: { view: 'canvas', page: 'page-1' },
};

for (const [file, , , , , , , bg, render] of ARTBOARDS) {
  writeFileSync(join(OUT, file), doc(bg, render()));
}
writeFileSync(join(OUT, 'canvas.json'), JSON.stringify(canvas, null, 2) + '\n');
console.log(`Артбордов: ${ARTBOARDS.length} → ${OUT}`);
