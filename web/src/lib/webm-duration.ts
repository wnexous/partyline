/*
 * The real length of a WebM that MediaRecorder wrote (VOI-08). MediaRecorder leaves the Duration
 * element out of the Segment's Info, so a player reports Infinity until it has downloaded the whole
 * file, and a phone on a slow connection guesses wrong. The recorder knows how long it recorded:
 * this writes that length into the container, where every player finds it at once.
 *
 * It reads the EBML structure by hand, as the container describes it: the EBML header, the Segment
 * and the Info inside it, with its TimecodeScale and Duration. Anything that is not such a file,
 * or that is cut short, comes back untouched, so a recording is never lost to this. The Segment's
 * size, which MediaRecorder leaves unknown, stays unknown.
 */

const EBML_HEADER_ID = 0x1a45dfa3;
const SEGMENT_ID = 0x18538067;
const INFO_ID = 0x1549a766;
const TIMECODE_SCALE_ID = 0x2ad7b1;
const DURATION_ID = 0x4489;
const UNKNOWN_SIZE = null;

/** Bytes backed by an ArrayBuffer of their own, which a File or a Blob takes. */
type Bytes = Uint8Array<ArrayBuffer>;

/** MediaRecorder's TimecodeScale: a tick is a millisecond (VOI-08). */
const DEFAULT_TIMECODE_SCALE = 1_000_000;

/** One EBML element, located in the bytes it was read from. */
interface Element {
  id: number;
  /** Where the ID starts. */
  start: number;
  /** Where the size field starts, and how many bytes it takes. */
  sizeStart: number;
  sizeWidth: number;
  /** Where the data starts, and how long it is; unknown sizes run to the end of their parent. */
  dataStart: number;
  dataLength: number | null;
  /** Where the data ends. */
  dataEnd: number;
}

/** A stretch of the original bytes to leave out, and what takes its place. */
interface Edit {
  start: number;
  end: number;
  bytes: Bytes;
}

const hasBytes = (bytes: Bytes, offset: number, count: number) =>
  offset >= 0 && offset + count <= bytes.length;

/** The four-byte-or-shorter ID at `offset`, with where it ends. */
function readId(bytes: Bytes, offset: number): { id: number; next: number } | null {
  if (offset >= bytes.length) return null;
  const first = bytes[offset] as number;
  let width = 0;
  for (let bit = 0; bit < 4; bit++) {
    if (first & (0x80 >> bit)) {
      width = bit + 1;
      break;
    }
  }
  if (width === 0 || !hasBytes(bytes, offset, width)) return null;
  let id = 0;
  for (let i = 0; i < width; i++) id = id * 256 + (bytes[offset + i] as number);
  return { id, next: offset + width };
}

/**
 * The EBML variable-length size at `offset`: its value (null when every bit is set, an unknown
 * size), the width of the field, and where it ends.
 */
function readSize(
  bytes: Bytes,
  offset: number,
): { value: number | null; width: number; next: number } | null {
  if (offset >= bytes.length) return null;
  const first = bytes[offset] as number;
  let width = 0;
  for (let bit = 0; bit < 8; bit++) {
    if (first & (0x80 >> bit)) {
      width = bit + 1;
      break;
    }
  }
  if (width === 0 || !hasBytes(bytes, offset, width)) return null;
  const marker = 0x80 >> (width - 1);
  let value = first & (marker - 1);
  for (let i = 1; i < width; i++) value = value * 256 + (bytes[offset + i] as number);
  const unknown = 2 ** (7 * width) - 1;
  return { value: value === unknown ? UNKNOWN_SIZE : value, width, next: offset + width };
}

/** The element at `offset`, whose data ends at `end` at the latest. */
function readElement(bytes: Bytes, offset: number, end: number): Element | null {
  const id = readId(bytes, offset);
  if (!id || id.next > end) return null;
  const size = readSize(bytes, id.next);
  if (!size || size.next > end) return null;
  const dataStart = size.next;
  if (size.value === UNKNOWN_SIZE) {
    return {
      id: id.id,
      start: offset,
      sizeStart: id.next,
      sizeWidth: size.width,
      dataStart,
      dataLength: null,
      dataEnd: end,
    };
  }
  if (dataStart + size.value > end) return null;
  return {
    id: id.id,
    start: offset,
    sizeStart: id.next,
    sizeWidth: size.width,
    dataStart,
    dataLength: size.value,
    dataEnd: dataStart + size.value,
  };
}

/** The children of an element, as a reader of the container walks them. */
function children(bytes: Bytes, parent: Element, end: number): Element[] | null {
  const found: Element[] = [];
  let cursor = parent.dataStart;
  while (cursor < end) {
    const child = readElement(bytes, cursor, end);
    // An unknown size cannot be walked past, so what comes after it stays unread.
    if (!child || child.dataLength === null) return null;
    found.push(child);
    cursor = child.dataEnd;
  }
  return found;
}

/** A size field for `value`, in the narrowest width that holds it and is at least `minimum`. */
function writeSize(value: number, minimum: number): Bytes | null {
  if (!Number.isInteger(value) || value < 0) return null;
  let width = Math.min(Math.max(minimum, 1), 8);
  while (width < 8 && value > 2 ** (7 * width) - 2) width++;
  if (value > 2 ** (7 * width) - 2) return null;
  const field = new Uint8Array(width);
  let rest = value;
  for (let i = width - 1; i >= 0; i--) {
    field[i] = rest % 256;
    rest = Math.floor(rest / 256);
  }
  field[0] = (field[0] as number) | (0x80 >> (width - 1));
  return field;
}

