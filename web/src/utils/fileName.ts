const INVALID_NAME = /[<>:"/\\|?\x00-\x1F]/;

export class FileNameError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "FileNameError";
  }
}

export function validateFileName(name: string): string {
  const trimmed = name.trim();
  if (!trimmed) throw new FileNameError("请输入名称");
  if (trimmed.length > 255) throw new FileNameError("名称不能超过255个字符");
  if (trimmed === "." || trimmed === "..") throw new FileNameError("名称不能为 . 或 ..");
  if (INVALID_NAME.test(trimmed)) {
    throw new FileNameError('名称不能包含 " \\ / : * ? | > < 等特殊字符');
  }
  return trimmed;
}

export function renameSelectionEnd(fileName: string, isDir: boolean): number {
  if (isDir) return fileName.length;
  const dot = fileName.lastIndexOf(".");
  if (dot <= 0) return fileName.length;
  return dot;
}
