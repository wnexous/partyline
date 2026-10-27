import { describe, expect, it } from "vitest";
import { withWebmDuration } from "@/lib/webm-duration";

/*
 * The real length of a WebM (VOI-08). The tests build the containers byte by byte, as
 * MediaRecorder writes them (an EBML header, a Segment of unknown size, an Info with no Duration
 * and a Cluster), and read the result back with their own minimal EBML reader, so what is asserted
 * is the file a player sees and not how the writer works.
 */

/** The EBML IDs the tests use, with the widths they are written in. */
const ID = {
  header: [0x1a, 0x45, 0xdf, 0xa3],
  doctype: [0x42, 0x82],
  segment: [0x18, 0x53, 0x80, 0x67],
  info: [0x15, 0x49, 0xa7, 0x66],
  timecodeScale: [0x2a, 0xd7, 0xb1],
  muxingApp: [0x4d, 0x80],
  duration: [0x44, 0x89],
  cluster: [0x1f, 0x43, 0xb6, 0x75],
} as const;

/** Bytes backed by an ArrayBuffer of their own. */
type Bytes = Uint8Array<ArrayBuffer>;

const DURATION_ID = 0x4489;
const INFO_ID = 0x1549a766;
const SEGMENT_ID = 0x18538067;

/** A Segment whose size is unknown: every bit of the eight-byte field is set, as Chrome writes it. */
const UNKNOWN_SEGMENT_SIZE = [0x01, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff];

const text = (value: string) => new TextEncoder().encode(value);

function concat(parts: Bytes[]): Bytes {
  const total = parts.reduce((sum, part) => sum + part.length, 0);
  const joined = new Uint8Array(total);
  let offset = 0;
  for (const part of parts) {
    joined.set(part, offset);
    offset += part.length;
  }
  return joined;
}

/** The EBML variable-length size of `value`, in `width` bytes or the narrowest that holds it. */
function sizeField(value: number, width = 0): Bytes {
  let w = width;
  if (w === 0) {
    w = 1;
    while (value > 2 ** (7 * w) - 2) w++;
  }
  const field = new Uint8Array(w);
  let rest = value;
  for (let i = w - 1; i >= 0; i--) {
    field[i] = rest % 256;
    rest = Math.floor(rest / 256);
  }
  field[0] = (field[0] as number) | (0x80 >> (w - 1));
  return field;
}

/** One element: its ID, the size of its data and the data. */
function element(id: readonly number[], data: Bytes, sizeWidth = 0): Bytes {
  return concat([new Uint8Array(id), sizeField(data.length, sizeWidth), data]);
}

/** An unsigned big-endian integer, in `width` bytes or the fewest that hold it. */
function uint(value: number, width = 0): Bytes {
  let w = width;
  if (w === 0) {
    w = 1;
    while (value >= 256 ** w) w++;
  }
  const bytes = new Uint8Array(w);
  let rest = value;
  for (let i = w - 1; i >= 0; i--) {
    bytes[i] = rest % 256;
    rest = Math.floor(rest / 256);
  }
  return bytes;
}

function float64(value: number): Bytes {
  const bytes = new Uint8Array(8);
  new DataView(bytes.buffer).setFloat64(0, value, false);
  return bytes;
}

const header = () =>
  element(ID.header, concat([element(ID.doctype, text("webm")), element([0x42, 0x86], uint(1))]));

const timecodeScale = (scale: number, width = 0) => element(ID.timecodeScale, uint(scale, width));

const muxingApp = (name: string) => element(ID.muxingApp, text(name));

const duration = (value: number) => element(ID.duration, float64(value));

const clusterStub = () => element(ID.cluster, new Uint8Array([0x00, 0x00, 0x00, 0x00]));

/** A recording as MediaRecorder hands it over: a Segment of unknown size, no Duration in Info. */
function webm(
  options: { scale?: number; scaleWidth?: number; withDuration?: number; sizeWidth?: number } = {},
): Bytes {
  const scale = options.scale ?? 1_000_000;
  const infoChildren = [timecodeScale(scale, options.scaleWidth), muxingApp("partyline-test")];
  if (options.withDuration !== undefined) infoChildren.push(duration(options.withDuration));
  return concat([
    header(),
    new Uint8Array(ID.segment),
    new Uint8Array(UNKNOWN_SEGMENT_SIZE),
    element(ID.info, concat(infoChildren), options.sizeWidth),
    clusterStub(),
  ]);
}

/** A place in the bytes: what a reader of the container knows of one element. */
interface Read {
  id: number;
  start: number;
  sizeStart: number;
  sizeWidth: number;
  dataStart: number;
  dataLength: number | null;
  end: number;
}

function readId(bytes: Bytes, offset: number): { id: number; next: number } {
  const first = bytes[offset] as number;
  let width = 0;
  for (let bit = 0; bit < 4; bit++) {
    if (first & (0x80 >> bit)) {
      width = bit + 1;
      break;
    }
  }
  if (width === 0) throw new Error(`no element ID at ${offset}`);
  let id = 0;
  for (let i = 0; i < width; i++) id = id * 256 + (bytes[offset + i] as number);
  return { id, next: offset + width };
}

