// jsdom lays nothing out, and the virtualizer reads the log's
// clientHeight and every row's offsetHeight. stubLayout answers fixed
// sizes for both; call the function it returns to restore jsdom's own.
export function stubLayout({
  viewport = 600,
  row = 60,
}: {
  viewport?: number
  row?: number
} = {}): () => void {
  const client = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'clientHeight')
  const offset = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetHeight')
  Object.defineProperty(HTMLElement.prototype, 'clientHeight', {
    configurable: true,
    get(this: HTMLElement) {
      return this.dataset.testid === 'conversation-log' ? viewport : 0
    },
  })
  Object.defineProperty(HTMLElement.prototype, 'offsetHeight', {
    configurable: true,
    get(this: HTMLElement) {
      return this.dataset.index === undefined ? 0 : row
    },
  })
  return () => {
    restore('clientHeight', client)
    restore('offsetHeight', offset)
  }
}

function restore(
  key: 'clientHeight' | 'offsetHeight',
  descriptor: PropertyDescriptor | undefined,
) {
  if (descriptor === undefined) {
    Reflect.deleteProperty(HTMLElement.prototype, key)
  } else {
    Object.defineProperty(HTMLElement.prototype, key, descriptor)
  }
}

// scrollState gives one element a scroll position the component can read
// and write, and a scrollHeight a test can grow.
export function scrollState(
  element: HTMLElement,
  {
    scrollTop = 0,
    scrollHeight = 5000,
    clientHeight = 600,
  }: { scrollTop?: number; scrollHeight?: number; clientHeight?: number } = {},
): void {
  let top = scrollTop
  Object.defineProperty(element, 'scrollTop', {
    configurable: true,
    get: () => top,
    set: (value: number) => {
      top = value
    },
  })
  Object.defineProperty(element, 'scrollHeight', {
    configurable: true,
    writable: true,
    value: scrollHeight,
  })
  Object.defineProperty(element, 'clientHeight', { configurable: true, value: clientHeight })
}
