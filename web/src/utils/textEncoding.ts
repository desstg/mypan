import { detect } from "chardet";

export interface DecodedText {
  text: string;
  encoding: string;
}

export const TEXT_ENCODINGS = [
  { value: "auto", label: "自动检测" },
  { value: "utf-8", label: "UTF-8" },
  { value: "gb18030", label: "GB18030 / GBK" },
  { value: "big5", label: "Big5" },
  { value: "utf-16le", label: "UTF-16 LE" },
  { value: "utf-16be", label: "UTF-16 BE" },
  { value: "shift_jis", label: "Shift-JIS" },
  { value: "euc-jp", label: "EUC-JP" },
  { value: "euc-kr", label: "EUC-KR" },
  { value: "windows-1252", label: "Windows-1252" },
] as const;

const decoderAliases: Record<string, string> = {
  ascii: "utf-8",
  utf8: "utf-8",
  "utf-8": "utf-8",
  utf16le: "utf-16le",
  "utf-16le": "utf-16le",
  utf16be: "utf-16be",
  "utf-16be": "utf-16be",
  gb18030: "gb18030",
  gbk: "gb18030",
  big5: "big5",
  shift_jis: "shift_jis",
  shiftjis: "shift_jis",
  "euc-jp": "euc-jp",
  euc_jp: "euc-jp",
  "euc-kr": "euc-kr",
  euc_kr: "euc-kr",
};

function normalizeEncoding(value: string | null | undefined) {
  const key = String(value || "utf-8").trim().toLowerCase();
  return decoderAliases[key] || key;
}

function bomEncoding(bytes: Uint8Array): { encoding: string; offset: number } | null {
  if (bytes.length >= 3 && bytes[0] === 0xef && bytes[1] === 0xbb && bytes[2] === 0xbf) {
    return { encoding: "utf-8", offset: 3 };
  }
  if (bytes.length >= 2 && bytes[0] === 0xff && bytes[1] === 0xfe) {
    return { encoding: "utf-16le", offset: 2 };
  }
  if (bytes.length >= 2 && bytes[0] === 0xfe && bytes[1] === 0xff) {
    return { encoding: "utf-16be", offset: 2 };
  }
  return null;
}

function decode(bytes: Uint8Array, encoding: string, mayEndMidCharacter: boolean) {
  const maxTrim = mayEndMidCharacter && encoding === "utf-8" ? Math.min(3, bytes.length) : 0;
  for (let trim = 0; trim <= maxTrim; trim += 1) {
    try {
      return new TextDecoder(encoding, { fatal: encoding === "utf-8" }).decode(
        trim ? bytes.subarray(0, bytes.length - trim) : bytes,
      );
    } catch {
      // 截断读取可能正好停在 UTF-8 多字节字符中间，逐字节回退重试。
    }
  }
  return new TextDecoder(encoding).decode(bytes);
}

function isValidUTF8(bytes: Uint8Array, mayEndMidCharacter: boolean) {
  const maxTrim = mayEndMidCharacter ? Math.min(3, bytes.length) : 0;
  for (let trim = 0; trim <= maxTrim; trim += 1) {
    try {
      new TextDecoder("utf-8", { fatal: true }).decode(trim ? bytes.subarray(0, bytes.length - trim) : bytes);
      return true;
    } catch {
      // 继续尝试回退截断末尾。
    }
  }
  return false;
}

export function decodeTextBytes(
  bytes: Uint8Array,
  selectedEncoding = "auto",
  mayEndMidCharacter = false,
): DecodedText {
  const bom = bomEncoding(bytes);
  const source = bom ? bytes.subarray(bom.offset) : bytes;
  let encoding = selectedEncoding === "auto" ? bom?.encoding : selectedEncoding;

  if (!encoding) {
    encoding = isValidUTF8(source, mayEndMidCharacter)
      ? "utf-8"
      : normalizeEncoding(detect(source)) || "gb18030";
  }

  encoding = normalizeEncoding(encoding);
  try {
    return { text: decode(source, encoding, mayEndMidCharacter).replace(/^\uFEFF/, ""), encoding };
  } catch {
    return { text: decode(source, "utf-8", mayEndMidCharacter).replace(/^\uFEFF/, ""), encoding: "utf-8" };
  }
}

export function isProbablyText(bytes: Uint8Array) {
  if (bytes.length === 0) return true;
  if (bomEncoding(bytes)) return true;
  let nul = 0;
  let controls = 0;
  for (const byte of bytes) {
    if (byte === 0) nul += 1;
    else if (byte < 7 || (byte > 13 && byte < 32)) controls += 1;
  }
  if (nul > Math.max(1, bytes.length * 0.005)) return false;
  return controls / bytes.length < 0.03;
}
