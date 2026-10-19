// One live pane in xterm.js, over WS /api/term.
//
//   observe  the default. The page asks for the size that fits its box; the dock renders
//            the pane at that size and restarts on resize. Input is off.
//   control  opened with cols=0&rows=0, so the pane keeps its real size: a controller
//            resizes the real pane for everyone, so that only happens on "Fit to window".
//
// xterm is always exactly frame.width × frame.height; the page scales it to its box by
// font size, then a small CSS scale for the remainder. A dropped socket reconnects in
// observe mode only. Input is never resent.

import { Terminal, type ITheme } from "@xterm/xterm";
import { WebglAddon } from "@xterm/addon-webgl";
import { WebLinksAddon } from "@xterm/addon-web-links";
import "@xterm/xterm/css/xterm.css";
import { openSocket } from "./api";
import type { TermClientMessage, TermMode, TermServerMessage } from "./types";

export interface TermTarget {
  dock: string;
  session: string;
  /** The herdr server the pane was seen on; the hub refuses a stale one. */
  epoch: string;
  pane: string;
  /** The pane's terminal_id from the same snapshot. */
  terminal: string;
}

export type TermPhase =
  | "connecting"
  | "open"
  /** the server ended the stream */
  | "closed"
  | "error"
  /** the socket dropped without a word */
  | "lost"
  /** the herdr server restarted since the snapshot this view was opened from */
  | "stale"
  | "stopped";

export interface TermState {
  mode: TermMode;
  phase: TermPhase;
  reason?: string;
  /** A control session dropped: what was typed last may or may not have arrived. */
  lostInput: boolean;
  /** A control session ended from the other side (taken over, released, expired). */
  controlEnded?: string;
  /** Control was refused: another controller holds the pane (the reason, if given). */
  busy?: string;
  cols: number;
  rows: number;
  renderer: "webgl" | "dom";
  retryIn?: number;
}

export interface TermOptions {
  onState: (s: TermState) => void;
  /** A link was clicked in the terminal. Its text is untrusted: ask before opening. */
  onLink: (uri: string) => void;
  /** The hub said the epoch is stale. Fetch a fresh snapshot, then retarget(). */
  onStale: () => void;
}

const FACE = '"Space Mono", ui-monospace, monospace';
const MIN_FONT = 6;
const MAX_FONT = 18;

// ---------------------------------------------------------------- Marbell Ember in 256 colours

function palette256(): string[] {
  const hex = (n: number) => n.toString(16).padStart(2, "0");
  const out: string[] = [];
  const steps = [0, 95, 135, 175, 215, 255];
  for (let r = 0; r < 6; r++)
    for (let g = 0; g < 6; g++)
      for (let b = 0; b < 6; b++) out.push(`#${hex(steps[r])}${hex(steps[g])}${hex(steps[b])}`);
  for (let i = 0; i < 24; i++) out.push(`#${hex(8 + i * 10).repeat(3)}`);
  // The two signal colours, in the slots Marbell Ember gives them (200 working, 201 blocked).
  out[200 - 16] = "#ffb52e";
  out[201 - 16] = "#ff2e7e";
  return out;
}

// A solid background, as in Ghostty's Marbell Ember theme. A transparent one
// makes the WebGL renderer draw a dark box behind every word on lines where
// a program paints its own background (Claude Code's prompt and recap lines).
export const EMBER_THEME: ITheme = {
  background: "#070b16",
  foreground: "#c9d3e3",
  cursor: "#f47853",
  cursorAccent: "#070b16",
  selectionBackground: "#26365a",
  selectionForeground: "#f4f7fb",
  black: "#111829",
  red: "#e5484d",
  green: "#4fd1c5",
  yellow: "#f2b866",
  blue: "#6e9bf5",
  magenta: "#f47853",
  cyan: "#5cc8f0",
  white: "#c9d3e3",
  brightBlack: "#687a9d",
  brightRed: "#ff6b6f",
  brightGreen: "#7ce8dd",
  brightYellow: "#ffd08a",
  brightBlue: "#9db8ff",
  brightMagenta: "#ff9a7b",
  brightCyan: "#8fddfa",
  brightWhite: "#f4f7fb",
  extendedAnsi: palette256(),
};

