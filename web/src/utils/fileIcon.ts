import type { FileItem } from "@/api/types";

type FileKind =
  | "folder"
  | "video"
  | "audio"
  | "image"
  | "archive"
  | "pdf"
  | "doc"
  | "sheet"
  | "slide"
  | "code"
  | "text"
  | "file";

const EXT_MAP: Record<string, FileKind> = {
  mp4: "video", mkv: "video", avi: "video", mov: "video", flv: "video", wmv: "video", webm: "video", ts: "video", m3u8: "video", rmvb: "video",
  mp3: "audio", flac: "audio", wav: "audio", aac: "audio", ogg: "audio", oga: "audio", opus: "audio", m4a: "audio", ape: "audio", wma: "audio",
  jpg: "image", jpeg: "image", png: "image", gif: "image", webp: "image", bmp: "image", svg: "image", heic: "image", heif: "image", avif: "image",
  zip: "archive", cbz: "archive", rar: "archive", "7z": "archive", tar: "archive", gz: "archive", bz2: "archive", tgz: "archive",
  pdf: "pdf",
  doc: "doc", docx: "doc",
  xls: "sheet", xlsx: "sheet", csv: "sheet", ods: "sheet",
  ppt: "slide", pptx: "slide",
  js: "code", jsx: "code", tsx: "code", go: "code", py: "code", java: "code", rs: "code", c: "code", cpp: "code", h: "code", hpp: "code", php: "code", rb: "code", swift: "code", kt: "code", sh: "code", sql: "code", json: "code", html: "code", css: "code", vue: "code",
  txt: "text", rtf: "text", md: "text", markdown: "text", log: "text", strm: "text", nfo: "text", srt: "text", ass: "text", lrc: "text", xml: "text", yml: "text", yaml: "text", toml: "text", ini: "text", conf: "text", env: "text",
};

export function fileKind(file: FileItem): FileKind {
  if (file.is_dir) return "folder";
  const dot = file.name.lastIndexOf(".");
  if (dot < 0) return "file";
  const ext = file.name.slice(dot + 1).toLowerCase();
  return EXT_MAP[ext] ?? "file";
}
