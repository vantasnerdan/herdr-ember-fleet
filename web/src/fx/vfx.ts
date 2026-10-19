// Ember Fleet effects: Marbell Ember's glass pass (ghostty/shaders/ember-glass.glsl),
// rebuilt for a page.
//
// Ghostty's shader filters the terminal's own pixels. A page can't read its pixels,
// so the same effects are layered instead:
//
//   plate    WebGL, behind the UI: the out-of-focus shot behind the glass, after
//            ember/make_plate.py. Warm source low left, cool spill top right, bokeh,
//            vignette, lens falloff, grain. Drawn once per resize.
//   light    WebGL, over the UI, click-through and additive:
//              ignition  one line of coral light runs down the window when it takes focus
//              caret     the text caret leaves a coral thread when it moves, and breathes
//              beacons   agent state glows: working breathes amber, blocked beats pink-red
//   banking  CSS (extras.css): a window without focus cools, then warms back up as
//            the ignition line runs.
//
// ?fx=0 turns all of it off. Reduced motion keeps the plate and holds beacons steady.
// Coral is the only thing that glows, apart from the two beacon colours.
//
// Ported from Ember Fleet's vfx.js. Here beacons follow herdr's agent states, and
// blocked agents keep their beacons first when there are more than the layer draws.

import { beacons } from "./brand";

const SWEEP_TIME = 0.55; // s
const THREAD_TIME = 0.16; // s, the caret's thread catches up
const MAX_BEACONS = 16;
// xterm's hidden input is a textarea too; the terminal has its own cursor.
const FIELD =
  'textarea:not(.xterm-helper-textarea), input:not([type]), input[type="text"], input[type="search"], input[type="url"], input[type="email"]';

const VERTEX = `#version 300 es
in vec2 p;
void main() { gl_Position = vec4(p, 0.0, 1.0); }`;

const PLATE = `#version 300 es
precision highp float;
uniform vec2 uRes;
uniform vec4 uBokeh[9];       // centre, radii (fractions of the window)
uniform vec4 uBokehColor[9];  // rgb, strength
out vec4 outColor;

vec3 blob(vec2 p, vec2 c, vec2 r, vec3 rgb, float a) {
  vec2 d = (p - c) / r;
  return exp(-dot(d, d)) * rgb * a;
}

float grain(vec2 p) {
  vec3 q = fract(p.xyx * 0.1031);
  q += dot(q, q.yzx + 33.33);
  return fract((q.x + q.y) * q.z);
}

void main() {
  vec2 p = vec2(gl_FragCoord.x / uRes.x, 1.0 - gl_FragCoord.y / uRes.y);
  vec3 c = vec3(7.0, 11.0, 22.0) / 255.0;
  // out-of-focus practicals: one warm source low left, cool spill top right, deep blue mid
  c += blob(p, vec2(0.10, 0.92), vec2(0.34, 0.42), vec3(244.0, 120.0, 83.0) / 255.0, 0.150);
  c += blob(p, vec2(0.93, 0.06), vec2(0.40, 0.46), vec3(79.0, 209.0, 197.0) / 255.0, 0.075);
  c += blob(p, vec2(0.55, 0.45), vec2(0.55, 0.60), vec3(40.0, 62.0, 120.0) / 255.0, 0.110);
  c += blob(p, vec2(0.80, 0.98), vec2(0.22, 0.20), vec3(242.0, 184.0, 102.0) / 255.0, 0.045);
  for (int i = 0; i < 9; i++) c += blob(p, uBokeh[i].xy, uBokeh[i].zw, uBokehColor[i].rgb, uBokehColor[i].a);
  c = clamp(c, 0.0, 1.0);
  vec2 v = (p - 0.5) * vec2(1.25, 1.5);
  c *= clamp(1.0 - 0.38 * dot(v, v), 0.0, 1.0);
  // lens falloff toward the corners, in linear light
  vec3 lin = mix(c / 12.92, pow((c + 0.055) / 1.055, vec3(2.4)), step(0.04045, c));
  vec2 l = p - 0.5;
  lin *= 1.0 - 0.44 * dot(l, l);
  c = mix(lin * 12.92, 1.055 * pow(max(lin, vec3(1e-9)), vec3(1.0 / 2.4)) - 0.055, step(0.0031308, lin));
  // fine silver grain dithers the gradients
  c += (grain(gl_FragCoord.xy) - 0.5) * 0.02;
  outColor = vec4(c, 1.0);
}`;

