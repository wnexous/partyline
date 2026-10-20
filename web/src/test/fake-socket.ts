/** A minimal controllable WebSocket stand-in for live socket tests. */
export class FakeSocket {
  static instances: FakeSocket[] = [];
  onopen: (() => void) | null = null;
  onclose: (() => void) | null = null;
  onmessage: ((e: MessageEvent) => void) | null = null;
  closed = false;

  constructor(readonly url: string) {
    FakeSocket.instances.push(this);
  }

  static latest(): FakeSocket {
    const s = FakeSocket.instances.at(-1);
    if (!s) throw new Error("no socket created");
    return s;
  }

  static create = (url: string) => new FakeSocket(url) as unknown as WebSocket;

  open() {
    this.onopen?.();
  }

  emit(frame: unknown) {
    this.onmessage?.({
      data: typeof frame === "string" ? frame : JSON.stringify(frame),
    } as MessageEvent);
  }

  /** Server-side or network close. */
  drop() {
    this.onclose?.();
  }

  close() {
    this.closed = true;
    this.onclose?.();
  }
}