/** Space Mono must be loaded before xterm measures its cells. */
export function fontsReady(): Promise<unknown> {
  const load = Promise.all([
    document.fonts.load(`400 14px ${FACE}`),
    document.fonts.load(`700 14px ${FACE}`),
  ]);
  return Promise.race([load, new Promise((r) => setTimeout(r, 1500))]);
}

const decode: (s: string) => Uint8Array =
  typeof (Uint8Array as unknown as { fromBase64?: unknown }).fromBase64 === "function"
    ? (s) => (Uint8Array as unknown as { fromBase64: (s: string) => Uint8Array }).fromBase64(s)
    : (s) => {
        const bin = atob(s);
        const out = new Uint8Array(bin.length);
        for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
        return out;
      };

// ---------------------------------------------------------------- the view

export class TermView {
  readonly term: Terminal;
  private readonly frame: HTMLDivElement;
  private ws?: WebSocket;
  private target: TermTarget;
  private state: TermState;
  private lastSeq = 0;
  private awaitFull = true;
  /** The size last asked of an observer. */
  private asked = { cols: 0, rows: 0 };
  /** Watching the whole pane at its real size (scaled down to fit), instead of at a size that fits. */
  private whole: { cols: number; rows: number } | null = null;
  /** CSS px per font px, measured from xterm's own screen at its current font size. */
  private ratio = { w: 0.6, h: 1.2 };
  /** The same, per font size: cells round to device pixels, so the ratio moves with the size. */
  private readonly ratios = new Map<number, { w: number; h: number }>();
  private attempts = 0;
  private retry?: ReturnType<typeof setTimeout>;
  private resizeTimer?: ReturnType<typeof setTimeout>;
  private fitQueued = 0;
  private wheelAcc = 0;
  private wheelAt = 0;
  private disposed = false;
  private readonly resizeObserver: ResizeObserver;
  private gl?: WebglAddon;
  private dprQuery?: MediaQueryList;

  constructor(
    private readonly host: HTMLElement,
    target: TermTarget,
    private readonly opts: TermOptions,
  ) {
    this.target = target;
    this.state = { mode: "observe", phase: "connecting", lostInput: false, cols: 0, rows: 0, renderer: "dom" };

    this.frame = document.createElement("div");
    this.frame.className = "term-frame";
    host.append(this.frame);

    this.term = new Terminal({
      fontFamily: FACE,
      fontSize: this.preferredFont(),
      lineHeight: 1,
      letterSpacing: 0,
      scrollback: 0,
      allowTransparency: false,
      theme: EMBER_THEME,
      disableStdin: true,
      cursorBlink: false,
      cursorStyle: "block",
      cursorInactiveStyle: "none",
      drawBoldTextInBrightColors: false,
      minimumContrastRatio: 1,
      customGlyphs: true,
      rescaleOverlappingGlyphs: true,
      macOptionIsMeta: true,
      linkHandler: {
        allowNonHttpProtocols: false,
        activate: (_e, uri) => this.opts.onLink(uri),
      },
    });
    this.term.open(this.frame);
    this.term.textarea?.setAttribute("inputmode", "none");
    this.term.textarea?.setAttribute("aria-label", "Terminal");

    try {
      const gl = new WebglAddon();
      gl.onContextLoss(() => {
        // xterm falls back to its DOM renderer once the addon is gone
        gl.dispose();
        this.gl = undefined;
        this.set({ renderer: "dom" });
      });
      this.term.loadAddon(gl);
      this.gl = gl;
      this.state.renderer = "webgl";
    } catch {
      this.state.renderer = "dom";
    }
    this.term.loadAddon(new WebLinksAddon((_e, uri) => this.opts.onLink(uri)));

    // xterm turns the wheel into arrow keys on the alternate screen; here the wheel
    // scrolls the real pane, and only while in control.
    this.term.attachCustomWheelEventHandler((ev) => {
      this.wheel(ev);
      return false;
    });
    this.term.onData((text) => {
      if (this.state.mode === "control" && this.state.phase === "open") this.send({ type: "input", text });
    });

    this.measure();
    this.resizeObserver = new ResizeObserver(() => this.onBoxResize());
    this.resizeObserver.observe(host);
    this.watchPixelRatio();
  }

