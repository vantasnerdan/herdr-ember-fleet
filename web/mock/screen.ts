// Synthetic terminal screens for the mock hub: a made-up agent TUI (or a shell) drawn
// as ANSI rows, the way herdr's terminal frames carry them. All text here is invented.

export type RGB = readonly [number, number, number];

export interface Span {
  t: string;
  fg?: RGB;
  bg?: RGB;
  bold?: boolean;
}
export type Line = Span[];

export const C = {
  text: [201, 211, 227],
  bright: [244, 247, 251],
  dim: [104, 122, 157],
  coral: [244, 120, 83],
  teal: [79, 209, 197],
  amber: [242, 184, 102],
  red: [229, 72, 77],
  blue: [110, 155, 245],
  violet: [157, 184, 255],
  working: [255, 181, 46],
  blocked: [255, 46, 126],
  inputBg: [17, 24, 41],
} as const satisfies Record<string, RGB>;

const ESC = "\x1b";

function sgr(s: Span): string {
  const parts = ["0"];
  if (s.bold) parts.push("1");
  if (s.fg) parts.push(`38;2;${s.fg.join(";")}`);
  if (s.bg) parts.push(`48;2;${s.bg.join(";")}`);
  return `${ESC}[${parts.join(";")}m`;
}

/** One row as ANSI, exactly `cols` cells wide (every character here is one cell). */
export function renderLine(line: Line, cols: number, fill?: RGB): string {
  let out = "";
  let used = 0;
  for (const span of line) {
    if (used >= cols) break;
    const chars = [...span.t];
    const take = chars.slice(0, cols - used);
    out += sgr(span) + take.join("");
    used += take.length;
  }
  if (used < cols) out += sgr({ t: "", bg: fill }) + " ".repeat(cols - used);
  return out + `${ESC}[0m`;
}

export const span = (t: string, fg?: RGB, bold = false, bg?: RGB): Span => ({ t, fg, bold, bg });

/** Wraps plain text to a width, keeping words whole where it can. */
export function wrap(text: string, width: number): string[] {
  if (width < 4) return [text.slice(0, Math.max(1, width))];
  const out: string[] = [];
  for (const para of text.split("\n")) {
    let line = "";
    for (const word of para.split(" ")) {
      if (!line) line = word;
      else if (line.length + 1 + word.length <= width) line += " " + word;
      else {
        out.push(line);
        line = word;
      }
      while (line.length > width) {
        out.push(line.slice(0, width));
        line = line.slice(width);
      }
    }
    out.push(line);
  }
  return out;
}

// ---------------------------------------------------------------- what a pane shows

export type Entry =
  | { kind: "prompt"; text: string }
  | { kind: "say"; text: string }
  | { kind: "tool"; text: string; ok?: boolean }
  | { kind: "diff"; add: boolean; text: string }
  | { kind: "note"; text: string }
  | { kind: "cmd"; text: string }
  | { kind: "out"; text: string };

export interface ScreenModel {
  harness?: string;
  harnessName: string;
  user: string;
  host: string;
  cwd: string;
  status: string;
  history: Entry[];
  input: string;
  question?: { title: string; options: string[]; choice: number };
  /** seconds the current work has run */
  elapsed: number;
  tick: number;
  /** lines scrolled up from the bottom */
  scroll: number;
}

const SPIN = ["✻", "✶", "✷", "✸", "✹", "✺"];

function entryLines(e: Entry, width: number): Line[] {
  switch (e.kind) {
    case "prompt":
      return wrap(e.text, width - 2).map((l, i) => [span(i ? "  " : "› ", C.coral, true), span(l, C.bright, true)]);
    case "say":
      return wrap(e.text, width - 2).map((l) => [span("  "), span(l, C.text)]);
    case "tool":
      return wrap(e.text, width - 2).map((l, i) => [span(i ? "  " : "● ", e.ok === false ? C.red : C.teal), span(l, C.text)]);
    case "diff":
      return [[span("    "), span(`${e.add ? "+" : "-"} ${e.text}`, e.add ? C.teal : C.red)]];
    case "note":
      return wrap(e.text, width - 2).map((l) => [span("  "), span(l, C.dim)]);
    case "cmd":
      return [[span("$ ", C.coral, true), span(e.text, C.bright)]];
    case "out":
      return wrap(e.text, width).map((l) => [span(l, C.text)]);
  }
}

function frame(cols: number, title?: string): Line {
  const inner = Math.max(0, cols - 2);
  const t = title ? `─ ${title} ` : "";
  return [span("╭" + (t + "─".repeat(inner)).slice(0, inner) + "╮", C.dim)];
}
const bottom = (cols: number): Line => [span("╰" + "─".repeat(Math.max(0, cols - 2)) + "╯", C.dim)];
function boxed(cols: number, content: Line, fg: RGB = C.dim): Line {
  const used = content.reduce((n, s) => n + [...s.t].length, 0);
  const pad = Math.max(0, cols - 2 - used);
  return [span("│", fg), ...content, span(" ".repeat(pad)), span("│", fg)];
}

