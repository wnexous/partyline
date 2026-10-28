/*
 * The real timeline and length of a WebM that MediaRecorder wrote (VOI-08).
 *
 * A player learns how long a WebM is from the Duration in its Info, and where its blocks are from
 * each Cluster's Timecode. MediaRecorder gets both wrong on a real phone: on desktop Chrome it
 * leaves Duration out, and on Android Chrome a page that records a second time gives the recording
 * a false gap of minutes between its first two blocks (the time since the page loaded), while the
 * Duration repeats that wrong timeline. A player then shows minutes of silence and a wrong length.
 *
 * This walks the container by hand, as the container describes it: the EBML header, the Segment
 * (known or unknown size), the SeekHead, Void and Info in it, and its Clusters with their Timecode
 * and SimpleBlock or BlockGroup blocks. It then
 *
 *  - closes a false gap of more than a second between two blocks to 20 ms, by rewriting each later
 *    Cluster's Timecode (or, for a gap inside one Cluster, the blocks' relative times) in place;
 *  - writes the length the recorder measured into Info's Duration, in the float width it already
 *    has, or inserts an eight-byte one where the Segment carries none.
 *
 * Every edit keeps every element size, so a SeekHead that points at elements stays right; inserting
 * a Duration into a known-size Segment grows Info and the Segment and moves the SeekHead's
 * positions with them. Anything that is not such a file, or that is cut short, comes back untouched,
 * so a recording is never lost to this.
 */

const EBML_HEADER_ID = 0x1a45dfa3;
const SEGMENT_ID = 0x18538067;
const SEEK_HEAD_ID = 0x114d9b74;
const SEEK_ID = 0x4dbb;
const SEEK_POSITION_ID = 0x53ac;
const INFO_ID = 0x1549a966;
const TIMECODE_SCALE_ID = 0x2ad7b1;
const DURATION_ID = 0x4489;
const CLUSTER_ID = 0x1f43b675;
const TIMECODE_ID = 0xe7;
const SIMPLE_BLOCK_ID = 0xa3;
const BLOCK_GROUP_ID = 0xa0;
const BLOCK_ID = 0xa1;

/** The IDs that end an unknown-size Cluster: the elements a Segment may hold. */
const TOP_LEVEL_IDS = new Set([
  EBML_HEADER_ID,
  SEGMENT_ID,
  SEEK_HEAD_ID,
  0x1254c367, // Tags
  0x1043a770, // Chapters
  0x1654ae6b, // Tracks
  0x1941a469, // Attachments
  0x1c53bb6b, // Cues
  INFO_ID,
  CLUSTER_ID,
]);

/** Bytes backed by an ArrayBuffer of their own, which a File or a Blob takes. */
type Bytes = Uint8Array<ArrayBuffer>;

/** MediaRecorder's TimecodeScale: a tick is a millisecond (VOI-08). */
const DEFAULT_TIMECODE_SCALE = 1_000_000;

/** A block that starts this long after the one before it is a false gap, not a pause. */
const FALSE_GAP_MS = 1_120;

/** A false gap closes to this: one audio block after the one before it. */
const CLOSED_GAP_MS = 20;

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
  return { value: value === unknown ? null : value, width, next: offset + width };
}

