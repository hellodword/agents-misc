import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";
import { vi } from "vitest";

const storage = new Map<string, string>();
vi.stubGlobal("localStorage", {
  getItem: (key: string) => storage.get(key) ?? null,
  setItem: (key: string, value: string) => storage.set(key, value),
  removeItem: (key: string) => storage.delete(key),
  clear: () => storage.clear(),
  key: (index: number) => [...storage.keys()][index] ?? null,
  get length() {
    return storage.size;
  },
});

class ResizeObserverMock {
  constructor(private callback: ResizeObserverCallback) {}
  observe(target: Element) {
    const size = { inlineSize: 800, blockSize: 160 };
    this.callback(
      [
        {
          target,
          contentRect: target.getBoundingClientRect(),
          borderBoxSize: [size],
          contentBoxSize: [size],
          devicePixelContentBoxSize: [size],
        } as ResizeObserverEntry,
      ],
      this as unknown as ResizeObserver,
    );
  }
  unobserve() {}
  disconnect() {}
}
vi.stubGlobal("ResizeObserver", ResizeObserverMock);
afterEach(cleanup);
vi.stubGlobal(
  "matchMedia",
  vi.fn(() => ({
    matches: false,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  })),
);
Object.defineProperty(HTMLElement.prototype, "getBoundingClientRect", {
  value() {
    return {
      width: 800,
      height: 160,
      top: 0,
      left: 0,
      right: 800,
      bottom: 160,
      x: 0,
      y: 0,
      toJSON() {
        return {};
      },
    };
  },
});
Object.defineProperty(HTMLElement.prototype, "clientHeight", {
  configurable: true,
  value: 800,
});
Object.defineProperty(HTMLElement.prototype, "clientWidth", {
  configurable: true,
  value: 800,
});
Object.defineProperty(HTMLElement.prototype, "scrollTo", {
  configurable: true,
  value: vi.fn(),
});

class EventSourceMock {
  static instances: EventSourceMock[] = [];
  listeners = new Map<string, EventListener[]>();
  onopen: ((event: Event) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  constructor(public url: string) {
    EventSourceMock.instances.push(this);
  }
  addEventListener(name: string, listener: EventListener) {
    this.listeners.set(name, [...(this.listeners.get(name) ?? []), listener]);
  }
  close() {}
  emit(name: string, data: unknown) {
    for (const listener of this.listeners.get(name) ?? [])
      listener(new MessageEvent(name, { data: JSON.stringify(data) }));
  }
}
vi.stubGlobal("EventSource", EventSourceMock);
