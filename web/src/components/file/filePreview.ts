import type { FileItem } from "@/api/types";

export type FilePreviewKind =
  | "video"
  | "audio"
  | "image"
  | "text"
  | "pdf"
  | "docx"
  | "spreadsheet"
  | "archive"
  | "pptx";

export interface ActiveFilePreview {
  kind: FilePreviewKind;
  file: FileItem;
}