  /** A window moved to another screen (or zoomed): redraw the glyphs and refit. */
  private readonly onPixelRatio = () => {
    this.gl?.clearTextureAtlas();
    this.term.refresh(0, this.term.rows - 1);
    this.queueFit();
    this.watchPixelRatio();
  };
  private watchPixelRatio() {
    this.dprQuery?.removeEventListener("change", this.onPixelRatio);
    this.dprQuery = matchMedia(`(resolution: ${devicePixelRatio}dppx)`);
    this.dprQuery.addEventListener("change", this.onPixelRatio);
  }

  // ---------------------------------------------------------------- public

  get mode(): TermMode {
    return this.state.mode;
  }

  /** Open (or reopen) the stream. Control never starts on its own: only from a click. */
  connect(mode: TermMode, takeover = false) {
    if (this.disposed) return;
    this.closeSocket(false);
    clearTimeout(this.retry);
    this.retry = undefined;
    this.awaitFull = true;
    this.lastSeq = 0;
    const size = mode === "observe" ? this.observeSize() : { cols: 0, rows: 0 };
    if (mode === "observe") this.asked = size;
    const q = new URLSearchParams({
      dock: this.target.dock,
      session: this.target.session,
      epoch: this.target.epoch,
      pane: this.target.pane,
      terminal: this.target.terminal,
      mode,
      takeover: takeover ? "1" : "0",
      cols: String(size.cols),
      rows: String(size.rows),
    });
    const sock = openSocket(`/api/term?${q}`);
    this.ws = sock;
    this.term.options.disableStdin = mode !== "control";
    this.term.textarea?.setAttribute("inputmode", mode === "control" ? "text" : "none");
    this.set({
      mode,
      phase: "connecting",
      reason: undefined,
      retryIn: undefined,
      controlEnded: mode === "control" ? undefined : this.state.controlEnded,
      lostInput: mode === "control" ? false : this.state.lostInput,
      busy: mode === "control" ? undefined : this.state.busy,
    });

    let ended = false; // the server said why before closing
    sock.onmessage = (e) => {
      if (this.ws !== sock || typeof e.data !== "string") return;
      let msg: TermServerMessage;
      try {
        msg = JSON.parse(e.data) as TermServerMessage;
      } catch {
        return;
      }
      if (msg.type === "frame") this.frameIn(msg);
      else if (msg.type === "status") {
        if (msg.state !== "open") ended = true;
        this.statusIn(msg.state, msg.reason);
      }
    };
    sock.onclose = () => {
      if (this.ws !== sock) return;
      this.ws = undefined;
      if (ended || this.disposed) return;
      // Dropped without a word. In control, typing may have been lost in flight.
      const wasControl = this.state.mode === "control";
      this.set({ phase: "lost", lostInput: wasControl || this.state.lostInput, reason: undefined });
      this.later();
    };
  }

  takeControl(takeover = false) {
    this.connect("control", takeover);
  }

  /** Give the pane back and keep watching. */
  release() {
    if (this.state.mode === "control") this.closeSocket(true);
    this.connect("observe");
  }

  /** In control: type text at the cursor (a pasted file's path). False if not in control now. */
  typeInput(text: string): boolean {
    if (this.state.mode !== "control" || this.state.phase !== "open" || !text) return false;
    this.send({ type: "input", text });
    return true;
  }

  /** In control: make the real pane the size of this box. */
  fitToWindow() {
    if (this.state.mode !== "control" || this.state.phase !== "open") return;
    const { cols, rows } = this.wantedSize();
    this.send({ type: "resize", cols, rows });
  }

  /** The hub moved on to a new herdr server: point at the pane there and watch it. */
  retarget(target: TermTarget) {
    this.target = target;
    this.term.reset();
    this.connect("observe");
  }

  /** Stop streaming (machine offline, pane gone). */
  stop(reason?: string) {
    clearTimeout(this.retry);
    this.retry = undefined;
    this.closeSocket(this.state.mode === "control");
    this.set({ phase: "stopped", reason, mode: "observe" });
  }

  dismissNotices() {
    this.set({ lostInput: false, controlEnded: undefined, busy: undefined });
  }

  focus() {
    this.term.focus();
  }

  dispose() {
    if (this.disposed) return;
    clearTimeout(this.retry);
    clearTimeout(this.resizeTimer);
    cancelAnimationFrame(this.fitQueued);
    this.closeSocket(this.state.mode === "control");
    this.disposed = true;
    this.resizeObserver.disconnect();
    this.dprQuery?.removeEventListener("change", this.onPixelRatio);
    this.term.dispose();
    this.frame.remove();
  }