const LIGHT = `#version 300 es
precision highp float;
uniform vec2 uRes;       // CSS px
uniform float uScale;    // canvas px per CSS px
uniform float uTime;     // s
uniform float uLive;     // 1 while the window has focus and motion is allowed
uniform float uSweep;    // s since the window took focus
uniform vec4 uCaret;     // x, y, w, h in CSS px; h = 0 when no caret is shown
uniform vec4 uPrev;      // where the caret was
uniform float uCaretT;   // s since it moved
uniform float uBreath;   // 1 while the caret is shown in a focused window
uniform int uCount;
uniform vec4 uBeacon[${MAX_BEACONS}];  // x, y, w, h
uniform vec4 uClip[${MAX_BEACONS}];    // left, top, right, bottom of what is visible
uniform float uKind[${MAX_BEACONS}];   // 0 working, 1 blocked
out vec4 outColor;

const float TAU = 6.2831853;
const vec3 CORAL = vec3(0.957, 0.471, 0.325);
const vec3 WORKING = vec3(1.0, 0.710, 0.180);
const vec3 BLOCKED = vec3(1.0, 0.180, 0.494);

float sdBox(vec2 p, vec2 c, vec2 h) {
  vec2 q = abs(p - c) - h;
  return length(max(q, 0.0)) + min(max(q.x, q.y), 0.0);
}

float sdTaper(vec2 p, vec2 a, vec2 b, float ra, float rb) {
  vec2 pa = p - a, ba = b - a;
  float h = clamp(dot(pa, ba) / max(dot(ba, ba), 1e-4), 0.0, 1.0);
  return length(pa - ba * h) - mix(ra, rb, h);
}

void main() {
  vec2 px = vec2(gl_FragCoord.x, uRes.y * uScale - gl_FragCoord.y) / uScale;
  vec3 col = vec3(0.0);

  // beacons: working breathes slowly; blocked gives two soft beats and rests.
  // A window without focus is not animated, so there they hold steady.
  float breathe = mix(1.0, 0.5 + 0.5 * sin(uTime * TAU / 4.6), uLive);
  float p = fract(uTime / 2.6);
  float b1 = (p - 0.10) / 0.055;
  float b2 = (p - 0.30) / 0.070;
  float beat = mix(1.0, min(1.0, exp(-b1 * b1) + 0.65 * exp(-b2 * b2)), uLive);
  for (int i = 0; i < ${MAX_BEACONS}; i++) {
    if (i >= uCount) break;
    vec4 k = uClip[i];
    if (px.x < k.x || px.y < k.y || px.x > k.z || px.y > k.w) continue;
    vec4 b = uBeacon[i];
    float d = sdBox(px, b.xy + b.zw * 0.5, b.zw * 0.5);
    // light around the shape, never on it; big cards glow less than dots
    float halo = exp(-max(d, 0.0) / 4.5) * step(0.0, d);
    float size = clamp(14.0 / min(b.z, b.w), 0.25, 1.0);
    bool blocked = uKind[i] > 0.5;
    col += (blocked ? BLOCKED : WORKING) * halo * size * 0.40 * (0.15 + 0.85 * (blocked ? beat : breathe));
  }

  // ignition: one thin line of coral light runs top to bottom on focus
  float t = uSweep / ${SWEEP_TIME.toFixed(2)};
  if (t >= 0.0 && t < 1.0) {
    float y = (1.0 - pow(1.0 - t, 3.0)) * uRes.y;
    float d = abs(px.y - y);
    float line = exp(-d * d / 6.0) + 0.35 * exp(-d / 28.0) * step(px.y, y);
    col += CORAL * line * 0.55 * (1.0 - t);
  }

  // caret: a tapered thread from where it was, then a halo that breathes
  vec2 size = uCaret.zw;
  float h = size.y;
  if (h >= 1.0) {
    vec2 cur = uCaret.xy + size * 0.5;
    vec2 prev = uPrev.xy + uPrev.zw * 0.5;
    float travel = distance(cur, prev);
    float ct = clamp(uCaretT / ${THREAD_TIME.toFixed(2)}, 0.0, 1.0);
    if (ct < 1.0 && travel >= 1.0) {
      float e = 1.0 - pow(1.0 - ct, 3.0);
      float d = sdTaper(px, mix(prev, cur, e), cur, 0.0, max(size.x, 2.0) * 0.5);
      float body = smoothstep(1.2, -1.2, d);
      float glow = exp(-max(d, 0.0) / (h * 0.18)) * 0.18;
      // long jumps (a click elsewhere, another field) get a quieter thread than typing
      float calm = mix(1.0, 0.45, smoothstep(h * 12.0, h * 40.0, travel));
      float bloom = exp(-distance(px, cur) / (h * 0.5)) * 0.05;
      col += CORAL * ((body * 0.6 + glow) * calm + bloom) * (1.0 - ct);
    }
    if (uBreath > 0.5) {
      float sd = sdBox(px, cur, size * 0.5);
      float breath = 0.5 + 0.5 * sin(uTime * TAU / 4.2);
      col += CORAL * exp(-max(sd, 0.0) / (h * 0.30)) * step(0.0, sd) * 0.14 * (0.30 + 0.70 * breath);
    }
  }

  float a = clamp(max(col.r, max(col.g, col.b)), 0.0, 1.0);
  outColor = vec4(min(col, vec3(a)), a);
}`;