/** The element at `offset`, whose data ends at `end` at the latest. */
function readElement(bytes: Bytes, offset: number, end: number): Element | null {
  const id = readId(bytes, offset);
  if (!id || id.next > end) return null;
  const size = readSize(bytes, id.next);
  if (!size || size.next > end) return null;
  const dataStart = size.next;
  if (size.value === null) {
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

/** `value` as an unsigned big-endian integer of exactly `width` bytes, or null when it does not fit. */
function writeUnsigned(value: number, width: number): Bytes | null {
  if (!Number.isInteger(value) || value < 0 || width < 1 || width > 8) return null;
  const field = new Uint8Array(width);
  let rest = value;
  for (let i = width - 1; i >= 0; i--) {
    field[i] = rest % 256;
    rest = Math.floor(rest / 256);
  }
  return rest === 0 ? field : null;
}

/** `value` as the signed two-byte timecode a block carries, or null when it does not fit. */
function writeSigned(value: number): Bytes | null {
  if (!Number.isInteger(value) || value < -0x8000 || value > 0x7fff) return null;
  const raw = value < 0 ? value + 0x10000 : value;
  return new Uint8Array([Math.floor(raw / 256), raw % 256]);
}

/** `value` as the float of `width` bytes a Duration carries. */
function writeFloat(value: number, width: 4 | 8): Bytes {
  const field = new Uint8Array(width);
  const view = new DataView(field.buffer);
  if (width === 4) view.setFloat32(0, value, false);
  else view.setFloat64(0, value, false);
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

/** The same bytes with every edit written where it stands, every edit keeping its range's length. */
function patch(bytes: Bytes, edits: Edit[]): Bytes {
  if (edits.length === 0) return bytes;
  const patched = bytes.slice();
  for (const edit of edits) patched.set(edit.bytes, edit.start);
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

/** The Segment, which comes after the EBML header, sometimes after a Void or another header. */
function findSegment(bytes: Bytes): Element | null {
  const header = readElement(bytes, 0, bytes.length);
  if (!header || header.id !== EBML_HEADER_ID || header.dataLength === null) return null;
  let cursor = header.dataEnd;
  while (cursor < bytes.length) {
    const element = readElement(bytes, cursor, bytes.length);
    if (!element) return null;
    if (element.id === SEGMENT_ID) return element;
    // Only the Segment may run to the end of the file; anything else is not walked past.
    if (element.dataLength === null) return null;
    cursor = element.dataEnd;
  }
  return null;
}

/**
 * Where an unknown-size Cluster ends: the next top-level element at a child boundary, or the end of
 * the Segment. Null when the Cluster's children cannot be walked far enough to tell.
 */
function unknownClusterEnd(bytes: Bytes, cluster: Element, end: number): number | null {
  let cursor = cluster.dataStart;
  while (cursor < end) {
    const child = readElement(bytes, cursor, end);
    if (!child) return null;
    if (TOP_LEVEL_IDS.has(child.id)) return cursor;
    // An unknown size inside the Cluster cannot be walked past, so it runs to the Segment's end.
    if (child.dataLength === null) return end;
    cursor = child.dataEnd;
  }
  return end;
}

/** The Segment's children, with each unknown-size Cluster given the end the container implies. */
function segmentChildren(bytes: Bytes, segment: Element): Element[] | null {
  const end = segment.dataLength === null ? bytes.length : segment.dataEnd;
  const found: Element[] = [];
  let cursor = segment.dataStart;
  while (cursor < end) {
    const child = readElement(bytes, cursor, end);
    if (!child) return null;
    if (child.dataLength === null) {
      if (child.id === CLUSTER_ID) {
        const stop = unknownClusterEnd(bytes, child, end);
        if (stop === null) return null;
        found.push({ ...child, dataLength: stop - child.dataStart, dataEnd: stop });
        cursor = stop;
        continue;
      }
      // Another unknown-size element runs to the end of the Segment; nothing after it can be read.
      found.push(child);
      return found;
    }
    found.push(child);
    cursor = child.dataEnd;
  }
  return found;
}

/** The Info's length fields: the TimecodeScale in force, and the Duration when it has one. */
interface Info {
  element: Element;
  timecodeScale: number;
  duration: Element | null;
}

function readInfo(bytes: Bytes, element: Element): Info | null {
  if (element.dataLength === null) return null;
  const end = element.dataEnd;
  let timecodeScale = DEFAULT_TIMECODE_SCALE;
  let duration: Element | null = null;
  let cursor = element.dataStart;
  while (cursor < end) {
    const child = readElement(bytes, cursor, end);
    if (!child || child.dataLength === null) return null;
    if (child.id === TIMECODE_SCALE_ID) {
      const scale = readUnsigned(bytes, child);
      if (scale === null || scale <= 0) return null;
      timecodeScale = scale;
    } else if (child.id === DURATION_ID) {
      if (child.dataLength !== 4 && child.dataLength !== 8) return null;
      duration = child;
    }
    cursor = child.dataEnd;
  }
  return { element, timecodeScale, duration };
}

/** One block, with the signed relative time it carries and where that field sits. */
interface BlockTime {
  relStart: number;
  relTicks: number;
}

/** One Cluster: its Timecode and its blocks, in the order the container lists them. */
interface Cluster {
  timecode: Element | null;
  timecodeTicks: number | null;
  blocks: BlockTime[];
}

/** The relative time and where it sits in a SimpleBlock or a Block, or null when it is not one. */
function readBlockTime(bytes: Bytes, block: Element): BlockTime | null {
  if (block.dataLength === null || block.dataLength < 4) return null;
  const first = bytes[block.dataStart] as number;
  let trackWidth = 0;
  for (let bit = 0; bit < 8; bit++) {
    if (first & (0x80 >> bit)) {
      trackWidth = bit + 1;
      break;
    }
  }
  if (trackWidth === 0 || trackWidth > 8) return null;
  if (block.dataStart + trackWidth + 2 > block.dataEnd) return null;
  const high = bytes[block.dataStart + trackWidth] as number;
  const low = bytes[block.dataStart + trackWidth + 1] as number;
  let relTicks = high * 256 + low;
  if (relTicks >= 0x8000) relTicks -= 0x10000;
  return { relStart: block.dataStart + trackWidth, relTicks };
}

/** The Block inside a BlockGroup, or null when the group has none this can read. */
function blockOfGroup(bytes: Bytes, group: Element): BlockTime | null {
  if (group.dataLength === null) return null;
  let cursor = group.dataStart;
  while (cursor < group.dataEnd) {
    const child = readElement(bytes, cursor, group.dataEnd);
    if (!child || child.dataLength === null) return null;
    if (child.id === BLOCK_ID) return readBlockTime(bytes, child);
    cursor = child.dataEnd;
  }
  return null;
}

function readCluster(bytes: Bytes, element: Element): Cluster {
  const cluster: Cluster = { timecode: null, timecodeTicks: null, blocks: [] };
  if (element.dataLength === null) return cluster;
  let cursor = element.dataStart;
  while (cursor < element.dataEnd) {
    const child = readElement(bytes, cursor, element.dataEnd);
    if (!child || child.dataLength === null) break;
    if (child.id === TIMECODE_ID) {
      const ticks = readUnsigned(bytes, child);
      if (ticks !== null) {
        cluster.timecode = child;
        cluster.timecodeTicks = ticks;
      }
    } else if (child.id === SIMPLE_BLOCK_ID) {
      const block = readBlockTime(bytes, child);
      if (block) cluster.blocks.push(block);
    } else if (child.id === BLOCK_GROUP_ID) {
      const block = blockOfGroup(bytes, child);
      if (block) cluster.blocks.push(block);
    }
    cursor = child.dataEnd;
  }
  return cluster;
}

/** Where a Cluster's Timecode and blocks go to drop false gaps: all values are in the file's ticks. */
interface GapPlan {
  timecodes: { element: Element; value: number }[];
  relatives: { start: number; value: number }[];
}

/**
 * The false gaps this file has, and where to write the shift that closes each: a gap between two
 * Clusters moves every later Cluster's Timecode, a gap inside one Cluster moves its later blocks'
 * relative times. Null when the clusters cannot be placed well enough to plan against.
 */
function planGaps(clusters: Cluster[], scale: number): GapPlan | null {
  const plan: GapPlan = { timecodes: [], relatives: [] };
  const gapTicks = Math.round((FALSE_GAP_MS * 1_000_000) / scale);
  const followTicks = Math.round((CLOSED_GAP_MS * 1_000_000) / scale);
  /** How far everything after the gaps found so far has moved, in ticks. */
  let carry = 0;
  /** Where the previous block, after the shifts planned so far, starts. */
  let previous: number | null = null;

  for (const cluster of clusters) {
    const timecode = cluster.timecode;
    const ticks = cluster.timecodeTicks;
    if (ticks === null || timecode === null) {
      // A Cluster that cannot be placed is left alone; one with blocks would put them nowhere.
      if (cluster.blocks.length > 0) return null;
      continue;
    }
    const before = carry;
    let clusterShift = 0;
    let local = 0;
    let localFailed = false;

    for (let i = 0; i < cluster.blocks.length; i++) {
      const block = cluster.blocks[i] as BlockTime;
      const start = ticks + before + clusterShift + local + block.relTicks;
      if (previous !== null && start - previous > gapTicks) {
        const delta: number = previous + followTicks - start;
        if (i === 0) {
          // The gap is between Clusters: this Cluster and everything after it move back.
          clusterShift += delta;
          previous = start + delta;
          continue;
        }
        if (!localFailed) {
          // The gap is inside this Cluster: shift this block's and the later ones' relative times.
          const writes: { start: number; value: number }[] = [];
          let fits = true;
          for (let j = i; j < cluster.blocks.length; j++) {
            const later = cluster.blocks[j] as BlockTime;
            const value = later.relTicks + local + delta;
            if (value < -0x8000 || value > 0x7fff) {
              fits = false;
              break;
            }
            writes.push({ start: later.relStart, value });
          }
          if (fits) {
            for (const write of writes) plan.relatives.push(write);
            local += delta;
            previous = start + delta;
            continue;
          }
          // A relative time that does not fit: this Cluster is left as it is.
          localFailed = true;
        }
      }
      previous = start;
    }

    // Every Cluster after a gap carries the shift, whether it has a block of its own or not.
    carry = before + clusterShift + local;
    const shift = before + clusterShift;
    if (shift !== 0) {
      const value = ticks + shift;
      if (!writeUnsigned(value, timecode.dataLength ?? 0)) return null;
      plan.timecodes.push({ element: timecode, value });
    }
  }
  return plan;
}

/** A SeekPosition in a SeekHead, with where its value sits. */
interface Position {
  element: Element;
  value: number;
}

/** Every SeekPosition a SeekHead holds, or null when its entries cannot be walked. */
function seekPositions(bytes: Bytes, seekHead: Element): Position[] | null {
  if (seekHead.dataLength === null) return null;
  const found: Position[] = [];
  let cursor = seekHead.dataStart;
  while (cursor < seekHead.dataEnd) {
    const seek = readElement(bytes, cursor, seekHead.dataEnd);
    if (!seek || seek.dataLength === null) return null;
    if (seek.id === SEEK_ID) {
      let inner = seek.dataStart;
      while (inner < seek.dataEnd) {
        const child = readElement(bytes, inner, seek.dataEnd);
        if (!child || child.dataLength === null) return null;
        if (child.id === SEEK_POSITION_ID) {
          const value = readUnsigned(bytes, child);
          if (value === null) return null;
          found.push({ element: child, value });
        }
        inner = child.dataEnd;
      }
    }
    cursor = seek.dataEnd;
  }
  return found;
}

/** An eight-byte float Duration for `ticks`, with its two-byte ID and one-byte size. */
function durationElement(ticks: number): Bytes {
  const element = new Uint8Array(11);
  element.set([0x44, 0x89, 0x88]);
  new DataView(element.buffer).setFloat64(3, ticks, false);
  return element;
}

/**
 * The bytes with a Duration put first in Info: Info grows and so does a Segment that knows its size,
 * and the SeekPositions that point past the insertion move with it. Null when a size or a position
 * has no width left for the new value, in which case the file is left as it is.
 */
function insertDuration(
  bytes: Bytes,
  segment: Element,
  info: Info,
  seekHeads: Element[],
  element: Bytes,
): Bytes | null {
  const dataStart = info.element.dataStart;
  const dataEnd = info.element.dataEnd;
  const infoData = join([element, bytes.subarray(dataStart, dataEnd)]);
  const sizeField = writeSize(infoData.length, info.element.sizeWidth);
  if (!sizeField) return null;
  const growth = sizeField.length + infoData.length - (dataEnd - info.element.sizeStart);
  if (growth <= 0) return null;

  const edits: Edit[] = [
    {
      start: info.element.sizeStart,
      end: dataEnd,
      bytes: join([sizeField, infoData]),
    },
  ];
  // A Segment that says how long it is grows with the Info it holds; MediaRecorder's stays unknown.
  if (segment.dataLength !== null) {
    const segmentSize = writeSize(segment.dataLength + growth, segment.sizeWidth);
    if (!segmentSize) return null;
    edits.push({ start: segment.sizeStart, end: segment.dataStart, bytes: segmentSize });
  }
  // Everything that starts past the new Duration moves by the growth; the SeekHead keeps up.
  const insertAt = dataStart - segment.dataStart;
  for (const seekHead of seekHeads) {
    const positions = seekPositions(bytes, seekHead);
    if (!positions) return null;
    for (const position of positions) {
      if (position.value < insertAt) continue;
      const moved = writeUnsigned(position.value + growth, position.element.dataLength ?? 0);
      if (!moved) return null;
      edits.push({
        start: position.element.dataStart,
        end: position.element.dataEnd,
        bytes: moved,
      });
    }
  }
  return applyEdits(bytes, edits);
}

function normalize(bytes: Bytes, durationMs: number): Bytes {
  const segment = findSegment(bytes);
  if (!segment) return bytes;
  const children = segmentChildren(bytes, segment);
  if (!children) return bytes;

  let info: Info | null = null;
  const clusters: Cluster[] = [];
  const seekHeads: Element[] = [];
  for (const child of children) {
    if (child.id === INFO_ID && info === null) {
      info = readInfo(bytes, child);
      if (!info) return bytes;
    } else if (child.id === CLUSTER_ID) {
      clusters.push(readCluster(bytes, child));
    } else if (child.id === SEEK_HEAD_ID) {
      seekHeads.push(child);
    }
  }
  if (!info) return bytes;

  const ticks = (durationMs * 1_000_000) / info.timecodeScale;
  if (!Number.isFinite(ticks)) return bytes;

  const edits: Edit[] = [];
  let insert: Bytes | null = null;
  if (info.duration) {
    const width = info.duration.dataLength;
    if (width !== 4 && width !== 8) return bytes;
    const field = writeFloat(ticks, width);
    let same = true;
    for (let i = 0; i < field.length; i++) {
      if (bytes[info.duration.dataStart + i] !== field[i]) {
        same = false;
        break;
      }
    }
    if (!same) {
      edits.push({ start: info.duration.dataStart, end: info.duration.dataEnd, bytes: field });
    }
  } else {
    insert = durationElement(ticks);
  }

  const plan = planGaps(clusters, info.timecodeScale);
  if (plan) {
    for (const write of plan.timecodes) {
      const field = writeUnsigned(write.value, write.element.dataLength ?? 0);
      if (!field) return patch(bytes, edits);
      edits.push({ start: write.element.dataStart, end: write.element.dataEnd, bytes: field });
    }
    for (const write of plan.relatives) {
      const field = writeSigned(write.value);
      if (!field) return patch(bytes, edits);
      edits.push({ start: write.start, end: write.start + 2, bytes: field });
    }
  }

  const patched = patch(bytes, edits);
  if (!insert) return patched;
  return insertDuration(patched, segment, info, seekHeads, insert) ?? patched;
}

/**
 * The same WebM with its false timeline gaps closed and its length written into the Info element's
 * Duration, counted in the file's own TimecodeScale; `durationMs` is how long the recording ran.
 * Existing elements keep their sizes, and a Duration that is already there keeps its float width.
 * Bytes that are not a WebM this can read come back as they were.
 */
export function normalizeWebm(bytes: Bytes, durationMs: number): Bytes {
  if (!Number.isFinite(durationMs) || durationMs < 0) return bytes;
  try {
    return normalize(bytes, durationMs);
  } catch {
    return bytes;
  }
}
