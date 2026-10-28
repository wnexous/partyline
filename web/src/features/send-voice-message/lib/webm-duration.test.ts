import { describe, expect, it } from "vitest";
import { normalizeWebm } from "./webm-duration";

/*
 * The real timeline and length of a WebM (VOI-08). The tests build the containers byte by byte, in
 * the two layouts Chrome writes (desktop: a Segment of unknown size and no Duration; Android: a
 * Segment of known size with a SeekHead, a Void and a four-byte Duration), and read the result back
 * with their own minimal EBML reader, so what is asserted is the file a player sees and not how the
 * writer works.
 */

/** The EBML IDs the tests use, with the bytes they are written as. */
const ID = {
  header: [0x1a, 0x45, 0xdf, 0xa3],
  doctype: [0x42, 0x82],
  segment: [0x18, 0x53, 0x80, 0x67],
  seekHead: [0x11, 0x4d, 0x9b, 0x74],
  seek: [0x4d, 0xbb],
  seekId: [0x53, 0xab],
  seekPosition: [0x53, 0xac],
  void: [0xec],
  info: [0x15, 0x49, 0xa9, 0x66],
  timecodeScale: [0x2a, 0xd7, 0xb1],
  muxingApp: [0x4d, 0x80],
  writingApp: [0x57, 0x41],
  duration: [0x44, 0x89],
  tracks: [0x16, 0x54, 0xae, 0x6b],
  cluster: [0x1f, 0x43, 0xb6, 0x75],
  timecode: [0xe7],
  simpleBlock: [0xa3],
} as const;

const SEGMENT_ID = 0x18538067;
const SEEK_HEAD_ID = 0x114d9b74;
const INFO_ID = 0x1549a966;
const DURATION_ID = 0x4489;
const CLUSTER_ID = 0x1f43b675;
const TIMECODE_ID = 0xe7;
const SIMPLE_BLOCK_ID = 0xa3;

/** The top-level IDs that end an unknown-size Cluster. */
const TOP_LEVEL = new Set([
  0x1a45dfa3,
  SEGMENT_ID,
  SEEK_HEAD_ID,
  INFO_ID,
  0x1654ae6b,
  0x1c53bb6b,
  0x1254c367,
  0x1043a770,
  0x1941a469,
  CLUSTER_ID,
]);

/** Bytes backed by an ArrayBuffer of their own. */
type Bytes = Uint8Array<ArrayBuffer>;

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

/** A Segment whose size is unknown: every bit of the eight-byte field is set, as Chrome writes it. */
const unknownSizeField = () => new Uint8Array([0x01, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff]);

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