/** The bytes of `parts`, one after another. */
function join(parts: Bytes[]): Bytes {
  const total = parts.reduce((sum, part) => sum + part.length, 0);
  const joined = new Uint8Array(total);
  let offset = 0;
  for (const part of parts) {
    joined.set(part, offset);
    offset += part.length;
  }
  return joined;
}

/** The original bytes with each edit in place of the range it stands for; edits are in order. */
function applyEdits(bytes: Bytes, edits: Edit[]): Bytes {
  const ordered = [...edits].sort((a, b) => a.start - b.start);
  const size = ordered.reduce((sum, edit) => sum + edit.bytes.length - (edit.end - edit.start), 0);
  const patched = new Uint8Array(bytes.length + size);
  let read = 0;
  let write = 0;
  for (const edit of ordered) {
    patched.set(bytes.subarray(read, edit.start), write);
    write += edit.start - read;
    patched.set(edit.bytes, write);
    write += edit.bytes.length;
    read = edit.end;
  }
  patched.set(bytes.subarray(read), write);
  return patched;
}

/** An unsigned big-endian integer of up to eight bytes, or null when it is not one. */
function readUnsigned(bytes: Bytes, element: Element): number | null {
  if (element.dataLength === null || element.dataLength < 1 || element.dataLength > 8) return null;
  let value = 0;
  for (let i = 0; i < element.dataLength; i++) {
    value = value * 256 + (bytes[element.dataStart + i] as number);
  }
  return value;
}

/**
 * The same WebM with its length written into the Info element's Duration, in the file's own
 * TimecodeScale; `durationMs` is how long the recording ran. A Duration that is already there is
 * overwritten in place. Bytes that are not a WebM this can read come back as they were.
 */
export function withWebmDuration(bytes: Bytes, durationMs: number): Bytes {
  if (!Number.isFinite(durationMs) || durationMs < 0) return bytes;

  const header = readElement(bytes, 0, bytes.length);
  if (!header || header.id !== EBML_HEADER_ID || header.dataLength === null) return bytes;

  // The Segment comes after the EBML header, sometimes after a Void or a second header element.
  let segment: Element | null = null;
  let cursor = header.dataEnd;
  while (cursor < bytes.length) {
    const element = readElement(bytes, cursor, bytes.length);
    if (!element) break;
    if (element.id === SEGMENT_ID) {
      segment = element;
      break;
    }
    // Only the Segment may run to the end of the file; anything else is not walked past.
    if (element.dataLength === null) break;
    cursor = element.dataEnd;
  }
  if (!segment) return bytes;

  const segmentEnd = segment.dataLength === null ? bytes.length : segment.dataEnd;
  let info: Element | null = null;
  cursor = segment.dataStart;
  while (cursor < segmentEnd) {
    const element = readElement(bytes, cursor, segmentEnd);
    if (!element || element.dataLength === null) return bytes;
    if (element.id === INFO_ID) {
      info = element;
      break;
    }
    cursor = element.dataEnd;
  }
  if (!info || info.dataLength === null) return bytes;

  const infoEnd = info.dataEnd;
  const infoChildren = children(bytes, info, infoEnd);
  if (!infoChildren) return bytes;

  let timecodeScale = DEFAULT_TIMECODE_SCALE;
  let duration: Element | null = null;
  for (const child of infoChildren) {
    if (child.id === TIMECODE_SCALE_ID) {
      const scale = readUnsigned(bytes, child);
      if (scale === null || scale <= 0) return bytes;
      timecodeScale = scale;
    } else if (child.id === DURATION_ID) {
      if (child.dataLength !== 4 && child.dataLength !== 8) return bytes;
      duration = child;
    }
  }

  const ticks = (durationMs * 1_000_000) / timecodeScale;
  if (!Number.isFinite(ticks)) return bytes;

  // Duration: a two-byte ID, its one-byte size (8) and the float, in the file's timecode ticks.
  const durationElement = new Uint8Array(11);
  durationElement.set([0x44, 0x89, 0x88]);
  new DataView(durationElement.buffer).setFloat64(3, ticks, false);

  const edits: Edit[] = [];
  if (duration && duration.dataLength === 8) {
    // The length is already the size of a Duration: it is written where the old one was.
    edits.push({
      start: duration.dataStart,
      end: duration.dataEnd,
      bytes: durationElement.subarray(3),
    });
  } else {
    // No Duration at all, or a four-byte one that cannot hold every length exactly: an eight-byte
    // Duration goes in, first in Info or where the old one was, and Info's size is rewritten.
    const infoData = duration
      ? join([
          bytes.subarray(info.dataStart, duration.start),
          durationElement,
          bytes.subarray(duration.dataEnd, infoEnd),
        ])
      : join([durationElement, bytes.subarray(info.dataStart, infoEnd)]);
    const sizeField = writeSize(infoData.length, info.sizeWidth);
    if (!sizeField) return bytes;
    edits.push({ start: info.sizeStart, end: info.dataEnd, bytes: join([sizeField, infoData]) });
    // A Segment that says how long it is grows with the Info it holds; MediaRecorder's stays unknown.
    if (segment.dataLength !== null) {
      const grown = infoData.length + sizeField.length - (infoEnd - info.sizeStart);
      const segmentSize = writeSize(segment.dataLength + grown, segment.sizeWidth);
      if (!segmentSize) return bytes;
      edits.push({ start: segment.sizeStart, end: segment.dataStart, bytes: segmentSize });
    }
  }

  return applyEdits(bytes, edits);
}