// ------------------------------------------------------------------ WebGL bits

interface Layer {
  canvas: HTMLCanvasElement;
  gl: WebGL2RenderingContext;
}
type Uniform = (name: string) => WebGLUniformLocation | null;

function layer(z: number, blend?: string): Layer | null {
  const canvas = document.createElement("canvas");
  canvas.setAttribute("aria-hidden", "true");
  canvas.dataset.emberFx = "";
  canvas.style.cssText = `position:fixed;inset:0;width:100vw;height:100vh;pointer-events:none;z-index:${z};${blend ? `mix-blend-mode:${blend};` : ""}`;
  const gl = canvas.getContext("webgl2", {
    alpha: true,
    premultipliedAlpha: true,
    antialias: false,
    depth: false,
    stencil: false,
    powerPreference: "low-power",
  });
  return gl && { canvas, gl };
}

function program(gl: WebGL2RenderingContext, fragment: string): Uniform {
  const compile = (type: number, source: string) => {
    const shader = gl.createShader(type);
    if (!shader) throw new Error("shader");
    gl.shaderSource(shader, source);
    gl.compileShader(shader);
    if (!gl.getShaderParameter(shader, gl.COMPILE_STATUS)) throw new Error(gl.getShaderInfoLog(shader) || "shader");
    return shader;
  };
  const prog = gl.createProgram();
  if (!prog) throw new Error("program");
  gl.attachShader(prog, compile(gl.VERTEX_SHADER, VERTEX));
  gl.attachShader(prog, compile(gl.FRAGMENT_SHADER, fragment));
  gl.bindAttribLocation(prog, 0, "p");
  gl.linkProgram(prog);
  if (!gl.getProgramParameter(prog, gl.LINK_STATUS)) throw new Error(gl.getProgramInfoLog(prog) || "link");
  gl.useProgram(prog);
  // one triangle that covers the screen
  gl.bindBuffer(gl.ARRAY_BUFFER, gl.createBuffer());
  gl.bufferData(gl.ARRAY_BUFFER, new Float32Array([-1, -1, 3, -1, -1, 3]), gl.STATIC_DRAW);
  gl.enableVertexAttribArray(0);
  gl.vertexAttribPointer(0, 2, gl.FLOAT, false, 0, 0);
  const locations = new Map<string, WebGLUniformLocation | null>();
  return (name) => {
    if (!locations.has(name)) locations.set(name, gl.getUniformLocation(prog, name));
    return locations.get(name) ?? null;
  };
}

function fit(canvas: HTMLCanvasElement, gl: WebGL2RenderingContext, scale: number): boolean {
  const w = Math.max(1, Math.round(innerWidth * scale));
  const h = Math.max(1, Math.round(innerHeight * scale));
  if (canvas.width !== w || canvas.height !== h) {
    canvas.width = w;
    canvas.height = h;
    gl.viewport(0, 0, w, h);
    return true;
  }
  return false;
}