  // ---------------------------------------------------------------- the stream

  private send(msg: TermClientMessage) {
    if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(JSON.stringify(msg));
  }

  private closeSocket(release: boolean) {
    const ws = this.ws;
    this.ws = undefined;
    if (!ws) return;
    // A close frame goes out after anything already queued, so the release arrives first.
    if (release && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: "release" }));
    if (ws.readyState === WebSocket.CONNECTING || ws.readyState === WebSocket.OPEN) ws.close(1000);
  }

  private frameIn(f: Extract<TermServerMessage, { type: "frame" }>) {
    if (!f.full && this.awaitFull) return; // a new stream starts with a full frame
    if (!f.full && this.lastSeq && f.seq !== this.lastSeq + 1 && this.state.mode === "observe") {
      // A gap no full frame has filled yet: a fresh observer starts with one.
      this.connect("observe");
      return;
    }
    this.awaitFull = false;
    this.lastSeq = f.seq;
    let resized = false;
    if (f.width > 0 && f.height > 0 && (f.width !== this.term.cols || f.height !== this.term.rows)) {
      this.term.resize(f.width, f.height);
      resized = true;
    }
    if (f.full) this.term.reset();
    this.term.write(decode(f.data));
    if (resized || this.state.cols !== f.width || this.state.rows !== f.height) {
      this.set({ cols: f.width, rows: f.height });
      this.queueFit();
    }
  }

  private statusIn(state: "open" | "closed" | "error" | "busy", reason?: string) {
    if (state !== "open") clearTimeout(this.resizeTimer);
    if (state === "open") {
      this.attempts = 0;
      this.set({ phase: "open", reason: undefined, retryIn: undefined });
      if (this.state.mode === "control") this.term.focus();
      else this.reconcileObserver(); // the preferred size may have changed while it connected
      return;
    }
    if (state === "busy") {
      // Someone else holds the pane. Offer a takeover; meanwhile keep watching.
      this.set({ busy: reason || "another controller" });
      this.watchAgain();
      return;
    }
    if (state === "error" && reason === "stale") {
      this.set({ phase: "stale", reason, mode: "observe" });
      this.opts.onStale();
      return;
    }
    if (this.state.mode === "control") {
      // Control ended from the other side; go back to watching. Never retake it.
      this.set({ controlEnded: reason || (state === "error" ? "error" : "closed") });
      this.watchAgain();
      return;
    }
    this.set({ phase: state, reason });
    this.later();
  }

  /** After control ends or is refused: watch again, once the socket has closed. */
  private watchAgain() {
    queueMicrotask(() => {
      if (!this.disposed) this.connect("observe");
    });
  }

  /** Reconnect in observe mode with backoff. */
  private later() {
    if (this.disposed || this.retry) return;
    this.attempts++;
    const delay = Math.min(15_000, 500 * 2 ** (this.attempts - 1)) * (0.75 + Math.random() * 0.5);
    this.set({ retryIn: Math.round(delay) });
    this.retry = setTimeout(() => {
      this.retry = undefined;
      this.connect("observe");
    }, delay);
  }

  private set(patch: Partial<TermState>) {
    Object.assign(this.state, patch);
    this.opts.onState({ ...this.state });
  }

  // ---------------------------------------------------------------- sizing

  /** CSS px per font px, from the screen xterm laid out. */
  private measure() {
    const screen = this.frame.querySelector<HTMLElement>(".xterm-screen");
    const fs = this.term.options.fontSize ?? 13;
    if (!screen || !this.term.cols || !this.term.rows) return;
    const w = screen.offsetWidth / this.term.cols / fs;
    const h = screen.offsetHeight / this.term.rows / fs;
    if (w > 0.2 && h > 0.5) {
      this.ratio = { w, h };
      this.ratios.set(fs, this.ratio);
    }
  }

  /** The font size a requested size is planned at: smaller on a phone. */
  private preferredFont() {
    return this.box().w < 560 ? 10.5 : 13;
  }

  private box() {
    const cs = getComputedStyle(this.host);
    const w = this.host.clientWidth - parseFloat(cs.paddingLeft) - parseFloat(cs.paddingRight);
    const h = this.host.clientHeight - parseFloat(cs.paddingTop) - parseFloat(cs.paddingBottom);
    return { w: Math.max(0, w), h: Math.max(0, h) };
  }

  /**
   * Watch the pane at its real size, scaled down to fit (nothing cut off at the right), or
   * (null) at a size that fills the box at a readable font. Only changes watching.
   */
  watchWhole(size: { cols: number; rows: number } | null) {
    this.whole = size; // kept through control; only watching uses it
    this.reconcileObserver();
  }

  /** An open observer asks again if the size it should watch at differs from the one it asked for. */
  private reconcileObserver() {
    if (this.state.mode !== "observe" || this.state.phase !== "open") return;
    const want = this.observeSize();
    if (want.cols === this.asked.cols && want.rows === this.asked.rows) return;
    this.asked = want;
    this.awaitFull = true;
    this.lastSeq = 0;
    this.send({ type: "resize", ...want });
  }

  private observeSize() {
    return this.whole ?? this.wantedSize();
  }

  /** The size that fills this box at a comfortable font size. */
  wantedSize() {
    const { w, h } = this.box();
    if (w < 40 || h < 40) return { cols: 100, rows: 30 };
    const font = this.preferredFont();
    const ratio = this.ratios.get(font) ?? this.ratio;
    const cols = Math.floor(w / (font * ratio.w));
    const rows = Math.floor(h / (font * ratio.h));
    return { cols: Math.max(20, Math.min(400, cols)), rows: Math.max(6, Math.min(200, rows)) };
  }

  private onBoxResize() {
    this.queueFit();
    if (this.state.mode !== "observe") return;
    clearTimeout(this.resizeTimer);
    this.resizeTimer = setTimeout(() => {
      if (this.state.mode !== "observe" || this.state.phase !== "open") return;
      const want = this.observeSize();
      if (want.cols === this.asked.cols && want.rows === this.asked.rows) return;
      this.asked = want;
      // The dock restarts the observer at the new size; its stream begins with a full frame.
      this.awaitFull = true;
      this.lastSeq = 0;
      this.send({ type: "resize", ...want });
    }, 300);
  }

  private queueFit() {
    if (this.fitQueued) return;
    this.fitQueued = requestAnimationFrame(() => {
      this.fitQueued = 0;
      this.fit();
    });
  }

  /** Scale xterm into the box: a font size that nearly fits, then a CSS scale ≤ 1 for the rest. */
  private fit() {
    if (this.disposed) return;
    const { w, h } = this.box();
    if (w < 10 || h < 10) return;
    this.measure();
    const cols = this.term.cols;
    const rows = this.term.rows;
    const ideal = Math.min(w / (cols * this.ratio.w), h / (rows * this.ratio.h));
    const font = Math.max(MIN_FONT, Math.min(MAX_FONT, Math.floor(ideal * 2) / 2));
    if (font !== this.term.options.fontSize) this.term.options.fontSize = font;
    const screen = this.frame.querySelector<HTMLElement>(".xterm-screen");
    const sw = screen?.offsetWidth || cols * font * this.ratio.w;
    const sh = screen?.offsetHeight || rows * font * this.ratio.h;
    const scale = Math.min(1, w / sw, h / sh);
    this.frame.style.transform = scale < 0.999 ? `scale(${scale})` : "";
    this.frame.style.width = `${sw}px`;
    this.frame.style.height = `${sh}px`;
  }

  private wheel(ev: WheelEvent) {
    if (this.state.mode !== "control" || this.state.phase !== "open") return;
    ev.preventDefault();
    const cell = (this.term.options.fontSize ?? 13) * this.ratio.h;
    this.wheelAcc += ev.deltaMode === 1 ? ev.deltaY : ev.deltaMode === 2 ? ev.deltaY * this.term.rows : ev.deltaY / cell;
    const now = performance.now();
    if (Math.abs(this.wheelAcc) < 1 || now - this.wheelAt < 40) return;
    const lines = Math.trunc(this.wheelAcc);
    this.wheelAcc -= lines;
    this.wheelAt = now;
    this.send({ type: "scroll", direction: lines < 0 ? "up" : "down", lines: Math.min(50, Math.abs(lines)) });
  }
}