function float32(value: number): Bytes {
  const bytes = new Uint8Array(4);
  new DataView(bytes.buffer).setFloat32(0, value, false);
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

const duration = (value: number, width: 4 | 8 = 8) =>
  element(ID.duration, width === 4 ? float32(value) : float64(value));

/** A SimpleBlock of the one audio track, at a relative time in ticks, with two bytes of sound. */
function simpleBlock(relTicks: number): Bytes {
  const time = new Uint8Array(2);
  new DataView(time.buffer).setInt16(0, relTicks, false);
  return element(
    ID.simpleBlock,
    concat([new Uint8Array([0x81]), time, new Uint8Array([0x80, 0, 0])]),
  );
}

/** A known-size Cluster: its Timecode and its blocks. */
function cluster(timecode: number, relatives: number[]): Bytes {
  return element(
    ID.cluster,
    concat([element(ID.timecode, uint(timecode)), ...relatives.map(simpleBlock)]),
  );
}

/** An unknown-size Cluster, which ends where the next top-level element begins. */
function unknownCluster(timecode: number, relatives: number[]): Bytes {
  return concat([
    new Uint8Array(ID.cluster),
    unknownSizeField(),
    element(ID.timecode, uint(timecode)),
    ...relatives.map(simpleBlock),
  ]);
}

/** A SeekHead of four-byte positions, which keep their length whatever offsets they hold. */
function seekHead(entries: { id: readonly number[]; position: number }[]): Bytes {
  return element(
    ID.seekHead,
    concat(
      entries.map(({ id, position }) =>
        element(
          ID.seek,
          concat([
            element(ID.seekId, new Uint8Array(id)),
            element(ID.seekPosition, uint(position, 4)),
          ]),
        ),
      ),
    ),
  );
}

/** The Android Chrome layout: a Segment of known size, SeekHead, Void, Info with a four-byte Duration. */
function androidWebm(options: { durationTicks?: number; clusters?: Bytes[] } = {}): Bytes {
  const clusters = options.clusters ?? [
    cluster(0, [0]),
    cluster(1_662_000, [0, 20, 40]),
    cluster(1_662_500, [0, 20]),
  ];
  const info = element(
    ID.info,
    concat([
      timecodeScale(1_000_000),
      duration(options.durationTicks ?? 1_936_186, 4),
      muxingApp("Chrome"),
      element(ID.writingApp, text("Chrome")),
    ]),
  );
  const tracks = element(ID.tracks, new Uint8Array([0x00, 0x00]));
  const empty = element(ID.void, new Uint8Array([0, 0, 0, 0]));
  // The SeekHead's length does not depend on the offsets (they are four bytes), so a first pass
  // fixes the layout and a second fills in where each element really starts, from the Segment's data.
  const placeholder = seekHead([
    { id: ID.info, position: 0 },
    { id: ID.tracks, position: 0 },
    { id: ID.cluster, position: 0 },
  ]);
  const infoAt = placeholder.length + empty.length;
  const tracksAt = infoAt + info.length;
  const clusterAt = tracksAt + tracks.length;
  const seek = seekHead([
    { id: ID.info, position: infoAt },
    { id: ID.tracks, position: tracksAt },
    { id: ID.cluster, position: clusterAt },
  ]);
  return concat([header(), element(ID.segment, concat([seek, empty, info, tracks, ...clusters]))]);
}

/** The desktop Chrome layout: a Segment of unknown size, an Info with no Duration, Clusters after. */
function desktopWebm(
  options: { scale?: number; withDuration?: number; clusters?: Bytes[] } = {},
): Bytes {
  const infoChildren = [timecodeScale(options.scale ?? 1_000_000), muxingApp("Chrome")];
  if (options.withDuration !== undefined) infoChildren.push(duration(options.withDuration));
  return concat([
    header(),
    new Uint8Array(ID.segment),
    unknownSizeField(),
    element(ID.info, concat(infoChildren)),
    ...(options.clusters ?? [cluster(0, [0, 20, 40])]),
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
  const dataEnd = value === null ? end : dataStart + value;
  if (dataEnd > end) throw new Error(`element ${id.id} runs past its parent`);
  return {
    id: id.id,
    start: offset,
    sizeStart: id.next,
    sizeWidth: width,
    dataStart,
    dataLength: value,
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
  let cursor = top.end;
  while (cursor < bytes.length) {
    const element = readElement(bytes, cursor, bytes.length);
    if (element.id === SEGMENT_ID) return element;
    cursor = element.end;
  }
  throw new Error("no Segment");
}

/** The Segment's children, giving each unknown-size Cluster the end its next top-level element sets. */
function segmentChildrenOf(bytes: Bytes, segment: Read): Read[] {
  const end = segment.dataLength === null ? bytes.length : segment.end;
  const found: Read[] = [];
  let cursor = segment.dataStart;
  while (cursor < end) {
    const child = readElement(bytes, cursor, end);
    if (child.dataLength === null) {
      let inside = child.dataStart;
      let stop = end;
      while (inside < end) {
        const grandchild = readElement(bytes, inside, end);
        if (TOP_LEVEL.has(grandchild.id)) {
          stop = inside;
          break;
        }
        inside = grandchild.end;
      }
      found.push({ ...child, dataLength: stop - child.dataStart, end: stop });
      cursor = stop;
      continue;
    }
    found.push(child);
    cursor = child.end;
  }
  return found;
}

function infoOf(bytes: Bytes): Read {
  const info = segmentChildrenOf(bytes, segmentOf(bytes)).find((child) => child.id === INFO_ID);
  if (!info) throw new Error("no Info");
  return info;
}

function durationOf(bytes: Bytes): Read | undefined {
  return childrenOf(bytes, infoOf(bytes)).find((child) => child.id === DURATION_ID);
}

/** The Duration as the file carries it: ticks of the TimecodeScale, a four- or eight-byte float. */
function durationValue(bytes: Bytes): number {
  const element = durationOf(bytes);
  if (!element || element.dataLength === null) throw new Error("no Duration");
  const view = new DataView(bytes.buffer, bytes.byteOffset + element.dataStart, element.dataLength);
  return element.dataLength === 4 ? view.getFloat32(0, false) : view.getFloat64(0, false);
}

function clustersOf(bytes: Bytes): Read[] {
  return segmentChildrenOf(bytes, segmentOf(bytes)).filter((child) => child.id === CLUSTER_ID);
}

function clusterTimecode(bytes: Bytes, cluster: Read): number {
  const timecode = childrenOf(bytes, cluster).find((child) => child.id === TIMECODE_ID);
  if (!timecode || timecode.dataLength === null) throw new Error("no Timecode");
  let value = 0;
  for (let i = 0; i < timecode.dataLength; i++) {
    value = value * 256 + (bytes[timecode.dataStart + i] as number);
  }
  return value;
}

function clusterTimecodes(bytes: Bytes): number[] {
  return clustersOf(bytes).map((cluster) => clusterTimecode(bytes, cluster));
}

/** The relative times the blocks of a Cluster carry, in ticks. */
function relativeTimes(bytes: Bytes, cluster: Read): number[] {
  return childrenOf(bytes, cluster)
    .filter((child) => child.id === SIMPLE_BLOCK_ID)
    .map((block) => {
      if (block.dataLength === null) throw new Error("a block has no size");
      const first = bytes[block.dataStart] as number;
      let trackWidth = 1;
      while (trackWidth <= 8 && (first & (0x80 >> (trackWidth - 1))) === 0) trackWidth++;
      return new DataView(
        bytes.buffer,
        bytes.byteOffset + block.dataStart + trackWidth,
        2,
      ).getInt16(0, false);
    });
}

describe("normalizeWebm (VOI-08)", () => {
  it("closes a false gap between Clusters and writes the length in place (Android Chrome layout)", () => {
    const original = androidWebm();
    const before = durationOf(original);
    expect(before?.dataLength).toBe(4);
    expect(durationValue(original)).toBe(1_936_186);

    const patched = normalizeWebm(original, 4_000);

    // Every element keeps its size: the file is the same length and the SeekHead still points right.
    expect(patched.length).toBe(original.length);
    expect(clusterTimecodes(patched)).toEqual([0, 20, 520]);
    expect(durationValue(patched)).toBe(4_000);
    expect(durationOf(patched)?.dataLength).toBe(4);
    expect(durationOf(patched)?.start).toBe(before?.start);
    // The blocks themselves keep their relative times; only the Clusters' Timecodes moved.
    const clusters = clustersOf(patched);
    expect(relativeTimes(patched, clusters[1] as Read)).toEqual([0, 20, 40]);
    expect(relativeTimes(patched, clusters[2] as Read)).toEqual([0, 20]);
    // The SeekHead was not touched.
    const seekHeadBefore = segmentChildrenOf(original, segmentOf(original)).find(
      (child) => child.id === SEEK_HEAD_ID,
    ) as Read;
    const seekHeadAfter = segmentChildrenOf(patched, segmentOf(patched)).find(
      (child) => child.id === SEEK_HEAD_ID,
    ) as Read;
    expect(Array.from(patched.subarray(seekHeadAfter.start, seekHeadAfter.end))).toEqual(
      Array.from(original.subarray(seekHeadBefore.start, seekHeadBefore.end)),
    );
    // The only bytes that changed are the Duration and the later Clusters' Timecodes.
    let changed = 0;
    for (let i = 0; i < original.length; i++) if (original[i] !== patched[i]) changed++;
    expect(changed).toBeGreaterThan(0);
  });

  it("writes the length into a Segment that carries no Duration (desktop Chrome layout)", () => {
    const original = desktopWebm();
    expect(durationOf(original)).toBeUndefined();

    const patched = normalizeWebm(original, 4_000);

    // An eight-byte Duration goes first in Info: two bytes of ID, one of size and the float.
    expect(patched.length).toBe(original.length + 11);
    expect(durationOf(patched)?.dataLength).toBe(8);
    expect(durationValue(patched)).toBe(4_000);
    // The blocks are untouched and the Segment still says it has no size.
    expect(clusterTimecodes(patched)).toEqual([0]);
    expect(relativeTimes(patched, clustersOf(patched)[0] as Read)).toEqual([0, 20, 40]);
    expect(segmentOf(patched).dataLength).toBeNull();
  });

  it("counts the length in the TimecodeScale the file declares", () => {
    const patched = normalizeWebm(desktopWebm({ scale: 500_000 }), 4_000);
    expect(durationValue(patched)).toBe(8_000);
  });

  it("leaves a file with no gap and the length it already carries exactly as it came", () => {
    const original = androidWebm({
      durationTicks: 4_000,
      clusters: [cluster(0, [0]), cluster(40, [0]), cluster(80, [0])],
    });
    const patched = normalizeWebm(original, 4_000);
    expect(Array.from(patched)).toEqual(Array.from(original));
    expect(clusterTimecodes(patched)).toEqual([0, 40, 80]);
    expect(durationValue(patched)).toBe(4_000);
  });

  it("closes a gap inside one Cluster by rewriting its blocks' relative times", () => {
    const original = androidWebm({
      durationTicks: 6_000,
      clusters: [cluster(0, [0, 5_000, 5_020])],
    });
    const patched = normalizeWebm(original, 6_000);
    expect(patched.length).toBe(original.length);
    expect(clusterTimecodes(patched)).toEqual([0]);
    expect(relativeTimes(patched, clustersOf(patched)[0] as Read)).toEqual([0, 20, 40]);
    expect(durationValue(patched)).toBe(6_000);
  });

  it("leaves a Cluster whose relative times cannot hold the shift as it is", () => {
    const original = androidWebm({
      durationTicks: 3_000,
      clusters: [cluster(0, [0, 20_000, -20_000])],
    });
    const patched = normalizeWebm(original, 3_000);
    expect(Array.from(patched)).toEqual(Array.from(original));
    expect(relativeTimes(patched, clustersOf(patched)[0] as Read)).toEqual([0, 20_000, -20_000]);
  });

  it("walks an unknown-size Cluster up to the next top-level element", () => {
    const original = concat([
      header(),
      new Uint8Array(ID.segment),
      unknownSizeField(),
      element(ID.info, timecodeScale(1_000_000)),
      unknownCluster(0, [0]),
      cluster(1_662_000, [0, 20]),
    ]);
    const patched = normalizeWebm(original, 4_000);
    expect(clusterTimecodes(patched)).toEqual([0, 20]);
    expect(durationValue(patched)).toBe(4_000);
  });

  it.each<[string, Bytes]>([
    ["an MP4", new Uint8Array([0x00, 0x00, 0x00, 0x18, 0x66, 0x74, 0x79, 0x70])],
    ["text", text("not a webm at all")],
    ["nothing", new Uint8Array(0)],
    ["a random byte", new Uint8Array([0x1a])],
  ])("returns %s exactly as it came", (_name, bytes) => {
    expect(() => normalizeWebm(bytes, 4_000)).not.toThrow();
    expect(normalizeWebm(bytes, 4_000)).toBe(bytes);
  });

  it("returns a file that is cut short exactly as it came", () => {
    const android = androidWebm();
    const segment = segmentOf(android);
    const info = infoOf(android);
    const desktop = desktopWebm();
    const cut = [
      android.subarray(0, 4), // the header's ID alone
      android.subarray(0, 8), // the header's ID and no size
      android.subarray(0, segment.start + 2), // the Segment's ID alone
      android.subarray(0, segment.dataStart + 3), // inside the SeekHead
      android.subarray(0, info.start + 2), // the Info's ID alone
      android.subarray(0, info.dataStart + 3), // inside Info's first child
      android.subarray(0, info.end - 2), // inside the last child of Info
      android.subarray(0, segment.end - 3), // inside the last Cluster
      desktop.subarray(0, desktop.length - 3), // inside the last Cluster of an unknown Segment
    ];
    for (const truncated of cut) {
      expect(() => normalizeWebm(truncated, 4_000)).not.toThrow();
      expect(normalizeWebm(truncated, 4_000)).toBe(truncated);
    }
  });

  it("leaves a file alone for a length that is not a time", () => {
    const original = androidWebm();
    expect(normalizeWebm(original, Number.NaN)).toBe(original);
    expect(normalizeWebm(original, -1)).toBe(original);
  });
});
