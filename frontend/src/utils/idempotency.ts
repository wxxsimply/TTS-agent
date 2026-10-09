export function createIdempotencyKey(): string {
  try {
    if (typeof window.crypto?.randomUUID === 'function') {
      return window.crypto.randomUUID()
    }
    if (typeof window.crypto?.getRandomValues === 'function') {
      const bytes = new Uint8Array(16)
      window.crypto.getRandomValues(bytes)
      return Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('')
    }
  } catch {
    // Some browsers expose Web Crypto on plain HTTP but reject individual methods.
  }
  return `run-${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}-${Math.random().toString(36).slice(2)}`
}
