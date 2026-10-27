/** FakeEventSource stands in for EventSource in tests; it never connects by itself. */
export class FakeEventSource {
  static instances: FakeEventSource[] = [];
  readonly url: string;
  readyState = 0;
  closed = false;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  private listeners: Record<string, ((event: MessageEvent) => void)[]> = {};

  constructor(url: string) {
    this.url = url;
    FakeEventSource.instances.push(this);
  }
  static latest(): FakeEventSource | undefined {
    return FakeEventSource.instances.filter((source) => !source.closed).at(-1);
  }
  addEventListener(type: string, listener: (event: MessageEvent) => void) {
    (this.listeners[type] ??= []).push(listener);
  }
  removeEventListener() {}
  close() {
    this.closed = true;
  }
  open() {
    this.readyState = 1;
    this.onopen?.();
  }
  fail() {
    this.onerror?.();
  }
  emit(type: string, data: unknown) {
    for (const listener of this.listeners[type] ?? [])
      listener(new MessageEvent(type, { data: JSON.stringify(data) }));
  }
}