// The plate's bokeh, from a fixed seed like make_plate.py (seed 4).
function bokeh(seed = 4) {
  const next = () => {
    seed = (seed + 0x6d2b79f5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
  const range = (a: number, b: number) => a + (b - a) * next();
  const colours = [
    [244, 120, 83],
    [79, 209, 197],
    [60, 90, 170],
  ];
  const shapes: number[] = [];
  const tints: number[] = [];
  for (let i = 0; i < 9; i++) {
    shapes.push(range(0, 1), range(0, 1), range(0.03, 0.08), range(0.05, 0.12));
    const [r, g, b] = colours[Math.floor(next() * 3)];
    tints.push(r / 255, g / 255, b / 255, range(0.012, 0.035));
  }
  return { shapes: new Float32Array(shapes), tints: new Float32Array(tints) };
}

// ---------------------------------------------------------------- the page side

interface Box {
  x: number;
  y: number;
  w: number;
  h: number;
}

// Where the caret of a textarea or input is, in viewport px: a hidden copy of the
// field lays out the text before the caret, and a marker span lands where it is.
const COPY = [
  "boxSizing", "width", "borderTopWidth", "borderRightWidth", "borderBottomWidth", "borderLeftWidth",
  "paddingTop", "paddingRight", "paddingBottom", "paddingLeft", "fontStyle", "fontVariant", "fontWeight",
  "fontStretch", "fontSize", "fontFamily", "lineHeight", "textAlign", "textTransform", "textIndent",
  "letterSpacing", "wordSpacing", "tabSize", "wordBreak",
] as const;
let mirror: HTMLDivElement | null = null;

function caretOf(field: HTMLInputElement | HTMLTextAreaElement): Box | null {
  if (field.selectionStart == null || field.selectionStart !== field.selectionEnd) return null;
  const style = getComputedStyle(field);
  if (!mirror) {
    mirror = document.createElement("div");
    mirror.setAttribute("aria-hidden", "true");
  }
  // outside <body>, so measuring never wakes the beacon observer
  if (!mirror.isConnected) document.documentElement.append(mirror);
  const m = mirror.style as unknown as Record<string, string>;
  const s = style as unknown as Record<string, string>;
  for (const key of COPY) m[key] = s[key];
  const area = field.tagName === "TEXTAREA";
  Object.assign(mirror.style, {
    position: "absolute",
    visibility: "hidden",
    top: "0",
    left: "-9999px",
    height: "auto",
    whiteSpace: area ? "pre-wrap" : "pre",
    overflowWrap: area ? "break-word" : "normal",
    overflowY: area && field.scrollHeight > field.clientHeight ? "scroll" : "hidden",
  });
  mirror.textContent = field.value.slice(0, field.selectionStart);
  const marker = mirror.appendChild(document.createElement("span"));
  marker.textContent = field.value.slice(field.selectionStart) || ".";
  const box = field.getBoundingClientRect();
  const line = parseFloat(style.lineHeight) || parseFloat(style.fontSize) * 1.2;
  const x = box.left + marker.offsetLeft + parseFloat(style.borderLeftWidth) - field.scrollLeft;
  let y = box.top + marker.offsetTop + parseFloat(style.borderTopWidth) - field.scrollTop;
  if (!area) y = box.top + (box.height - line) / 2; // inputs centre their one line
  if (y + line < box.top || y > box.bottom || x < box.left - 1 || x > box.right + 1) return null;
  return { x: x - 1, y, w: 2, h: line };
}

// The ancestors that clip an element, so a beacon's halo stops where it does.
const clippers = new WeakMap<Element, Element[]>();
function visibleBox(el: Element): [number, number, number, number] | null {
  let list = clippers.get(el);
  if (!list) {
    list = [];
    for (let p = el.parentElement; p && p !== document.body; p = p.parentElement) {
      const s = getComputedStyle(p);
      if (s.overflowX !== "visible" || s.overflowY !== "visible") list.push(p);
    }
    clippers.set(el, list);
  }
  let [l, t, r, b] = [0, 0, innerWidth, innerHeight];
  for (const p of list) {
    const q = p.getBoundingClientRect();
    [l, t, r, b] = [Math.max(l, q.left), Math.max(t, q.top), Math.min(r, q.right), Math.min(b, q.bottom)];
  }
  return r > l && b > t ? [l, t, r, b] : null;
}

// ---------------------------------------------------------------------- start

declare global {
  interface Window {
    emberFx?: { stop: () => void };
  }
}

export function startEffects() {
  if (new URLSearchParams(location.search).get("fx") === "0") return;
  const reduced = matchMedia("(prefers-reduced-motion: reduce)");
  const plate = layer(-1);
  const light = layer(2147483001, "plus-lighter");
  if (!plate || !light) return;

  let u: Uniform;
  let p: Uniform;
  try {
    p = program(plate.gl, PLATE);
    const { shapes, tints } = bokeh();
    plate.gl.uniform4fv(p("uBokeh"), shapes);
    plate.gl.uniform4fv(p("uBokehColor"), tints);
    u = program(light.gl, LIGHT);
  } catch (error) {
    console.warn("Ember effects are off:", error);
    return;
  }

  const bank = document.createElement("div");
  bank.className = "ember-bank";
  bank.setAttribute("aria-hidden", "true");
  document.body.prepend(plate.canvas);
  document.body.append(bank, light.canvas);
  const root = document.documentElement;
  const now = () => performance.now() / 1000;

  let focused = document.hasFocus();
  let sweepAt = focused && !reduced.matches ? now() : -1e6; // ignition on open
  let caret: Box | null = null;
  let prev: Box | null = null;
  let caretAt = -1e6;
  let lit: { el: Element; kind: number }[] = [];
  let frame = 0;
  let last = 0;
  let stopped = false;
  root.classList.toggle("ember-banked", !focused);

  const drawPlate = () => {
    fit(plate.canvas, plate.gl, 1);
    plate.gl.uniform2f(p("uRes"), plate.canvas.width, plate.canvas.height);
    plate.gl.drawArrays(plate.gl.TRIANGLES, 0, 3);
  };

  const findBeacons = () => {
    const found: { el: Element; kind: number }[] = [];
    for (const [kind, selectors] of [
      [1, beacons.blocked],
      [0, beacons.working],
    ] as const) {
      for (const el of document.querySelectorAll(selectors.join(","))) found.push({ el, kind });
    }
    lit = found;
  };

  const drawLight = (t: number, live: boolean) => {
    const gl = light.gl;
    const scale = Math.min(devicePixelRatio || 1, 1.5);
    fit(light.canvas, gl, scale);
    gl.uniform2f(u("uRes"), innerWidth, innerHeight);
    gl.uniform1f(u("uScale"), light.canvas.width / innerWidth);
    gl.uniform1f(u("uTime"), t % 3600);
    gl.uniform1f(u("uLive"), live ? 1 : 0);
    gl.uniform1f(u("uSweep"), reduced.matches ? -1 : t - sweepAt);

    const c = caret ?? { x: 0, y: 0, w: 0, h: 0 };
    const q = prev ?? c;
    gl.uniform4f(u("uCaret"), c.x, c.y, c.w, c.h);
    gl.uniform4f(u("uPrev"), q.x, q.y, q.w, q.h);
    gl.uniform1f(u("uCaretT"), reduced.matches ? 1e6 : t - caretAt);
    gl.uniform1f(u("uBreath"), caret && live ? 1 : 0);

    const boxes: number[] = [];
    const clips: number[] = [];
    const kinds: number[] = [];
    for (const { el, kind } of lit) {
      if (kinds.length >= MAX_BEACONS) break;
      const r = el.getBoundingClientRect();
      if (!r.width || !r.height) continue;
      const clip = visibleBox(el);
      if (!clip) continue;
      if (r.right < clip[0] || r.left > clip[2] || r.bottom < clip[1] || r.top > clip[3]) continue;
      // skip beacons something else (a dialog) is covering
      const hit = document.elementFromPoint(
        Math.min(Math.max(r.left + r.width / 2, clip[0]), clip[2] - 1),
        Math.min(Math.max(r.top + r.height / 2, clip[1]), clip[3] - 1),
      );
      if (hit && !el.contains(hit) && !hit.contains(el)) continue;
      boxes.push(r.left, r.top, r.width, r.height);
      clips.push(...clip);
      kinds.push(kind);
    }
    gl.uniform1i(u("uCount"), kinds.length);
    if (kinds.length) {
      const pad = (a: number[], n: number) => new Float32Array([...a, ...new Array(MAX_BEACONS * n - a.length).fill(0)]);
      gl.uniform4fv(u("uBeacon"), pad(boxes, 4));
      gl.uniform4fv(u("uClip"), pad(clips, 4));
      gl.uniform1fv(u("uKind"), pad(kinds, 1));
    }
    gl.clearColor(0, 0, 0, 0);
    gl.clear(gl.COLOR_BUFFER_BIT);
    gl.drawArrays(gl.TRIANGLES, 0, 3);
    return kinds.length > 0;
  };

  const draw = (ms: number) => {
    frame = 0;
    if (stopped || document.hidden) return;
    const t = ms / 1000;
    const motion = !reduced.matches;
    const live = focused && motion;
    const busy = (motion && t - sweepAt < SWEEP_TIME) || (motion && caret && t - caretAt < THREAD_TIME);
    // slow breathing alone needs no more than 30 frames a second
    if (!busy && last && ms - last < 32) {
      frame = requestAnimationFrame(draw);
      return;
    }
    last = ms;
    const beaconsShown = drawLight(t, live);
    if (busy || (live && (caret || beaconsShown))) frame = requestAnimationFrame(draw);
    else last = 0;
  };
  const kick = () => {
    if (!frame && !stopped) frame = requestAnimationFrame(draw);
  };

  const moveCaret = (quiet: boolean) => {
    const el = document.activeElement;
    const next =
      (el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement) && el.matches(FIELD) ? caretOf(el) : null;
    if (!next) caret = prev = null;
    else if (!caret || quiet) caret = prev = next;
    else if (Math.abs(next.x - caret.x) > 0.5 || Math.abs(next.y - caret.y) > 0.5) {
      prev = caret;
      caret = next;
      caretAt = now();
    }
    kick();
  };
  let caretQueued = 0;
  const queueCaret = (quiet = false) => {
    if (caretQueued) return;
    caretQueued = requestAnimationFrame(() => {
      caretQueued = 0;
      moveCaret(quiet);
    });
  };
  let beaconsQueued = 0;
  const queueBeacons = () => {
    if (beaconsQueued) return;
    beaconsQueued = requestAnimationFrame(() => {
      beaconsQueued = 0;
      findBeacons();
      kick();
    });
  };

  const onFocus = () => {
    focused = true;
    root.classList.remove("ember-banked");
    if (!reduced.matches) sweepAt = now();
    queueCaret(true);
    kick();
  };
  const onBlur = () => {
    focused = false;
    root.classList.add("ember-banked");
    kick();
  };
  const onResize = () => {
    drawPlate();
    queueCaret(true);
    kick();
  };
  const onScroll = () => {
    queueCaret(true);
    kick();
  };
  const onCaret = () => queueCaret(false);
  const observer = new MutationObserver(queueBeacons);

  const stop = () => {
    stopped = true;
    cancelAnimationFrame(frame);
    observer.disconnect();
    window.removeEventListener("focus", onFocus);
    window.removeEventListener("blur", onBlur);
    window.removeEventListener("resize", onResize);
    document.removeEventListener("scroll", onScroll, true);
    document.removeEventListener("selectionchange", onCaret);
    document.removeEventListener("input", onCaret, true);
    document.removeEventListener("focusin", onCaret);
    document.removeEventListener("focusout", onCaret);
    document.removeEventListener("visibilitychange", kick);
    reduced.removeEventListener("change", kick);
    plate.canvas.remove();
    light.canvas.remove();
    bank.remove();
    mirror?.remove();
    root.classList.remove("ember-banked");
    delete root.dataset.emberFx;
  };
  for (const canvas of [plate.canvas, light.canvas]) {
    canvas.addEventListener("webglcontextlost", (event) => {
      event.preventDefault();
      stop();
    });
  }

  window.addEventListener("focus", onFocus);
  window.addEventListener("blur", onBlur);
  window.addEventListener("resize", onResize);
  document.addEventListener("scroll", onScroll, { capture: true, passive: true });
  document.addEventListener("selectionchange", onCaret);
  document.addEventListener("input", onCaret, true);
  document.addEventListener("focusin", onCaret);
  document.addEventListener("focusout", onCaret);
  document.addEventListener("visibilitychange", kick);
  reduced.addEventListener("change", kick);
  observer.observe(document.body, { subtree: true, childList: true, attributes: true, attributeFilter: ["class"] });

  drawPlate();
  findBeacons();
  moveCaret(true);
  kick();
  root.dataset.emberFx = "on";
  window.emberFx = { stop };
}
