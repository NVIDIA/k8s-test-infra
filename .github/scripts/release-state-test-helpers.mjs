import { gzipSync } from "node:zlib";

function writeString(buffer, offset, length, value) {
  buffer.write(value, offset, Math.min(length, Buffer.byteLength(value)), "utf8");
}

function writeOctal(buffer, offset, length, value) {
  const text = value.toString(8).padStart(length - 1, "0") + "\0";
  writeString(buffer, offset, length, text);
}

export function chartArchive(entries) {
  const blocks = [];
  for (const entry of entries) {
    const content = Buffer.from(entry.content ?? "");
    const header = Buffer.alloc(512);
    writeString(header, 0, 100, entry.name);
    writeOctal(header, 100, 8, entry.mode ?? (entry.type === "directory" ? 0o755 : 0o644));
    writeOctal(header, 108, 8, 0);
    writeOctal(header, 116, 8, 0);
    writeOctal(header, 124, 12, entry.type === "file" || entry.type === undefined ? content.length : 0);
    writeOctal(header, 136, 12, entry.mtime ?? 0);
    header.fill(0x20, 148, 156);
    const typeFlags = { file: "0", directory: "5", symlink: "2", character: "3", block: "4", fifo: "6" };
    writeString(header, 156, 1, typeFlags[entry.type ?? "file"]);
    writeString(header, 157, 100, entry.linkname ?? "");
    writeString(header, 257, 6, "ustar\0");
    writeString(header, 263, 2, "00");
    const checksum = [...header].reduce((sum, byte) => sum + byte, 0);
    writeString(header, 148, 8, checksum.toString(8).padStart(6, "0") + "\0 ");
    blocks.push(header);
    if (entry.type === "file" || entry.type === undefined) {
      blocks.push(content);
      blocks.push(Buffer.alloc((512 - (content.length % 512)) % 512));
    }
  }
  blocks.push(Buffer.alloc(1024));
  return gzipSync(Buffer.concat(blocks), { mtime: 0 });
}
