/** A minimal controllable WebSocket stand-in for live socket tests. */
export class FakeSocket {
  static instances: FakeSocket[] = [];
  onopen: (() => void) | null = null;
  onclose: ((e: CloseEvent) => void) | null = null;
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

  /** Server-side or network close; 1006 (abnormal closure) unless a close code is given. */
  drop(code = 1006, reason = "") {
    this.onclose?.({ code, reason, wasClean: code !== 1006 } as CloseEvent);
  }

  close() {
    this.closed = true;
    this.onclose?.({ code: 1000, reason: "", wasClean: true } as CloseEvent);
  }
}
