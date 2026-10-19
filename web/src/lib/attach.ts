// Files for an agent: pasted, dropped or picked, uploaded at once to the agent's machine,
// and then named by their path in the prompt (or typed at the cursor, in control).

import { reactive } from "vue";
import { errorText, MAX_UPLOAD, uploadFile } from "./api";
import type { UploadTarget } from "./types";

export interface Attachment {
  id: number;
  file: File;
  name: string;
  size: number;
  /** An object URL, for images. */
  preview?: string;
  /** 0..1 */
  progress: number;
  state: "uploading" | "done" | "failed";
  /** Where it landed on the agent's machine. */
  path?: string;
  error?: string;
  abort?: () => void;
}

let seq = 0;

const stamp = () => {
  const d = new Date();
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}${p(d.getMonth() + 1)}${p(d.getDate())}-${p(d.getHours())}${p(d.getMinutes())}${p(d.getSeconds())}`;
};

/** A pasted screenshot arrives as "image.png"; give it a name worth reading later. */
export function nameOf(file: File): string {
  const name = file.name || "";
  if (!name || /^image\.(png|jpe?g|gif|webp)$/i.test(name)) {
    const ext = (file.type.split("/")[1] || "bin").replace("jpeg", "jpg").replace(/[^a-z0-9]/gi, "");
    return `pasted-${stamp()}${seq ? `-${seq}` : ""}.${ext}`;
  }
  return name;
}

export function newAttachment(file: File): Attachment {
  return reactive<Attachment>({
    id: ++seq,
    file,
    name: nameOf(file),
    size: file.size,
    preview: file.type.startsWith("image/") ? URL.createObjectURL(file) : undefined,
    progress: 0,
    state: "uploading",
  });
}

/** Uploads (or re-uploads) one attachment, updating it as it goes. Resolves either way. */
export async function startUpload(a: Attachment, target: UploadTarget): Promise<boolean> {
  a.state = "uploading";
  a.error = undefined;
  a.progress = 0;
  if (a.size > MAX_UPLOAD) {
    a.state = "failed";
    a.error = "Over the 50 MB limit";
    return false;
  }
  const { done, abort } = uploadFile(target, a.file, a.name, (f) => (a.progress = f));
  a.abort = abort;
  try {
    const r = await done;
    a.path = r.path;
    a.progress = 1;
    a.state = "done";
    return true;
  } catch (e) {
    a.state = "failed";
    a.error = errorText(e);
    return false;
  } finally {
    a.abort = undefined;
  }
}

export function dropAttachment(a: Attachment) {
  a.abort?.();
  if (a.preview) URL.revokeObjectURL(a.preview);
}

/** The files in a paste or a drop (an image copied from a page comes as a file item). */
export function filesOf(data: DataTransfer | null | undefined): File[] {
  if (!data) return [];
  if (data.files?.length) return [...data.files];
  const out: File[] = [];
  for (const item of data.items ?? []) {
    if (item.kind !== "file") continue;
    const f = item.getAsFile();
    if (f) out.push(f);
  }
  return out;
}

/** True while something with files is dragged over the page. */
export const draggingFiles = (data: DataTransfer | null | undefined) => !!data?.types?.includes("Files");

export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(n < 10 * 1024 ? 1 : 0)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

/** A path as one word for a shell or a TUI prompt: quoted only when it has to be. */
export function quotePath(path: string): string {
  return /^[\w@%+=:,./-]+$/.test(path) ? path : `'${path.replace(/'/g, `'\\''`)}'`;
}