function readElement(bytes: Bytes, offset: number, end: number): Read {
  const id = readId(bytes, offset);
  const first = bytes[id.next] as number;
  let width = 0;
  for (let bit = 0; bit < 8; bit++) {
    if (first & (0x80 >> bit)) {
      width = bit + 1;
      break;
    }
  }
  if (width === 0) throw new Error(`no size at ${id.next}`);
  const marker = 0x80 >> (width - 1);
  let value: number | null = first & (marker - 1);
  for (let i = 1; i < width; i++) value = value * 256 + (bytes[id.next + i] as number);
  if (value === 2 ** (7 * width) - 1) value = null;
  const dataStart = id.next + width;
  const dataLength = value;
  const dataEnd = dataLength === null ? end : dataStart + dataLength;
  if (dataEnd > end) throw new Error(`element ${id.id} runs past its parent`);
  return {
    id: id.id,
    start: offset,
    sizeStart: id.next,
    sizeWidth: width,
    dataStart,
    dataLength,
    end: dataEnd,
  };
}

function childrenOf(bytes: Bytes, parent: Read, end = parent.end): Read[] {
  const found: Read[] = [];
  let cursor = parent.dataStart;
  while (cursor < end) {
    const child = readElement(bytes, cursor, end);
    if (child.dataLength === null) throw new Error(`element ${child.id} has no size`);
    found.push(child);
    cursor = child.end;
  }
  return found;
}

function segmentOf(bytes: Bytes): Read {
  const top = readElement(bytes, 0, bytes.length);
  expect(top.id).toBe(0x1a45dfa3);
  const segment = readElement(bytes, top.end, bytes.length);
  expect(segment.id).toBe(SEGMENT_ID);
  return segment;
}

function infoOf(bytes: Bytes): Read {
  const info = childrenOf(bytes, segmentOf(bytes)).find((child) => child.id === INFO_ID);
  if (!info) throw new Error("no Info");
  return info;
}

function infoChildren(bytes: Bytes): Read[] {
  return childrenOf(bytes, infoOf(bytes));
}

function durationOf(bytes: Bytes): Read | undefined {
  return infoChildren(bytes).find((child) => child.id === DURATION_ID);
}

/** The Duration as the file carries it: ticks of the TimecodeScale, a float. */
function durationValue(bytes: Bytes): number {
  const element = durationOf(bytes);
  if (!element) throw new Error("no Duration");
  if (element.dataLength !== 8) throw new Error("the Duration is not an eight-byte float");
  return new DataView(bytes.buffer, bytes.byteOffset + element.dataStart, 8).getFloat64(0, false);
}

describe("withWebmDuration (VOI-08)", () => {
  it("writes the length of the recording into an Info that carries none", () => {
    const original = webm();
    const before = infoOf(original);
    expect(durationOf(original)).toBeUndefined();

    const patched = withWebmDuration(original, 4_000);
    const after = infoOf(patched);

    // A Duration is an eight-byte float, two bytes of ID and one of size: eleven bytes.
    expect(after.dataLength).toBe((before.dataLength as number) + 11);
    const written = readElement(patched, after.dataStart, after.end);
    expect(written.id).toBe(DURATION_ID);
    expect(written.dataLength).toBe(8);
    expect(durationValue(patched)).toBe(4_000);

    // It goes first, and the rest of Info's children keep their bytes.
    expect(Array.from(patched.subarray(after.dataStart + 11, after.end))).toEqual(
      Array.from(original.subarray(before.dataStart, before.end)),
    );
    // Everything after Info (the Cluster) is untouched.
    expect(Array.from(patched.subarray(after.end))).toEqual(
      Array.from(original.subarray(before.end)),
    );
    // The Segment still says it has no size: it was not made known or rewritten.
    expect(segmentOf(patched).dataLength).toBeNull();
  });

  it("overwrites a Duration that is already there, without making the file longer", () => {
    const original = webm({ withDuration: 0 });
    const before = durationOf(original) as Read;
    expect(durationValue(original)).toBe(0);

    const patched = withWebmDuration(original, 4_000);
    const after = durationOf(patched) as Read;

    expect(patched.length).toBe(original.length);
    expect(after.start).toBe(before.start);
    expect(after.dataLength).toBe(8);
    expect(durationValue(patched)).toBe(4_000);
    const expected = original.slice();
    new DataView(expected.buffer).setFloat64(before.dataStart, 4_000, false);
    expect(Array.from(patched)).toEqual(Array.from(expected));
  });

  it("counts the length in the TimecodeScale the file declares", () => {
    const patched = withWebmDuration(webm({ scale: 500_000 }), 4_000);
    expect(durationValue(patched)).toBe(8_000);
  });

  it("reads a TimecodeScale written in more bytes than it needs", () => {
    const patched = withWebmDuration(webm({ scale: 1_000_000, scaleWidth: 4 }), 4_000);
    expect(durationValue(patched)).toBe(4_000);
  });

  it.each<[string, Bytes]>([
    ["an MP4", new Uint8Array([0x00, 0x00, 0x00, 0x18, 0x66, 0x74, 0x79, 0x70])],
    ["text", text("not a webm at all")],
    ["nothing", new Uint8Array(0)],
    ["a random byte", new Uint8Array([0x1a])],
  ])("returns %s exactly as it came", (_name, bytes) => {
    expect(withWebmDuration(bytes, 4_000)).toBe(bytes);
  });

  it("returns a file that is cut short exactly as it came", () => {
    const original = webm();
    const info = infoOf(original);
    const cut = [
      original.subarray(0, 4), // the header's ID alone
      original.subarray(0, 8), // the header's ID and no size
      original.subarray(0, info.start + 2), // the Info's ID alone
      original.subarray(0, info.dataStart + 3), // inside Info's first child
      original.subarray(0, info.end - 2), // inside the last child of Info
    ];
    for (const truncated of cut) {
      expect(withWebmDuration(truncated, 4_000)).toBe(truncated);
    }
  });
});
