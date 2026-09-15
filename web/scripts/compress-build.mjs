import { constants as zlibConstants, gzip } from "node:zlib";
import { promisify } from "node:util";
import { readdir, readFile, stat, unlink, writeFile } from "node:fs/promises";
import { extname, join } from "node:path";
import { fileURLToPath } from "node:url";

const gzipAsync = promisify(gzip);
const outputDir = fileURLToPath(new URL("../../internal/api/web/", import.meta.url));
const compressibleExtensions = new Set([".css", ".js", ".mjs", ".svg", ".ttf", ".wasm"]);

async function collectFiles(directory) {
  const entries = await readdir(directory, { withFileTypes: true });
  const files = [];
  for (const entry of entries) {
    const file = join(directory, entry.name);
    if (entry.isDirectory()) files.push(...await collectFiles(file));
    else files.push(file);
  }
  return files;
}

let originalBytes = 0;
let embeddedBytes = 0;
let compressedFiles = 0;

for (const file of await collectFiles(outputDir)) {
  if (file.endsWith(".DS_Store")) {
    await unlink(file);
    continue;
  }

  const info = await stat(file);
  originalBytes += info.size;
  if (!compressibleExtensions.has(extname(file))) {
    embeddedBytes += info.size;
    continue;
  }

  const source = await readFile(file);
  const compressed = await gzipAsync(source, {
    level: zlibConstants.Z_BEST_COMPRESSION,
    mtime: 0,
  });
  if (compressed.length >= source.length) {
    embeddedBytes += source.length;
    continue;
  }

  await writeFile(`${file}.gz`, compressed);
  await unlink(file);
  compressedFiles += 1;
  embeddedBytes += compressed.length;
}

const saved = originalBytes - embeddedBytes;
console.log(`内嵌资源压缩完成：${compressedFiles} 个文件，减少 ${(saved / 1024 / 1024).toFixed(2)} MB`);