/** The pane's screen at this size: rows of ANSI, and where the cursor sits. */
export function renderScreen(m: ScreenModel, cols: number, rows: number): { rows: string[]; cursor: [number, number] } {
  const lines: Line[] = [];
  let cursor: [number, number] = [rows, 1];

  if (!m.harness) {
    // a shell: history scrolls, the prompt sits at the end
    const body: Line[] = m.history.flatMap((e) => entryLines(e, cols));
    const prompt: Line = [
      span(`${m.user}@${m.host}`, C.teal, true),
      span(":", C.dim),
      span(m.cwd, C.blue, true),
      span("$ ", C.dim),
      span(m.input, C.bright),
    ];
    body.push(prompt);
    const start = Math.max(0, body.length - rows - m.scroll);
    const shown = body.slice(start, start + rows);
    while (shown.length < rows) shown.push([]);
    const promptRow = shown.indexOf(prompt);
    if (promptRow >= 0) cursor = [promptRow + 1, Math.min(cols, prompt.reduce((n, s) => n + [...s.t].length, 0) + 1)];
    return { rows: shown.map((l) => renderLine(l, cols)), cursor };
  }

  // an agent TUI: a header, the conversation, the question or the spinner, the input box, a status line
  const header: Line = [
    span(` ${m.harnessName} `, C.coral, true),
    span(` ${m.cwd}`, C.dim),
  ];
  const foot: Line[] = [];
  if (m.status === "blocked" && m.question) {
    const q = m.question;
    foot.push(frame(cols, "needs your answer"));
    for (const l of wrap(q.title, cols - 4)) foot.push(boxed(cols, [span(" "), span(l, C.bright, true)]));
    q.options.forEach((o, i) => {
      const sel = i === q.choice;
      foot.push(boxed(cols, [span(sel ? " › " : "   ", C.coral, true), span(`${i + 1}. ${o}`, sel ? C.bright : C.text, sel)]));
    });
    foot.push(bottom(cols));
  } else if (m.status === "working") {
    foot.push([
      span(` ${SPIN[m.tick % SPIN.length]} `, C.coral, true),
      span(`Working… (${m.elapsed}s · esc to interrupt)`, C.amber),
    ]);
  } else if (m.status === "done") {
    foot.push([span(" ✓ ", C.teal, true), span("Done. Waiting for the next task.", C.dim)]);
  } else foot.push([]);

  const inputLines = wrap(m.input || "", Math.max(4, cols - 6));
  const box: Line[] = [frame(cols)];
  inputLines.forEach((l, i) =>
    box.push(boxed(cols, [span(i ? "   " : " > ", C.coral, true), span(l || "", C.bright)])),
  );
  box.push(bottom(cols));
  const status: Line = [
    span(` ${m.harness}`, C.dim),
    span(" · ", C.dim),
    span(m.status, m.status === "working" ? C.working : m.status === "blocked" ? C.blocked : C.dim),
    span(` · ${m.user}@${m.host}`, C.dim),
  ];

  const fixed = 2 + foot.length + box.length + 1;
  const room = Math.max(1, rows - fixed);
  const body: Line[] = m.history.flatMap((e) => [...entryLines(e, cols), [] as Line]);
  const start = Math.max(0, body.length - room - m.scroll);
  const shown = body.slice(start, start + room);
  while (shown.length < room) shown.unshift([]);

  lines.push(header, []);
  lines.push(...shown, ...foot, ...box, status);
  const out = lines.slice(0, rows);
  while (out.length < rows) out.push([]);

  // 1-based: header, blank, the body, the footer, the box's top border, then the input lines
  const inputRow = 2 + room + foot.length + 1 + inputLines.length;
  const lastInput = inputLines[inputLines.length - 1] ?? "";
  cursor = [Math.min(rows, inputRow), Math.min(cols, 5 + [...lastInput].length)];
  return { rows: out.map((l) => renderLine(l, cols)), cursor };
}

/** A full frame: clear, then every row; a partial frame: only the rows that changed. */
export function fullFrame(rows: string[], cursor: [number, number], showCursor: boolean): string {
  let s = `${ESC}[?2026h${ESC}[?25l${ESC}[0m${ESC}[2J${ESC}[1;1H`;
  rows.forEach((r, i) => (s += `${ESC}[${i + 1};1H${r}`));
  s += `${ESC}[?2026l${ESC}[${cursor[0]};${cursor[1]}H${showCursor ? `${ESC}[?25h` : `${ESC}[?25l`}`;
  return s;
}

export function partialFrame(
  rows: string[],
  previous: string[],
  cursor: [number, number],
  showCursor: boolean,
): string | null {
  let s = "";
  rows.forEach((r, i) => {
    if (r !== previous[i]) s += `${ESC}[${i + 1};1H${r}`;
  });
  if (!s) return null;
  return `${ESC}[?2026h${ESC}[?25l${s}${ESC}[?2026l${ESC}[${cursor[0]};${cursor[1]}H${showCursor ? `${ESC}[?25h` : `${ESC}[?25l`}`;
}
